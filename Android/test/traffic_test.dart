import 'dart:async';

import 'package:client/traffic.dart';
import 'package:client/screens/traffic_card.dart';
import 'package:client/app_theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fake_vpn.dart';
import 'support/fakes.dart';

Map<String, Object?> trafficFixture({
  int sequence = 1,
  int time = 1000,
  String up = '1024',
  String down = '2048',
  String epoch = 'epoch-a',
  String? resource,
  bool active = true,
  bool available = true,
  bool complete = true,
}) {
  final binding = snapshot(state: 'connected');
  return {
    'version': 1,
    'runtimeId': binding.runtimeId,
    'requestId': binding.requestId,
    'accountKey': binding.accountKey,
    'resourceKey': resource ?? binding.resourceKey,
    'generation': binding.generation,
    'counterEpoch': epoch,
    'sequence': sequence,
    'monotonicMillis': time,
    'uploadBytes': up,
    'downloadBytes': down,
    'active': active,
    'available': available,
    'complete': complete,
  };
}

class FakeTrafficClient implements TrafficClient {
  Map<String, Object?> value = trafficFixture();
  Future<TrafficSample> Function()? onSample;
  int calls = 0;
  @override
  Future<TrafficSample> sample() async {
    calls++;
    return onSample?.call() ?? TrafficSample.fromMap(value);
  }
}

