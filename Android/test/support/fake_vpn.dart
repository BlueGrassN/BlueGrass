import 'dart:async';
import 'dart:convert';

import 'package:client/platform/vpn_client.dart';

import 'fakes.dart';

VpnSnapshot snapshot({
  String runtime = '00000000-0000-4000-8000-000000000010',
  int generation = 1,
  String state = 'idle',
  String? request = '00000000-0000-4000-8000-000000000011',
  String account = '1',
  String? resource,
  String observation = 'unknown',
  String? ip,
  String? errorCode,
  String routingMode = 'global',
}) => VpnSnapshot.fromMap({
  'version': 1,
  'runtimeId': runtime,
  'generation': generation,
  'state': state,
  'requestId': state == 'idle' ? null : request,
  'resourceKey': state == 'idle' ? null : resource ?? node('1').key.stableId,
  'accountKey': state == 'idle' ? null : account,
  'tunActive': state == 'connected' || state == 'paused',
  'errorCode': errorCode,
  'observedExitIp': ip,
  'observationState': observation,
  'routingMode': routingMode,
});

class FakeVpnClient implements VpnClient {
  final events = StreamController<VpnSnapshot>.broadcast(sync: true);
  VpnSnapshot current = snapshot();
  bool permitted = true;
  int preparations = 0, disconnects = 0;
  final requests = <VpnConnectRequest>[];
  final pauses = <String>[];
  final updates = <Map<String, String>>[];
  Future<VpnSnapshot> Function(Map<String, String>)? onUpdate;
  Future<bool> Function()? onPrepare;
  Future<VpnSnapshot> Function(VpnConnectRequest)? onConnect;
  Future<VpnSnapshot> Function()? onDisconnect, onObserve, onStatus;
  @override
  Stream<VpnSnapshot> get snapshots => events.stream;
  @override
  Future<bool> prepare() async {
    preparations++;
    return onPrepare?.call() ?? permitted;
  }

  @override
  Future<VpnSnapshot> connect(VpnConnectRequest request) async {
    requests.add(request);
    return onConnect?.call(request) ??
        (current = snapshot(
          generation: current.generation + 1,
          state: 'connecting',
          request: request.requestId,
          resource: request.resourceKey,
          account: request.accountKey,
          routingMode:
              (jsonDecode(request.config) as Map)['routingMode'] as String? ??
              'global',
        ));
  }

  @override
  Future<VpnSnapshot> disconnect() async {
    disconnects++;
    return onDisconnect?.call() ??
        (current = snapshot(generation: current.generation + 1));
  }

  @override
  Future<VpnSnapshot> pause({required String reason}) async {
    pauses.add(reason);
    return current = snapshot(
      generation: current.generation + 1,
      state: 'paused',
      request: current.requestId,
      resource: current.resourceKey,
      account: current.accountKey ?? '1',
      routingMode: current.routingMode,
    );
  }

  @override
  Future<VpnSnapshot> status() async => onStatus?.call() ?? current;
  @override
  Future<VpnSnapshot> observeExit() async => onObserve?.call() ?? current;

  @override
  Future<VpnSnapshot> updateResource({
    required String runtimeId,
    required String requestId,
    required String accountKey,
    required String resourceKey,
    required String config,
  }) async {
    final update = {
      'runtimeId': runtimeId,
      'requestId': requestId,
      'accountKey': accountKey,
      'resourceKey': resourceKey,
      'config': config,
    };
    updates.add(update);
    return onUpdate?.call(update) ?? current;
  }

  void emit(VpnSnapshot value) {
    current = value;
    events.add(value);
  }
}
