import 'dart:convert';
import 'dart:math';

import 'package:flutter/services.dart';

import '../routing_mode.dart';

enum VpnState { idle, connecting, connected, paused, error }

enum VpnObservationState { unknown, checking, available, failed }

/// Only fixed codes cross the presentation boundary; platform messages may
/// contain configuration or credentials and must never be surfaced.
class VpnFailure implements Exception {
  const VpnFailure(this.code);
  final String code;
  @override
  String toString() => 'VpnFailure';
}

class VpnSnapshot {
  const VpnSnapshot._({
    required this.runtimeId,
    required this.generation,
    required this.state,
    required this.requestId,
    required this.resourceKey,
    required this.accountKey,
    required this.tunActive,
    required this.errorCode,
    required this.observedExitIp,
    required this.observationState,
    required this.routingMode,
  });
  final String runtimeId;
  final int generation;
  final VpnState state;
  final String? requestId, resourceKey, accountKey, errorCode, observedExitIp;
  final bool tunActive;
  final VpnObservationState observationState;
  final String routingMode;

  factory VpnSnapshot.fromMap(Object? value) {
    if (value is! Map) throw const VpnFailure('INVALID_SNAPSHOT');
    final mode = value.containsKey('routingMode')
        ? value['routingMode']
        : 'global';
    if (!routingModes.contains(mode)) {
      throw const VpnFailure('INVALID_SNAPSHOT');
    }
    const fields = {
      'version',
      'runtimeId',
      'generation',
      'state',
      'requestId',
      'resourceKey',
      'accountKey',
      'tunActive',
      'errorCode',
      'observedExitIp',
      'observationState',
    };
    if (!fields.every(value.containsKey) ||
        value['version'] != 1 ||
        value['runtimeId'] is! String ||
        (value['runtimeId'] as String).isEmpty ||
        value['generation'] is! int ||
        (value['generation'] as int) < 0 ||
        value['tunActive'] is! bool ||
        [
          'requestId',
          'resourceKey',
          'accountKey',
          'errorCode',
          'observedExitIp',
        ].any((k) => value[k] != null && value[k] is! String)) {
      throw const VpnFailure('INVALID_SNAPSHOT');
    }
    final state = VpnState.values
        .where((s) => s.name == value['state'])
        .firstOrNull;
    final observation = VpnObservationState.values
        .where((s) => s.name == value['observationState'])
        .firstOrNull;
    if (state == null ||
        observation == null ||
        (state != VpnState.idle &&
            [
              'requestId',
              'resourceKey',
              'accountKey',
            ].any((k) => value[k] == null || (value[k] as String).isEmpty))) {
      throw const VpnFailure('INVALID_SNAPSHOT');
    }
    final ip = value['observedExitIp'] as String?;
    final octets = ip?.split('.');
    final validIp =
        octets?.length == 4 &&
        octets!.every(
          (s) =>
              RegExp(r'^(0|[1-9][0-9]{0,2})$').hasMatch(s) &&
              int.parse(s) <= 255,
        );
    const safeCodes = {
      'RESOURCE_EXPIRED',
      'RESOURCE_REVOKED',
      'CONFIG_CHANGED',
      'RESOURCE_UNAVAILABLE',
      'ELIGIBILITY_UNCONFIRMED',
      'SESSION_REVOKED',
      'VPN_REVOKED',
      'NETWORK_UNAVAILABLE',
      'NETWORK_CHANGED',
      'NATIVE_START_FAILED',
      'CLEANUP_FAILED',
      'EXIT_PROBE_FAILED',
      'SERVER_TIME_UNAVAILABLE',
      'PERMISSION_DENIED',
      'VPN_PERMISSION_REQUIRED',
      'TUN_SETUP_FAILED',
      'VPN_ERROR',
    };
    return VpnSnapshot._(
      runtimeId: value['runtimeId'] as String,
      generation: value['generation'] as int,
      state: state,
      requestId: value['requestId'] as String?,
      resourceKey: value['resourceKey'] as String?,
      accountKey: value['accountKey'] as String?,
      tunActive: value['tunActive'] as bool,
      errorCode: value['errorCode'] == null
          ? null
          : safeCodes.contains(value['errorCode'])
          ? value['errorCode'] as String
          : 'VPN_ERROR',
      observedExitIp: observation == VpnObservationState.available && validIp
          ? ip
          : null,
      observationState: observation == VpnObservationState.available && !validIp
          ? VpnObservationState.failed
          : observation,
      routingMode: mode as String,
    );
  }
}