void main() {
  testWidgets(
    'native cumulative bytes yield independent totals and monotonic rates',
    (tester) async {
      final client = FakeTrafficClient();
      final m = TrafficModel(client: client);
      addTearDown(m.dispose);
      m.bind(snapshot(state: 'connected'), '1');
      m.setVisible(true);
      await tester.pump();
      expect(m.totalBytes, BigInt.from(3072));
      expect(m.uploadRate, isNull);
      client.value = trafficFixture(
        sequence: 2,
        time: 3000,
        up: '3072',
        down: '10240',
      );
      await m.refresh();
      expect(m.uploadRate, 1024);
      expect(m.downloadRate, 4096);
      expect(m.uploadBytes, BigInt.from(3072));
      expect(m.downloadBytes, BigInt.from(10240));
      expect(m.totalBytes, BigInt.from(13312));
      m.setVisible(false);
    },
  );

  testWidgets(
    'hidden dashboard stops polling and returns with a fresh rate baseline',
    (tester) async {
      final client = FakeTrafficClient();
      final model = TrafficModel(client: client);
      addTearDown(model.dispose);
      model.bind(snapshot(state: 'connected'), '1');
      model.setVisible(true);
      await tester.pump();
      model.setVisible(false);
      final before = client.calls;
      await tester.pump(const Duration(seconds: 30));
      expect(client.calls, before);
      client.value = trafficFixture(
        sequence: 2,
        time: 31000,
        up: '5120',
        down: '8192',
      );
      model.setVisible(true);
      await tester.pump();
      expect(model.totalBytes, BigInt.from(13312));
      expect(model.uploadRate, isNull);
      client.value = trafficFixture(
        sequence: 3,
        time: 32000,
        up: '6144',
        down: '9216',
      );
      await model.refresh();
      expect(model.uploadRate, 1024);
      model.setVisible(false);
    },
  );

  testWidgets(
    'late replies from a switched resource cannot repaint its totals',
    (tester) async {
      final pending = Completer<TrafficSample>();
      final client = FakeTrafficClient()..onSample = () => pending.future;
      final m = TrafficModel(client: client);
      addTearDown(m.dispose);
      m.bind(snapshot(state: 'connected'), '1');
      m.setVisible(true);
      m.bind(
        snapshot(state: 'connected', resource: node('2').key.stableId),
        '1',
      );
      pending.complete(TrafficSample.fromMap(trafficFixture()));
      await tester.pump();
      expect(m.totalBytes, isNull);
      m.bind(snapshot(), '1');
      expect(m.totalBytes, BigInt.zero);
      expect(m.uploadRate, 0);
    },
  );

  testWidgets(
    'failures keep last totals, reject old samples and never fabricate rates',
    (tester) async {
      final client = FakeTrafficClient();
      final m = TrafficModel(client: client);
      addTearDown(m.dispose);
      m.bind(snapshot(state: 'connected'), '1');
      m.setVisible(true);
      await tester.pump();
      client.onSample = () async =>
          throw PlatformException(code: 'native-secret');
      await m.refresh();
      expect(m.available, isFalse);
      expect(m.totalBytes, BigInt.from(3072));
      expect(m.uploadRate, isNull);
      client.onSample = null;
      client.value = trafficFixture(sequence: 0, time: 900, up: '3', down: '4');
      await m.refresh();
      expect(m.totalBytes, BigInt.from(3072));
      expect(m.available, isFalse);
      client.value = trafficFixture(
        sequence: 2,
        time: 3000,
        up: '2048',
        down: '4096',
      );
      await m.refresh();
      expect(m.uploadRate, isNull);
      expect(m.totalBytes, BigInt.from(6144));
      client.value = trafficFixture(
        sequence: 3,
        time: 12000,
        up: '10240',
        down: '20480',
      );
      await m.refresh();
      expect(m.uploadRate, isNull);
      client.value = trafficFixture(
        sequence: 1,
        time: 13000,
        up: '20',
        down: '40',
        epoch: 'epoch-b',
      );
      await m.refresh();
      expect(m.complete, isFalse);
      expect(m.uploadRate, isNull);
      client.value = trafficFixture(
        sequence: 100,
        time: 14000,
        up: '9999',
        down: '9999',
      );
      await m.refresh();
      expect(m.totalBytes, BigInt.from(60));
      expect(m.available, isFalse);
      m.setVisible(false);
    },
  );

  testWidgets(
    'network pause stops speed; terminal resource expiry clears the session totals',
    (tester) async {
      final client = FakeTrafficClient();
      final m = TrafficModel(client: client);
      addTearDown(m.dispose);
      m.bind(snapshot(state: 'connected'), '1');
      m.setVisible(true);
      await tester.pump();
      client.value = trafficFixture(sequence: 2, time: 2000, active: false);
      m.bind(snapshot(state: 'paused', errorCode: 'NETWORK_UNAVAILABLE'), '1');
      await tester.pump();
      expect(m.uploadRate, 0);
      expect(m.totalBytes, BigInt.from(3072));
      m.bind(snapshot(state: 'paused', errorCode: 'RESOURCE_EXPIRED'), '1');
      expect(m.totalBytes, BigInt.zero);
    },
  );

  test(
    'decimal counters preserve precision and reject malformed channel payloads',
    () {
      final s = TrafficSample.fromMap(
        trafficFixture(up: '9007199254740993', down: '18446744073709551615'),
      );
      expect(
        s.uploadBytes + s.downloadBytes,
        BigInt.parse('18455751272964292608'),
      );
      for (final bad in ['-1', '1.2', '01', 'NaN', '9' * 40]) {
        expect(
          () => TrafficSample.fromMap(trafficFixture(up: bad)),
          throwsFormatException,
        );
      }
      expect(formatTraffic(1536, rate: true), (value: '1.5', unit: 'KiB/s'));
      expect(formatTraffic(null), (value: '—', unit: 'B'));
    },
  );

  testWidgets(
    'statistics card displays two speeds and exactly three cumulative metrics',
    (tester) async {
      final client = FakeTrafficClient();
      final m = TrafficModel(client: client);
      addTearDown(m.dispose);
      m.bind(snapshot(state: 'connected'), '1');
      await tester.pumpWidget(
        MaterialApp(
          theme: blueGrassTheme(Brightness.light),
          home: Scaffold(
            body: SingleChildScrollView(
              child: TrafficCard(model: m, active: true),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      for (final label in ['上传速度', '下载速度', '总流量', '总上传', '总下载']) {
        expect(find.text(label), findsOneWidget);
      }
      expect(
        tester.widget<Text>(find.byKey(const ValueKey('traffic-总流量'))).data,
        '3.0',
      );
      expect(
        tester.widget<Text>(find.byKey(const ValueKey('traffic-总上传'))).data,
        '1.0',
      );
      expect(
        tester.widget<Text>(find.byKey(const ValueKey('traffic-总下载'))).data,
        '2.0',
      );
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('traffic channel sends a read-only call without credentials', (
    tester,
  ) async {
    const channel = MethodChannel('bluegrass/vpn');
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(channel, (
      call,
    ) async {
      expect(call.method, 'traffic');
      expect(call.arguments, isNull);
      return trafficFixture();
    });
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        channel,
        null,
      ),
    );
    final sample = await const MethodChannelTrafficClient().sample();
    expect(sample.downloadBytes, BigInt.from(2048));
  });
}
