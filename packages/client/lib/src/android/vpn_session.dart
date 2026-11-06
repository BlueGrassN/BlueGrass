import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/foundation.dart';

import 'platform/vpn_client.dart';
import 'resource_presentation.dart';

/// Coordinates ephemeral requests. The Android service owns connection state.
class VpnSession extends ChangeNotifier {
  VpnSession({
    required this._source,
    this._client,
    required this._clock,
    this._refreshInterval = const Duration(minutes: 1),
  });

  final CustomerDataSource _source;
  final VpnClient? _client;
  final DateTime Function() _clock;
  final Duration _refreshInterval;
  StreamSubscription<VpnSnapshot>? _subscription;
  Map<String, VpnSnapshot>? _recoveryEvents;
  Timer? _refreshTimer;
  Future<void> _mutations = Future.value();
  Future<void>? _eligibilityRefresh;
  String? _accountKey, _requestId, _resourceId;
  final _retiredRuntimes = <String>{}, _retiredRequests = <String>{};
  int _operation = 0, _observationRevision = 0;
  bool _disposed = false, _acceptEvents = false, _synchronized = false;
  bool busy = false, probing = false;
  bool _probeFailed = false;
  VpnSnapshot? snapshot;
  ResourceItem? activeResource;
  ResourceItem? _pendingResource;
  String? _pendingRequestId;
  String? error;
  String routingMode = 'global';

  bool get available => _client != null;
  VpnState get state => snapshot?.state ?? VpnState.idle;
  bool get active => snapshot?.tunActive == true || state != VpnState.idle;
  bool get canConnect =>
      available &&
      _synchronized &&
      _acceptEvents &&
      !busy &&
      state != VpnState.connecting &&
      _accountKey != null;
  VpnObservationState get observationState => _probeFailed
      ? VpnObservationState.failed
      : probing
      ? VpnObservationState.checking
      : snapshot?.observationState ?? VpnObservationState.unknown;
  String? get observedExitIp =>
      state == VpnState.connected &&
          observationState == VpnObservationState.available
      ? snapshot?.observedExitIp
      : null;
  DateTime? get _effectiveNow =>
      _source is ApiV2DataSource ? _source.effectiveNowUtc : _clock().toUtc();
  bool _current(int operation) => !_disposed && operation == _operation;
  void _changed() {
    if (!_disposed) notifyListeners();
  }

  Future<T> _serialize<T>(Future<T> Function() action) {
    final result = _mutations.then((_) => action());
    _mutations = result.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    return result;
  }

  void _listen() {
    _subscription ??= _client?.snapshots.listen(
      (value) {
        final buffer = _recoveryEvents;
        if (!_disposed && buffer != null) {
          if (value.generation > (buffer[value.runtimeId]?.generation ?? -1)) {
            buffer[value.runtimeId] = value;
          }
          return;
        }
        if (!_acceptEvents || _disposed) return;
        _accept(value);
      },
      onError: (Object _) {
        if (!_disposed) {
          error = 'VPN 状态同步失败，请刷新重试。';
          _synchronized = false;
          _changed();
        }
      },
    );
  }

  ResourceKey? _key(String? id) {
    try {
      final parts = jsonDecode(id ?? '') as List;
      if (parts.length != 5 || parts.any((p) => p is! String)) return null;
      final key = ResourceKey.fromJson({
        'environment': parts[0],
        'dataSource': parts[1],
        'accountKey': parts[2],
        'kind': parts[3],
        'sourceId': parts[4],
      });
      if (key.accountKey != _accountKey || key.kind != 'subscription') {
        return null;
      }
      if (_source is ApiV2DataSource &&
          (key.environment != _source.environment ||
              key.dataSource != 'api_v2')) {
        return null;
      }
      return key;
    } catch (_) {
      return null;
    }
  }