/// Ephemeral transport only. Never store or print the config string.
class VpnConnectRequest {
  const VpnConnectRequest({
    required this.requestId,
    required this.resourceKey,
    required this.accountKey,
    required this.name,
    required this.config,
  });
  final String requestId, resourceKey, accountKey, name, config;
  Map<String, Object?> toMap() => {
    'requestId': requestId,
    'resourceKey': resourceKey,
    'accountKey': accountKey,
    'name': name,
    'config': config,
  };
  @override
  String toString() => 'VpnConnectRequest';
}

abstract interface class VpnClient {
  Stream<VpnSnapshot> get snapshots;
  Future<bool> prepare();
  Future<VpnSnapshot> connect(VpnConnectRequest request);
  Future<VpnSnapshot> updateResource({
    required String runtimeId,
    required String requestId,
    required String accountKey,
    required String resourceKey,
    required String config,
  });
  Future<VpnSnapshot> disconnect();
  Future<VpnSnapshot> pause({required String reason});
  Future<VpnSnapshot> status();
  Future<VpnSnapshot> observeExit();
}

class MethodChannelVpnClient implements VpnClient {
  static const _methods = MethodChannel('bluegrass/vpn');
  static const _events = EventChannel('bluegrass/vpn/events');
  late final Stream<VpnSnapshot> _snapshots = _events
      .receiveBroadcastStream()
      .map(VpnSnapshot.fromMap)
      .handleError((Object _) {
        throw const VpnFailure('VPN_UNAVAILABLE');
      });
  @override
  Stream<VpnSnapshot> get snapshots => _snapshots;

  Future<T> _invoke<T>(String method, [Object? args]) async {
    try {
      final value = await _methods.invokeMethod<T>(method, args);
      if (value == null) throw const VpnFailure('INVALID_SNAPSHOT');
      return value;
    } catch (_) {
      throw const VpnFailure('VPN_UNAVAILABLE');
    }
  }

  Future<VpnSnapshot> _snapshot(String method, [Object? args]) async =>
      VpnSnapshot.fromMap(await _invoke<Object>(method, args));
  @override
  Future<bool> prepare() => _invoke<bool>('prepare');
  @override
  Future<VpnSnapshot> connect(VpnConnectRequest request) async {
    if (utf8.encode(request.config).length > 32768) {
      throw const VpnFailure('CONFIG_TOO_LARGE');
    }
    return _snapshot('connect', request.toMap());
  }

  @override
  Future<VpnSnapshot> disconnect() => _snapshot('disconnect');
  @override
  Future<VpnSnapshot> updateResource({
    required String runtimeId,
    required String requestId,
    required String accountKey,
    required String resourceKey,
    required String config,
  }) async {
    if (utf8.encode(config).length > 32768) {
      throw const VpnFailure('CONFIG_TOO_LARGE');
    }
    return _snapshot('updateResource', {
      'runtimeId': runtimeId,
      'requestId': requestId,
      'accountKey': accountKey,
      'resourceKey': resourceKey,
      'config': config,
    });
  }

  @override
  Future<VpnSnapshot> pause({required String reason}) =>
      _snapshot('pause', {'reason': reason});
  @override
  Future<VpnSnapshot> status() => _snapshot('status');
  @override
  Future<VpnSnapshot> observeExit() => _snapshot('observeExit');
}

String newVpnRequestId() {
  final random = Random.secure();
  final bytes = List.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final hex = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}
