import 'dart:convert';

import 'package:client/platform/vpn_client.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('bluegrass/vpn');
  final calls = <MethodCall>[];
  final idle = <String, Object?>{
    'version': 1,
    'runtimeId': '00000000-0000-4000-8000-000000000010',
    'generation': 3,
    'state': 'idle',
    'requestId': null,
    'resourceKey': null,
    'accountKey': null,
    'tunActive': false,
    'errorCode': null,
    'observedExitIp': null,
    'observationState': 'unknown',
  };
  setUp(() {
    calls.clear();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          calls.add(call);
          return call.method == 'prepare' ? true : idle;
        });
  });
  tearDown(
    () => TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null),
  );

  test(
    'platform adapter uses fixed methods and exact connect envelope',
    () async {
      final client = MethodChannelVpnClient();
      expect(await client.prepare(), isTrue);
      final result = await client.connect(
        VpnConnectRequest(
          requestId: 'request',
          resourceKey: 'resource',
          accountKey: '1',
          name: 'node',
          config: jsonEncode({'version': 1}),
        ),
      );
      expect(result.generation, 3);
      expect(calls.last.arguments, {
        'requestId': 'request',
        'resourceKey': 'resource',
        'accountKey': '1',
        'name': 'node',
        'config': '{"version":1}',
      });
      await client.pause(reason: 'RESOURCE_EXPIRED');
      expect(calls.last.arguments, {'reason': 'RESOURCE_EXPIRED'});
      await client.status();
      await client.observeExit();
      await client.disconnect();
      expect(calls.map((c) => c.method), [
        'prepare',
        'connect',
        'pause',
        'status',
        'observeExit',
        'disconnect',
      ]);
    },
  );

  test('snapshot routing mode preserves legacy and rejects unknown values', () {
    expect(VpnSnapshot.fromMap(idle).routingMode, 'global');
    expect(
      VpnSnapshot.fromMap({...idle, 'routingMode': 'bypass_cn'}).routingMode,
      'bypass_cn',
    );
    for (final invalid in [null, '', 'direct', 'GLOBAL', 1]) {
      expect(
        () => VpnSnapshot.fromMap({...idle, 'routingMode': invalid}),
        throwsA(isA<VpnFailure>()),
      );
    }
  });

  test('oversize UTF8 payload is rejected before native dispatch', () async {
    final client = MethodChannelVpnClient();
    await expectLater(
      client.connect(
        VpnConnectRequest(
          requestId: 'r',
          resourceKey: 'k',
          accountKey: '1',
          name: 'n',
          config: '密' * 11000,
        ),
      ),
      throwsA(isA<VpnFailure>()),
    );
    expect(calls, isEmpty);
  });

  test(
    'updateResource dispatches only binding and the bounded config JSON',
    () async {
      final client = MethodChannelVpnClient();
      final VpnSnapshot result = await client.updateResource(
        runtimeId: 'runtime',
        requestId: 'request',
        accountKey: '1',
        resourceKey: 'resource',
        config: '{"version":1}',
      );
      expect(result.generation, 3);
      expect(calls.single.method, 'updateResource');
      expect(calls.single.arguments, {
        'runtimeId': 'runtime',
        'requestId': 'request',
        'accountKey': '1',
        'resourceKey': 'resource',
        'config': '{"version":1}',
      });
      await expectLater(
        client.updateResource(
          runtimeId: 'runtime',
          requestId: 'request',
          accountKey: '1',
          resourceKey: 'resource',
          config: '密' * 11000,
        ),
        throwsA(isA<VpnFailure>()),
      );
      expect(calls, hasLength(1));
    },
  );

  test(
    'snapshot retains known status codes while rejecting arbitrary error text',
    () {
      expect(
        VpnSnapshot.fromMap({...idle, 'errorCode': 'RESOURCE_EXPIRED'})
            .errorCode,
        'RESOURCE_EXPIRED',
      );
      expect(
        VpnSnapshot.fromMap({...idle, 'errorCode': 'fixture-password'})
            .errorCode,
        'VPN_ERROR',
      );
    },
  );

  test('event channel delivers complete native snapshots', () async {
    const events = MethodChannel('bluegrass/vpn/events');
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(events, (call) async => null);
    final received = MethodChannelVpnClient().snapshots.first;
    await Future<void>.delayed(Duration.zero);
    await messenger.handlePlatformMessage(
      events.name,
      const StandardMethodCodec().encodeSuccessEnvelope(idle),
      (_) {},
    );
    expect((await received).generation, 3);
    messenger.setMockMethodCallHandler(events, null);
  });

  test('native exception text and invalid snapshots never escape', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          channel,
          (_) async => throw PlatformException(
            code: 'fixture-password',
            message: 'fixture-password',
            details: 'fixture-password',
          ),
        );
    try {
      await MethodChannelVpnClient().connect(
        VpnConnectRequest(
          requestId: 'r',
          resourceKey: 'k',
          accountKey: '1',
          name: 'n',
          config: '{}',
        ),
      );
      fail('must throw');
    } catch (error) {
      expect(error, isA<VpnFailure>());
      expect(error.toString(), isNot(contains('fixture-password')));
    }
    expect(
      () => VpnSnapshot.fromMap({...idle, 'version': 2}),
      throwsA(isA<VpnFailure>()),
    );
    expect(
      () => VpnSnapshot.fromMap({...idle, 'state': 'surprise'}),
      throwsA(isA<VpnFailure>()),
    );
    expect(
      () => VpnSnapshot.fromMap({...idle, 'generation': -1}),
      throwsA(isA<VpnFailure>()),
    );
    expect(
      VpnSnapshot.fromMap({...idle, 'observedExitIp': 'fixture-password'})
          .observedExitIp,
      isNull,
    );
  });
}
