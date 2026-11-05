import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/services.dart';

import 'api_endpoint.dart';

/// GUI-owned HTTPS over a verified, physically bound Windows socket. The
/// resolver supplies numeric destinations only; null never enables system DNS.
/// TLS identity is pinned in native code once, before session restoration.
final class ControlHttpTransport implements HttpTransport {
  ControlHttpTransport({
    required this.resolveHost,
    this.unprotectedTransport,
    this.protectionActive,
    this.timeout = const Duration(seconds: 20),
  }) {
    // Builds share one bootstrap origin. Runtime API changes come exclusively
    // from startup configuration, not a dart-define-only transport bypass.
    const configuredApi = String.fromEnvironment(
      'BLUEGRASS_API_URL',
      defaultValue: 'https://client-api.sunip.vip/api/client/v1',
    );
    if (configuredApi != 'https://client-api.sunip.vip/api/client/v1') {
      throw const ApiFailure('unsupported_api_build');
    }
  }

  final Future<List<InternetAddress>?> Function(String host) resolveHost;
  final HttpTransport? unprotectedTransport;
  final bool Function()? protectionActive;
  final Duration timeout;
  static const _channel = MethodChannel('bluegrass/platform');
  static int _nextId = 0;
  final _pending = <String, _Pending>{};
  bool _disposed = false;
  bool _configured = false, _used = false;
  Uri _baseUri = Uri.parse(defaultApiUrl);

  Future<void> configure(Uri endpoint) async {
    if (_disposed ||
        _used ||
        (_configured && endpoint != _baseUri) ||
        !validApiBase(endpoint)) {
      throw const ApiFailure('insecure_endpoint');
    }
    await _channel.invokeMethod<void>('controlHttpConfigure', {
      'host': endpoint.host,
    });
    _baseUri = endpoint;
    _configured = true;
  }

  @override
  Future<ApiResponse> send(ApiRequest request) async {
    if (_disposed) throw const ApiFailure('cancelled');
    _used = true;
    if (timeout <= Duration.zero || timeout > const Duration(seconds: 20)) {
      throw const ApiFailure('invalid_transport_limits');
    }
    // Capture once at invocation. No DNS/route/TLS failure can select this
    // branch later. Main owns mode-transition/account-generation guards.
    bool active;
    try {
      active = protectionActive?.call() ?? true;
    } catch (_) {
      throw const ApiFailure('network_error');
    }
    final uri = request.uri;
    if (uri.scheme != 'https' ||
        uri.host != _baseUri.host ||
        uri.port != 443 ||
        uri.userInfo.isNotEmpty ||
        uri.hasFragment ||
        !(uri.path == '/api/client/v1' ||
            uri.path.startsWith('/api/client/v1/'))) {
      throw const ApiFailure('insecure_endpoint');
    }
    if (!active) return _sendUnprotected(request);
    final target = '${uri.path}${uri.hasQuery ? '?${uri.query}' : ''}';
    if (!const {'GET', 'POST'}.contains(request.method) ||
        target.length > 4096 ||
        target.codeUnits.any((c) => c <= 32 || c >= 127)) {
      throw const ApiFailure('invalid_request');
    }
    final headers = <String, String>{};
    for (final entry in request.headers.entries) {
      final name = entry.key.toLowerCase();
      if (!const {'authorization', 'accept', 'content-type'}.contains(name) ||
          headers.containsKey(name) ||
          entry.value.length > 4096 ||
          entry.value.codeUnits.any((c) => c < 32 || c >= 127)) {
        throw const ApiFailure('invalid_request');
      }
      headers[name] = entry.value;
    }
    Uint8List body;
    try {
      body = Uint8List.fromList(
        request.body == null ? [] : utf8.encode(jsonEncode(request.body)),
      );
    } catch (_) {
      throw const ApiFailure('invalid_request');
    }
    if (body.length > 4096) throw const ApiFailure('payload_too_large');
    if (request.method == 'GET' && body.isNotEmpty) {
      throw const ApiFailure('invalid_request');
    }
    final id = '${++_nextId}';
    final pending = _Pending();
    _pending[id] = pending;
    final elapsed = Stopwatch()..start();
    final timer = Timer(timeout, () => _cancel(id, pending, 'timeout'));
    Future<ApiResponse> perform() async {
      final addresses = await resolveHost(uri.host);
      if (pending.closed) throw const ApiFailure('cancelled');
      if (addresses == null ||
          addresses.isEmpty ||
          addresses.length > 8 ||
          addresses.any((ip) => !_numericCandidate(ip))) {
        throw const ApiFailure('control_dns_failed');
      }
      final remaining = timeout.inMilliseconds - elapsed.elapsedMilliseconds;
      if (remaining <= 0) throw const ApiFailure('timeout');
      pending.nativeStarted = true;
      final value = await _channel.invokeMethod<Object?>('controlHttpSend', {
        'id': id,
        'ip': addresses.first.address,
        'target': target,
        'method': request.method,
        'headers': headers,
        'body': body,
        'timeoutMs': remaining,
      });
      if (pending.closed) throw const ApiFailure('cancelled');
      if (value is! Map ||
          value['status'] is! int ||
          value['body'] is! Uint8List ||
          value['headers'] is! Map) {
        throw const ApiFailure('network_error');
      }
      final status = value['status'] as int;
      final bytes = value['body'] as Uint8List;
      if (bytes.length > 2 * 1024 * 1024) {
        throw const ApiFailure('response_too_large');
      }
      final resultHeaders = <String, String>{};
      for (final entry in (value['headers'] as Map).entries) {
        if (entry.key is! String || entry.value is! String) {
          throw const ApiFailure('network_error');
        }
        resultHeaders[(entry.key as String).toLowerCase()] =
            entry.value as String;
      }
      if (status < 200 || status > 599) throw const ApiFailure('network_error');
      return ApiResponse(status: status, body: bytes, headers: resultHeaders);
    }

    try {
      return await Future.any([perform(), pending.cancelled.future]);
    } on ApiFailure {
      rethrow;
    } on PlatformException catch (e) {
      const safe = {
        'timeout',
        'cancelled',
        'physical_route_unavailable',
        'tls_failed',
        'response_too_large',
        'invalid_response',
        'busy',
      };
      throw ApiFailure(safe.contains(e.code) ? e.code : 'network_error');
    } catch (_) {
      throw const ApiFailure('network_error');
    } finally {
      timer.cancel();
      elapsed.stop();
      pending.closed = true;
      _pending.remove(id);
    }
  }