  bool _accept(
    VpnSnapshot value, {
    bool recovering = false,
    bool stopped = false,
  }) {
    if (_disposed || _retiredRuntimes.contains(value.runtimeId)) return false;
    final previous = snapshot;
    if (previous != null) {
      if (previous.runtimeId == value.runtimeId &&
          value.generation <= previous.generation) {
        return false;
      }
      if (previous.runtimeId != value.runtimeId) {
        if (!recovering && !stopped) return false;
      }
    }
    if (value.state != VpnState.idle) {
      if (value.accountKey != _accountKey ||
          _key(value.resourceKey) == null ||
          _retiredRequests.contains(value.requestId)) {
        return false;
      }
      if (!recovering &&
          (value.requestId != _requestId || value.resourceKey != _resourceId)) {
        return false;
      }
      if (recovering) {
        _requestId = value.requestId;
        _resourceId = value.resourceKey;
      }
    } else {
      if (_requestId != null) _retiredRequests.add(_requestId!);
      _requestId = _resourceId = null;
      activeResource = null;
      _pendingResource = null;
      _pendingRequestId = null;
    }
    if (previous != null) {
      if (previous.runtimeId != value.runtimeId) {
        _retiredRuntimes.add(previous.runtimeId);
      }
      if (previous.requestId != null && previous.requestId != value.requestId) {
        _retiredRequests.add(previous.requestId!);
      }
    }
    if (previous?.state != value.state ||
        previous?.requestId != value.requestId ||
        previous?.runtimeId != value.runtimeId) {
      _observationRevision++;
      probing = false;
    }
    snapshot = value;
    if (value.requestId != null && value.requestId == _pendingRequestId) {
      activeResource = _pendingResource;
      _pendingResource = null;
      _pendingRequestId = null;
    }
    if (value.state != VpnState.connected) probing = false;
    _probeFailed = false;
    _scheduleRefresh();
    _changed();
    return true;
  }

  void _scheduleRefresh() {
    if (!active || !_acceptEvents) {
      _refreshTimer?.cancel();
      _refreshTimer = null;
    } else {
      _refreshTimer ??= Timer.periodic(_refreshInterval, (_) {
        unawaited(refreshEligibility());
      });
    }
  }

  /// Invalidate asynchronous preparation/probes immediately, before awaiting stop.
  void beginAccountChange() {
    _operation++;
    _acceptEvents = false;
    _recoveryEvents = null;
    busy = probing = false;
    _eligibilityRefresh = null;
    _refreshTimer?.cancel();
    _refreshTimer = null;
  }

  void cancelAccountChange() {
    if (_disposed) return;
    _acceptEvents = _accountKey != null;
    _scheduleRefresh();
    _changed();
  }

  /// A successful call is proof of an idle service, not just a sent command.
  Future<void> stop() async {
    if (_client == null) return;
    final stopped = await _serialize(_client.disconnect);
    if (stopped.state != VpnState.idle || stopped.tunActive) {
      throw const VpnFailure('STOP_FAILED');
    }
    final previous = snapshot;
    if (previous != null &&
        previous.runtimeId == stopped.runtimeId &&
        (stopped.generation < previous.generation ||
            (previous.state != VpnState.idle &&
                stopped.generation == previous.generation))) {
      throw const VpnFailure('STOP_FAILED');
    }
    _accept(stopped, stopped: true);
    if (_requestId != null) _retiredRequests.add(_requestId!);
    _requestId = _resourceId = _pendingRequestId = null;
    _pendingResource = activeResource = null;
    _synchronized = true;
  }

