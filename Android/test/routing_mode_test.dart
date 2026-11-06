import 'dart:async';
import 'dart:convert';

import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/android_store.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late MemoryStore store;
  late FakeVpnClient vpn;
  late AppController controller;
  var disposed = false;
  setUp(() {
    disposed = false;
    store = MemoryStore()..routingMode = 'bypass_cn';
    vpn = FakeVpnClient();
    controller = AppController(
      source: FakeSource()..customer = testCustomer,
      store: store,
      vpnClient: vpn,
      clock: () => testNow,
    );
  });
  tearDown(() async {
    if (!disposed) controller.dispose();
    await vpn.events.close();
  });

  test('saved mode reaches connect and authoritative refresh', () async {
    await controller.initialize();
    expect(controller.routingMode, 'bypass_cn');
    await controller.connect();
    expect(jsonDecode(vpn.requests.single.config)['routingMode'], 'bypass_cn');
    vpn.emit(
      snapshot(
        generation: 3,
        state: 'connected',
        request: vpn.requests.single.requestId,
        routingMode: 'bypass_cn',
      ),
    );
    await controller.refreshVpnEligibility();
    expect(jsonDecode(vpn.updates.last['config']!)['routingMode'], 'bypass_cn');
  });

  test(
    'recovery uses active mode instead of a different saved preference',
    () async {
      vpn.current = snapshot(state: 'connected', routingMode: 'global');
      await controller.initialize();
      expect(controller.routingMode, 'global');
      expect(controller.canChangeRoutingMode, isFalse);
      expect(jsonDecode(vpn.updates.last['config']!)['routingMode'], 'global');
      await controller.setRoutingMode('bypass_cn');
      expect(controller.routingMode, 'global');
      await controller.disconnect();
      expect(controller.routingMode, 'bypass_cn');
    },
  );

  for (final state in ['connecting', 'connected', 'paused']) {
    test('mode cannot change while $state', () async {
      await controller.initialize();
      await controller.connect();
      vpn.emit(
        snapshot(
          generation: 3,
          state: state,
          request: vpn.requests.single.requestId,
          routingMode: 'bypass_cn',
        ),
      );
      await controller.setRoutingMode('global');
      expect(controller.routingMode, 'bypass_cn');
      expect(store.routingMode, 'bypass_cn');
      expect(controller.canChangeRoutingMode, isFalse);
    });
  }

  for (final state in ['connected', 'paused']) {
    test(
      'reconnecting recovered $state session preserves its actual mode',
      () async {
        vpn.current = snapshot(state: state, routingMode: 'global');
        await controller.initialize();
        expect(controller.routingMode, 'global');
        await controller.connect();
        expect(jsonDecode(vpn.requests.single.config)['routingMode'], 'global');
      },
    );
  }

  test('login retries failed routing initialization before creating device identity', () async {
    store.failRoutingRead = true;
    await controller.initialize();
    expect(controller.requiresSessionClear, isTrue);
    await controller.logout();
    store.failRoutingRead = false;
    store.onDeviceId = () async {
      expect(store.routingReads, 2);
      return '00000000-0000-4000-8000-000000000001';
    };
    await controller.login('demo', 'fixture');
    expect(controller.customer, isNotNull);
    expect(controller.routingMode, 'bypass_cn');
    expect(store.routingReads, 2);
  });

  test(
    'failed save leaves previous mode and connection configuration intact',
    () async {
      await controller.initialize();
      store.failRouting = true;
      await controller.setRoutingMode('global');
      expect(controller.routingMode, 'bypass_cn');
      expect(controller.settingsError, isNotNull);
      await controller.connect();
      expect(
        jsonDecode(vpn.requests.single.config)['routingMode'],
        'bypass_cn',
      );
    },
  );

  test('pending save prevents connection and competing edits', () async {
    await controller.initialize();
    final pending = Completer<void>();
    store.onRoutingWrite = (_) => pending.future;
    final saving = controller.setRoutingMode('global');
    expect(controller.canConnect, isFalse);
    expect(controller.canChangeRoutingMode, isFalse);
    await controller.connect();
    await controller.setRoutingMode('bypass_cn');
    expect(vpn.requests, isEmpty);
    expect(controller.routingMode, 'bypass_cn');
    pending.complete();
    await saving;
    expect(controller.routingMode, 'global');
    expect(store.routingMode, 'global');
    expect(controller.canConnect, isTrue);
  });

  test(
    'invalid mode is rejected without overwriting saved preference',
    () async {
      await controller.initialize();
      await controller.setRoutingMode('direct');
      expect(controller.routingMode, 'bypass_cn');
      expect(store.routingMode, 'bypass_cn');
      expect(controller.settingsError, isNotNull);
    },
  );

  test(
    'mode storage validates native values and sends exact channel command',
    () async {
      const channel = MethodChannel('bluegrass/android');
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
      String value = 'global';
      messenger.setMockMethodCallHandler(channel, (call) async {
        if (call.method == 'routingModeRead') return value;
        if (call.method == 'routingModeWrite') {
          value = (call.arguments as Map)['value'];
          return null;
        }
        throw MissingPluginException();
      });
      final nativeStore = AndroidStore();
      expect(await nativeStore.readRoutingMode(), 'global');
      await nativeStore.writeRoutingMode('bypass_cn');
      expect(await nativeStore.readRoutingMode(), 'bypass_cn');
      value = 'invalid';
      await expectLater(nativeStore.readRoutingMode(), throwsException);
      await expectLater(
        nativeStore.writeRoutingMode('direct'),
        throwsException,
      );
    },
  );

  testWidgets('routing selector saves and becomes disabled during connection', (
    tester,
  ) async {
    controller.dispose();
    controller = AppController(
      source: FakeSource()..customer = testCustomer,
      store: store,
      vpnClient: vpn,
      clock: () => testNow,
    );
    await controller.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: controller));
    final selector = find.byKey(const Key('routing-mode'));
    await tester.scrollUntilVisible(
      selector,
      250,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('国内外分流'), findsOneWidget);
    expect(find.text('全局代理'), findsOneWidget);
    await tester.tap(find.text('全局代理'));
    await tester.pumpAndSettle();
    expect(controller.routingMode, 'global');
    await controller.connect();
    await tester.pump();
    expect(
      tester.widget<SegmentedButton<String>>(selector).onSelectionChanged,
      isNull,
    );
    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
    disposed = true;
  });
}
