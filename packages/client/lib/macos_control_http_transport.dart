import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;

import 'api_endpoint.dart';

typedef NumericSocketConnector = Future<ConnectionTask<RawSocket>> Function(
  InternetAddress address,
  int port,
);

/// The authorized native host resolves API IPv4 addresses and owns their
/// physical /32 routes and PF permits. HTTP credentials stay in this process.
/// TLS still verifies the original API hostname against platform trust roots.
class MacControlHttpTransport implements HttpTransport {
  MacControlHttpTransport({
    required this.resolveHost,
    required this.protectionActive,
    required this.unprotectedTransport,
    this.timeout = const Duration(seconds: 20),
    this.maxResponseBytes = 2 * 1024 * 1024,
    this._securityContext,
    NumericSocketConnector? connect,
  }) : _connect = connect ?? ((ip, port) => RawSocket.startConnect(ip, port));

  final Future<List<InternetAddress>?> Function(String) resolveHost;
  final bool Function() protectionActive;
  final HttpTransport unprotectedTransport;
  final Duration timeout;
  final int maxResponseBytes;
  final SecurityContext? _securityContext;
  final NumericSocketConnector _connect;
  final _pending = <_MacRequest>{};
  Uri _base = Uri.parse(defaultApiUrl);
  bool _used = false, _configured = false, _disposed = false;

  Future<void> configure(Uri endpoint) async {
    if (_disposed ||
        _used ||
        (_configured && endpoint != _base) ||
        !validApiBase(endpoint)) {
      throw const ApiFailure('insecure_endpoint');
    }
    _base = endpoint;
    _configured = true;
  }

  @override
  Future<ApiResponse> send(ApiRequest request) async {
    if (_disposed) throw const ApiFailure('cancelled');
    _used = true;
    if (timeout <= Duration.zero ||
        timeout > const Duration(seconds: 20) ||
        maxResponseBytes <= 0 ||
        maxResponseBytes > 2 * 1024 * 1024) {
      throw const ApiFailure('invalid_transport_limits');
    }
    final uri = request.uri;
    if (uri.scheme != 'https' ||
        uri.host != _base.host ||
        uri.port != 443 ||
        uri.userInfo.isNotEmpty ||
        uri.hasFragment ||
        !(uri.path == '/api/client/v1' ||
            uri.path.startsWith('/api/client/v1/'))) {
      throw const ApiFailure('insecure_endpoint');
    }
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
    final List<int> body;
    try {
      body = request.body == null
          ? const []
          : utf8.encode(jsonEncode(request.body));
    } catch (_) {
      throw const ApiFailure('invalid_request');
    }
    if (body.length > 4096) throw const ApiFailure('payload_too_large');
    if (request.method == 'GET' && body.isNotEmpty) {
      throw const ApiFailure('invalid_request');
    }

    // Capture once: a failed protected request never becomes a direct retry,
    // even if the mode changes while its DNS result or TLS handshake is pending.
    final bool active;
    try {
      active = protectionActive();
    } catch (_) {
      throw const ApiFailure('network_error');
    }
    if (!active) return unprotectedTransport.send(request);
    if (_pending.length >= 16) throw const ApiFailure('busy');
    final operation = _MacRequest();
    _pending.add(operation);
    final timer = Timer(timeout, () => operation.cancel('timeout'));

    Future<ApiResponse> perform() async {
      final addresses = await resolveHost(uri.host);
      operation.check();
      if (addresses == null ||
          addresses.isEmpty ||
          addresses.length > 8 ||
          addresses.any(
            (ip) =>
                ip.type != InternetAddressType.IPv4 ||
                !domain.isPublicIpv4(ip.address),
          )) {
        throw const ApiFailure('control_dns_failed');
      }
      final client = HttpClient(context: _securityContext)
        ..connectionTimeout = timeout
        ..idleTimeout = Duration.zero
        ..findProxy = (_) => 'DIRECT';
      operation.client = client;
      client.connectionFactory = (url, proxyHost, proxyPort) async {
        operation.check();
        if (url.scheme != 'https' ||
            url.host != uri.host ||
            url.port != 443 ||
            proxyHost != null ||
            proxyPort != null) {
          throw const ApiFailure('insecure_endpoint');
        }
        final tcp = await _connect(addresses.first, 443);
        if (operation.closed) {
          tcp.cancel();
          unawaited(
            tcp.socket.then<void>(
              (socket) => socket.close(),
              onError: (Object _) {},
            ),
          );
          operation.check();
        }
        operation.tcp = tcp;
        Future<Socket> secure() async {
          final raw = await tcp.socket;
          operation.raw = raw;
          if (operation.closed) unawaited(raw.close());
          operation.check();
          try {
            // HttpClient does not add TLS to custom connectionFactory sockets.
            // Retain the actual TCP socket throughout TLS negotiation: upgrading
            // a Socket with SecureSocket.secure detaches its raw socket, making
            // destroy() on the old Socket ineffective during a stalled handshake.
            // The host override performs no DNS lookup and preserves both SNI
            // and certificate verification. Never accept bad certificates.
            final tls = await RawSecureSocket.secure(
              raw,
              host: uri.host,
              context: _securityContext,
              supportedProtocols: const ['http/1.1'],
            );
            if (operation.closed) {
              unawaited(tls.close());
              unawaited(raw.close());
              operation.check();
            }
            final socket = _TlsSocket(tls, raw);
            operation.secure = socket;
            return socket;
          } catch (_) {
            unawaited(raw.close());
            rethrow;
          }
        }

        return ConnectionTask.fromSocket<Socket>(
          secure(),
          operation.closeTransport,
        );
      };
      operation.check();
      final outgoing = await client.openUrl(request.method, uri);
      operation.check();
      outgoing.followRedirects = false;
      outgoing.persistentConnection = false;
      headers.forEach(outgoing.headers.set);
      if (body.isNotEmpty) outgoing.add(body);
      final incoming = await outgoing.close();
      operation.check();
      if (incoming.contentLength > maxResponseBytes) {
        throw const ApiFailure('response_too_large');
      }
      final bytes = <int>[];
      await for (final chunk in incoming) {
        operation.check();
        if (bytes.length + chunk.length > maxResponseBytes) {
          throw const ApiFailure('response_too_large');
        }
        bytes.addAll(chunk);
      }
      final responseHeaders = <String, String>{};
      incoming.headers.forEach(
        (name, values) =>
            responseHeaders[name.toLowerCase()] = values.join(', '),
      );
      return ApiResponse(
        status: incoming.statusCode,
        body: bytes,
        headers: responseHeaders,
      );
    }

    try {
      final response = await Future.any([
        perform(),
        operation.cancelled.future,
      ]);
      operation.check();
      return response;
    } catch (error) {
      // Socket closure can synchronously complete an HTTP read before the
      // cancellation future is delivered. Preserve the reason for that closure.
      operation.check();
      if (error is ApiFailure) rethrow;
      if (error is TlsException) throw const ApiFailure('tls_failed');
      if (error is TimeoutException) throw const ApiFailure('timeout');
      throw const ApiFailure('network_error');
    } finally {
      timer.cancel();
      operation.closed = true;
      operation.closeTransport();
      _pending.remove(operation);
    }
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    for (final operation in _pending.toList()) {
      operation.cancel('cancelled');
    }
  }
}