  Future<void> recover(String? accountKey) async {
    if (_client == null || _disposed) return;
    final operation = ++_operation;
    _accountKey = accountKey;
    _acceptEvents = false;
    _synchronized = false;
    busy = probing = false;
    _eligibilityRefresh = null;
    _recoveryEvents = {};
    _listen();
    try {
      var current = await _serialize(_client.status);
      if (!_current(operation)) return;
      final event = _recoveryEvents?[current.runtimeId];
      if (event != null && event.generation > current.generation) {
        current = event;
      }
      if ((current.state != VpnState.idle || current.tunActive) &&
          (accountKey == null ||
              current.accountKey != accountKey ||
              _key(current.resourceKey) == null)) {
        await stop();
        if (!_current(operation)) return;
      } else {
        if (_retiredRuntimes.contains(current.runtimeId) ||
            (current.requestId != null &&
                _retiredRequests.contains(current.requestId))) {
          throw const VpnFailure('STALE_STATUS');
        }
        // An unchanged status is still authoritative for a rejected connect.
        // Do not leave its provisional request ID installed after failure.
        if (snapshot?.runtimeId == current.runtimeId &&
            snapshot?.generation == current.generation) {
          _requestId = current.requestId;
          _resourceId = current.resourceKey;
        }
        _accept(current, recovering: true);
      }
      _synchronized = true;
      _recoveryEvents = null;
      _pendingResource = null;
      _pendingRequestId = null;
      _acceptEvents = accountKey != null;
      error = null;
      _scheduleRefresh();
      await refreshEligibility();
    } catch (_) {
      if (_current(operation)) {
        error = '无法确认 VPN 状态，请重试。';
        throw const VpnFailure('VPN_UNAVAILABLE');
      }
    } finally {
      if (_current(operation)) {
        _recoveryEvents = null;
        _changed();
      }
    }
  }