  Future<ApiResponse> _sendUnprotected(ApiRequest request) async {
    final fallback = unprotectedTransport;
    if (fallback == null) throw const ApiFailure('network_error');
    final id = '${++_nextId}';
    final pending = _Pending();
    _pending[id] = pending;
    final timer = Timer(timeout, () => _cancel(id, pending, 'timeout'));
    try {
      return await Future.any([
        fallback.send(request),
        pending.cancelled.future,
      ]);
    } on ApiFailure {
      rethrow;
    } catch (_) {
      throw const ApiFailure('network_error');
    } finally {
      timer.cancel();
      pending.closed = true;
      _pending.remove(id);
    }
  }

  // Native code performs the authoritative destination/source/route checks.
  static bool _numericCandidate(InternetAddress address) {
    final ip = InternetAddress.tryParse(address.address);
    if (ip == null ||
        ip.isLoopback ||
        ip.isMulticast ||
        ip.isLinkLocal ||
        address.address.contains('%') ||
        ip.rawAddress.every((b) => b == 0)) {
      return false;
    }
    final b = ip.rawAddress;
    return !(b.length == 4 &&
        (b[0] == 0 ||
            b[0] >= 224 ||
            (b[0] == 198 && (b[1] == 18 || b[1] == 19))));
  }

  void _cancel(String id, _Pending pending, String code) {
    if (pending.closed) return;
    pending.closed = true;
    pending.cancelled.completeError(ApiFailure(code));
    if (pending.nativeStarted) {
      unawaited(
        _channel
            .invokeMethod<void>('controlHttpCancel', {'id': id})
            .catchError((Object _) {}),
      );
    }
  }

  /// Call when the controller/app that owns this transport is disposed. Late
  /// DNS/worker results cannot start requests or escape to another account.
  /// UnprotectedTransport has no cancellation interface: its own timeout owns
  /// underlying IO; disposal still immediately invalidates its returned result.
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    for (final entry in _pending.entries) {
      _cancel(entry.key, entry.value, 'cancelled');
    }
  }
}

final class _Pending {
  final cancelled = Completer<ApiResponse>();
  bool closed = false;
  bool nativeStarted = false;
}