class _MacRequest {
  final cancelled = Completer<ApiResponse>();
  bool closed = false;
  bool _transportClosed = false;
  ApiFailure? _cancellation;
  HttpClient? client;
  ConnectionTask<RawSocket>? tcp;
  RawSocket? raw;
  Socket? secure;
  void check() {
    if (closed) throw _cancellation ?? const ApiFailure('cancelled');
  }

  void closeTransport() {
    // HttpClient.close also cancels its connection tasks, whose callback is
    // this method. Mark closure before calling back into HttpClient.
    if (_transportClosed) return;
    _transportClosed = true;
    tcp?.cancel();
    unawaited(raw?.close());
    secure?.destroy();
    client?.close(force: true);
  }

  void cancel(String code) {
    if (closed) return;
    _cancellation = ApiFailure(code);
    closed = true;
    if (!cancelled.isCompleted) cancelled.completeError(_cancellation!);
    closeTransport();
  }
}

/// Adapts an already authenticated raw TLS connection to HttpClient's public
/// Socket interface. Reads and writes apply backpressure independently, so a
/// paused HTTP reader cannot prevent request writes or cancellation.
class _TlsSocket extends Stream<Uint8List> implements Socket {
  _TlsSocket(this._tls, this._tcp) {
    _tls
      ..readEventsEnabled = false
      ..writeEventsEnabled = false;
    _writer = _TlsWriter(this);
    _sink = IOSink(_writer);
    // An error is also delivered to the read stream and any pending write.
    // HttpClient need not observe the sink's done future when it destroys us.
    unawaited(
      _sink.done.then<void>((_) {}, onError: (Object _, StackTrace _) {}),
    );
    _input
      ..onListen = () {
        _tls.readEventsEnabled = !_destroyed;
      }
      ..onPause = () {
        _tls.readEventsEnabled = false;
      }
      ..onResume = () {
        _tls.readEventsEnabled = !_destroyed;
      }
      ..onCancel = () {
        _tls.readEventsEnabled = false;
        _tls.shutdown(SocketDirection.receive);
      };
    _events = _tls.listen(_onEvent, onError: _fail, onDone: destroy);
  }