  Future<void> connect(ResourceItem selected) async {
    if (!canConnect || selected.key.accountKey != _accountKey) return;
    // Resource switching/retry retains the live policy until an explicit stop.
    final mode = active ? snapshot?.routingMode ?? routingMode : routingMode;
    final operation = ++_operation;
    busy = true;
    probing = false;
    _probeFailed = false;
    error = null;
    _changed();
    try {
      if (!await _client!.prepare()) {
        if (_current(operation)) error = '未获得 VPN 授权，请允许后重试。';
        return;
      }
      if (!_current(operation)) return;
      final node = await _source.getNode(selected.key.sourceId);
      if (!_current(operation)) return;
      final now = _effectiveNow;
      if (now == null) throw const VpnFailure('SERVER_TIME_UNAVAILABLE');
      if (node.key != selected.key || node.key.accountKey != _accountKey) {
        throw const VpnFailure('RESOURCE_UNAVAILABLE');
      }
      final result = prepareProfile(node, now);
      if (result is Rejected && result.error.code == 'ELIG-008') {
        throw const VpnFailure('XRAY_UNSUPPORTED_PROTOCOL');
      }
      if (result is! Ready) throw const VpnFailure('RESOURCE_UNAVAILABLE');
      final profile = result.profile;
      await _serialize(() async {
        if (!_current(operation)) return;
        // Use the effective time at dispatch, including time spent awaiting
        // an earlier native command. Queuing must never extend a deadline.
        final dispatchNow = _effectiveNow;
        if (dispatchNow == null) {
          throw const VpnFailure('SERVER_TIME_UNAVAILABLE');
        }
        if (prepareProfile(node, dispatchNow) is! Ready) {
          throw const VpnFailure('RESOURCE_UNAVAILABLE');
        }
        final config = _encodeProfile(profile, dispatchNow, mode);
        _requestId = newVpnRequestId();
        _resourceId = node.key.stableId;
        _pendingRequestId = _requestId;
        _pendingResource = ResourceItem.fromNode(node, dispatchNow);
        final value = await _client.connect(
          VpnConnectRequest(
            requestId: _requestId!,
            resourceKey: _resourceId!,
            accountKey: _accountKey!,
            name: node.name,
            config: config,
          ),
        );
        if (!_current(operation)) return;
        _accept(value);
        if (snapshot?.requestId != _requestId ||
            snapshot?.resourceKey != _resourceId) {
          throw const VpnFailure('CONNECT_UNCONFIRMED');
        }
      });
    } catch (failure) {
      if (_current(operation)) {
        // Native rejection can leave the old session running. Query it instead
        // of inventing a failure state or discarding its subsequent events.
        if (_requestId != snapshot?.requestId) {
          final recoveryOperation = _operation + 1;
          try {
            await recover(_accountKey);
          } catch (_) {
            /* fail closed */
          }
          if (!_current(recoveryOperation)) return;
        }
        if (_disposed || !_acceptEvents) return;
        error =
            failure is VpnFailure && failure.code == 'SERVER_TIME_UNAVAILABLE'
            ? '服务端时间尚未确认，请刷新资源后重试。'
            : failure is VpnFailure &&
                  failure.code == 'XRAY_UNSUPPORTED_PROTOCOL'
            ? '当前 Xray 内核不支持此协议，请选择其他资源。'
            : '连接未完成，请刷新资源并重试。';
        _changed();
      }
    } finally {
      if (_current(operation)) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> disconnect() async {
    beginAccountChange();
    busy = true;
    error = null;
    _changed();
    final operation = _operation;
    try {
      await stop();
    } catch (_) {
      if (_current(operation)) error = 'VPN 停止失败，请重试。';
    } finally {
      if (_current(operation)) {
        busy = false;
        cancelAccountChange();
      }
    }
  }

  // Native owns both transport identity and the monotonic expiry timer. Only a
  // successful authoritative refresh may replace that lease (including renewal).
  // Neither a UI timer nor reconnecting a network creates a second deadline.
  String _encodeProfile(ConnectionProfile profile, DateTime now, String mode) {
    final protocol =
        profile.protocolConfig ??
        ProtocolConfig.legacySocks(
          username: profile.username,
          password: profile.password,
        );
    final config = jsonEncode({
      'version': 1,
      'host': profile.host,
      'port': profile.port,
      'protocolConfig': protocol.toJson(),
      'expiresAtUtc': profile.validUntilUtc?.toIso8601String(),
      'effectiveNowUtc': now.toUtc().toIso8601String(),
      'routingMode': mode,
    });
    if (utf8.encode(config).length > 32768) {
      throw const VpnFailure('CONFIG_TOO_LARGE');
    }
    return config;
  }

  void noteSyncFailure() {
    if (_disposed || !active) return;
    error = '资源同步失败，当前会话仍按上次确认的有效期运行。';
    _changed();
  }

  String? _revocationReason(Object failure) {
    if (failure is! ApiFailure) return null;
    if (failure.status == 401 ||
        failure.status == 429 ||
        (failure.status != null && failure.status! >= 500)) {
      return null;
    }
    if ({'resource_expired', 'node_expired'}.contains(failure.code)) {
      return 'RESOURCE_EXPIRED';
    }
    if ({
          'resource_revoked',
          'node_revoked',
          'resource_disabled',
          'node_disabled',
        }.contains(failure.code) ||
        (failure.status == 404 && _source.customer?.id == _accountKey)) {
      return 'RESOURCE_UNAVAILABLE';
    }
    return null;
  }

  Future<void> refreshEligibility() {
    if (!_acceptEvents || !active || _resourceId == null || _disposed) {
      return Future.value();
    }
    if (_eligibilityRefresh != null) return _eligibilityRefresh!;
    late final Future<void> pending;
    pending = _refreshEligibility().whenComplete(() {
      if (identical(_eligibilityRefresh, pending)) _eligibilityRefresh = null;
    });
    return _eligibilityRefresh = pending;
  }

  Future<void> _refreshEligibility() async {
    final operation = _operation, request = _requestId;
    final runtime = snapshot?.runtimeId, account = _accountKey;
    final key = _key(_resourceId);
    if (key == null || runtime == null || request == null || account == null) {
      return;
    }
    bool current() =>
        _current(operation) &&
        request == _requestId &&
        runtime == snapshot?.runtimeId &&
        account == _accountKey &&
        key.stableId == _resourceId &&
        _acceptEvents &&
        active;
    late final NodeRecord node;
    try {
      node = await _source.getNode(key.sourceId);
    } catch (failure) {
      if (!current()) return;
      final reason = _revocationReason(failure);
      if (reason != null) {
        await pause(reason);
      } else {
        noteSyncFailure();
      }
      return;
    }
    if (!current()) return;
    if (node.key != key) {
      noteSyncFailure();
      return;
    }
    if (!node.isForwarded) {
      await pause('RESOURCE_UNAVAILABLE');
      return;
    }
    final now = _effectiveNow;
    if (now == null) {
      noteSyncFailure();
      return;
    }
    final result = prepareProfile(node, now);
    if (result is! Ready) {
      if (result is Rejected && result.error.code == 'ELIG-003') {
        await pause('RESOURCE_EXPIRED');
      } else {
        noteSyncFailure();
      }
      return;
    }
    var sent = false;
    try {
      await _serialize(() async {
        if (!current()) return;
        final dispatchNow = _effectiveNow;
        if (dispatchNow == null) {
          throw const VpnFailure('SERVER_TIME_UNAVAILABLE');
        }
        final fresh = prepareProfile(node, dispatchNow);
        if (fresh is! Ready) throw const VpnFailure('RESOURCE_EXPIRED');
        final config = _encodeProfile(
          fresh.profile,
          dispatchNow,
          snapshot!.routingMode,
        );
        final generation = snapshot!.generation;
        sent = true;
        final value = await _client!.updateResource(
          runtimeId: runtime,
          requestId: request,
          accountKey: account,
          resourceKey: key.stableId,
          config: config,
        );
        if (!current()) return;
        if (value.runtimeId != runtime ||
            value.requestId != request ||
            value.accountKey != account ||
            value.resourceKey != key.stableId ||
            value.generation < generation) {
          throw const VpnFailure('INVALID_UPDATE');
        }
        _accept(value);
        if (snapshot?.errorCode == 'CONFIG_CHANGED' ||
            value.errorCode == 'CONFIG_CHANGED') {
          error = '资源连接配置已变更，连接已暂停，请重新连接。';
        } else if (state == VpnState.connected ||
            state == VpnState.connecting ||
            state == VpnState.paused) {
          activeResource = ResourceItem.fromNode(node, dispatchNow);
          error = null;
        }
        _changed();
      });
    } catch (failure) {
      if (!current()) return;
      if (failure is VpnFailure && failure.code == 'RESOURCE_EXPIRED') {
        await pause('RESOURCE_EXPIRED');
      } else if (sent) {
        // An unavailable/invalid native comparison cannot establish that this
        // validated config still matches the running one. Keep TUN, stop core.
        await pause('ELIGIBILITY_UNCONFIRMED');
      } else {
        noteSyncFailure();
      }
    }
  }

  Future<void> pause(String reason) async {
    if (_client == null || !active || _disposed) return;
    final operation = ++_operation, request = _requestId;
    busy = probing = false;
    try {
      await _serialize(() async {
        if (!_current(operation) || request != _requestId) return;
        final value = await _client.pause(reason: reason);
        if (_current(operation) && request == _requestId) {
          _accept(value);
          error = '资源已失效或暂时无法确认，连接已暂停。请刷新后重新连接。';
        }
      });
    } catch (_) {
      if (_current(operation)) error = 'VPN 暂停失败，请立即重试停止连接。';
    } finally {
      if (_current(operation)) _changed();
    }
  }

  Future<void> observeExit() async {
    if (_client == null ||
        state != VpnState.connected ||
        busy ||
        probing ||
        !_acceptEvents) {
      return;
    }
    final operation = _operation,
        request = _requestId,
        runtime = snapshot?.runtimeId,
        observationRevision = _observationRevision;
    bool current() =>
        _current(operation) &&
        observationRevision == _observationRevision &&
        request == _requestId &&
        runtime == snapshot?.runtimeId;
    probing = true;
    _probeFailed = false;
    _changed();
    try {
      final value = await _client.observeExit();
      if (current()) _accept(value);
    } catch (_) {
      if (current()) _probeFailed = true;
    } finally {
      if (current()) {
        probing = false;
        _changed();
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _operation++;
    _refreshTimer?.cancel();
    unawaited(_subscription?.cancel());
    super.dispose();
  }
}