  final RawSecureSocket _tls;
  final RawSocket _tcp;
  final _input = StreamController<Uint8List>(sync: true);
  late final StreamSubscription<RawSocketEvent> _events;
  late final _TlsWriter _writer;
  late final IOSink _sink;
  bool _destroyed = false;

  void _onEvent(RawSocketEvent event) {
    if (_destroyed) return;
    try {
      switch (event) {
        case RawSocketEvent.read:
          final bytes = _tls.read();
          if (bytes != null) _input.add(bytes);
        case RawSocketEvent.write:
          _writer.ready();
        case RawSocketEvent.readClosed:
          _tls.readEventsEnabled = false;
          unawaited(_input.close());
        case RawSocketEvent.closed:
          destroy();
      }
    } catch (error, stack) {
      _fail(error, stack);
    }
  }

  void _fail(Object error, StackTrace stack) {
    if (!_input.isClosed) _input.addError(error, stack);
    destroy();
  }

  @override
  void destroy() {
    if (_destroyed) return;
    _destroyed = true;
    _writer.stop();
    // Closing TCP also aborts a TLS write that cannot flush to its peer.
    unawaited(_tcp.close());
    unawaited(_tls.close());
    unawaited(_events.cancel());
    unawaited(_input.close());
  }

  @override
  StreamSubscription<Uint8List> listen(
    void Function(Uint8List)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) => _input.stream.listen(
    onData,
    onError: onError,
    onDone: onDone,
    cancelOnError: cancelOnError,
  );

  @override
  Encoding get encoding => _sink.encoding;
  @override
  set encoding(Encoding value) => _sink.encoding = value;
  @override
  void add(List<int> data) => _sink.add(data);
  @override
  void write(Object? object) => _sink.write(object);
  @override
  void writeAll(Iterable<dynamic> objects, [String separator = '']) =>
      _sink.writeAll(objects, separator);
  @override
  void writeln([Object? object = '']) => _sink.writeln(object);
  @override
  void writeCharCode(int charCode) => _sink.writeCharCode(charCode);
  @override
  void addError(Object error, [StackTrace? stackTrace]) =>
      throw UnsupportedError('Cannot send errors on sockets');
  @override
  Future<dynamic> addStream(Stream<List<int>> stream) =>
      _sink.addStream(stream);
  @override
  Future<dynamic> flush() => _sink.flush();
  @override
  Future<dynamic> close() => _sink.close();
  @override
  Future<dynamic> get done => _sink.done;
  @override
  InternetAddress get address => _tls.address;
  @override
  InternetAddress get remoteAddress => _tls.remoteAddress;
  @override
  int get port => _tls.port;
  @override
  int get remotePort => _tls.remotePort;
  @override
  bool setOption(SocketOption option, bool enabled) =>
      _tls.setOption(option, enabled);
  @override
  Uint8List getRawOption(RawSocketOption option) => _tls.getRawOption(option);
  @override
  void setRawOption(RawSocketOption option) => _tls.setRawOption(option);
}

class _TlsWriter implements StreamConsumer<List<int>> {
  _TlsWriter(this.socket);

  final _TlsSocket socket;
  StreamIterator<List<int>>? _iterator;
  Completer<void>? _writable;

  void ready() {
    final writable = _writable;
    _writable = null;
    writable?.complete();
  }

  void stop() {
    ready();
    unawaited(_iterator?.cancel());
  }

  @override
  Future<Socket> addStream(Stream<List<int>> stream) async {
    if (socket._destroyed) throw const SocketException.closed();
    final iterator = _iterator = StreamIterator(stream);
    try {
      while (await iterator.moveNext()) {
        final bytes = iterator.current;
        var offset = 0;
        while (offset < bytes.length) {
          if (socket._destroyed) throw const SocketException.closed();
          offset += socket._tls.write(bytes, offset, bytes.length - offset);
          if (offset < bytes.length) {
            final writable = _writable = Completer<void>();
            socket._tls.writeEventsEnabled = true;
            await writable.future;
          }
        }
      }
      if (socket._destroyed) throw const SocketException.closed();
      return socket;
    } catch (error, stack) {
      socket._fail(error, stack);
      rethrow;
    } finally {
      _iterator = null;
      await iterator.cancel();
    }
  }

  @override
  Future<Socket> close() async {
    if (!socket._destroyed) socket._tls.shutdown(SocketDirection.send);
    return socket;
  }
}
