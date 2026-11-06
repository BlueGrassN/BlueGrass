import 'dart:async';

import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fake_vpn.dart';
import 'support/fakes.dart';

void main() {
  testWidgets(
    'large text and tall system insets keep final page actions above dock',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.view.padding = const FakeViewPadding(bottom: 48);
      tester.view.viewPadding = const FakeViewPadding(bottom: 48);
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.view.resetPadding);
      addTearDown(tester.view.resetViewPadding);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final vpn = FakeVpnClient()..current = snapshot(state: 'connected');
      final c = AppController(
        source: FakeSource()..customer = testCustomer,
        store: MemoryStore(),
        vpnClient: vpn,
        clock: () => testNow,
      );
      await c.initialize();
      await tester.pumpWidget(BlueGrassApp(controller: c));
      await tester.pumpAndSettle();
      for (final (tab, target) in [
        ('diagnostics', find.byKey(const Key('diagnostic-observe-exit'))),
        ('settings', find.text('官方网站')),
      ]) {
        await tester.tap(find.byKey(Key('nav-$tab')));
        await tester.pumpAndSettle();
        final scroll = find.byType(Scrollable).hitTestable().first;
        // Lazy children refine the maximum extent after the first layout.
        final position = tester.state<ScrollableState>(scroll).position;
        for (var i = 0; i < 10; i++) {
          position.jumpTo(position.maxScrollExtent);
          await tester.pumpAndSettle();
          if (position.extentAfter < .1) break;
        }
        expect(position.extentAfter, lessThan(.1));
        expect(
          tester.getBottomRight(target).dy,
          lessThan(tester.getTopLeft(find.byKey(const Key('nav-home'))).dy),
          reason: tab,
        );
        expect(target.hitTestable(), findsOneWidget, reason: tab);
        expect(tester.takeException(), isNull);
      }
      await tester.pumpWidget(const SizedBox.shrink());
      c.dispose();
      await vpn.events.close();
    },
  );

  testWidgets('preparing a resource can be cancelled before native connect', (
    tester,
  ) async {
    final pending = Completer<void>();
    final source = FakeSource()..customer = testCustomer;
    final vpn = FakeVpnClient();
    final c = AppController(
      source: source,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
    );
    await c.initialize();
    source.onNode = (_) async {
      await pending.future;
      return node('1');
    };
    await tester.pumpWidget(BlueGrassApp(controller: c));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    expect(c.vpnBusy, isTrue);
    expect(c.vpnState, VpnState.idle);
    expect(find.byKey(const Key('disconnect')).hitTestable(), findsOneWidget);
    await tester.tap(find.byKey(const Key('disconnect')));
    pending.complete();
    await tester.pumpAndSettle();
    expect(c.vpnState, VpnState.idle);
    expect(vpn.requests, isEmpty);
    await tester.pumpWidget(const SizedBox.shrink());
    c.dispose();
    await vpn.events.close();
  });

  testWidgets('copy feedback leaves dock labels tappable', (tester) async {
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (_) async => null,
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    final vpn = FakeVpnClient()..current = snapshot(state: 'connected');
    final c = AppController(
      source: FakeSource()..customer = testCustomer,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
    );
    await c.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: c));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('nav-diagnostics')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('copy-diagnostics')));
    await tester.pumpAndSettle();
    expect(find.text('已复制脱敏诊断信息'), findsOneWidget);
    await tester.tap(find.text('仪表盘'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('disconnect')).hitTestable(), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    c.dispose();
    await vpn.events.close();
  });

  testWidgets('phone dashboard exposes connection and exit without scrolling', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final vpn = FakeVpnClient();
    final c = AppController(
      source: FakeSource()..customer = testCustomer,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
    );
    await c.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: c));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('connect')).hitTestable(), findsOneWidget);
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pumpAndSettle();
    expect(c.vpnState, VpnState.connecting);
    vpn.emit(
      snapshot(
        generation: 3,
        state: 'connected',
        request: vpn.requests.single.requestId,
        observation: 'available',
        ip: '203.0.113.42',
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('203.0.113.42').hitTestable(), findsOneWidget);
    expect(find.byKey(const Key('disconnect')).hitTestable(), findsOneWidget);
    await tester.tap(find.byKey(const Key('disconnect')));
    await tester.pumpAndSettle();
    expect(c.vpnState, VpnState.idle);
    expect(find.text('203.0.113.42'), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
    c.dispose();
    await vpn.events.close();
  });

  testWidgets(
    'back from another tab returns to dashboard without stopping VPN',
    (tester) async {
      final vpn = FakeVpnClient()..current = snapshot(state: 'connected');
      final c = AppController(
        source: FakeSource()..customer = testCustomer,
        store: MemoryStore(),
        vpnClient: vpn,
        clock: () => testNow,
      );
      await c.initialize();
      await tester.pumpWidget(BlueGrassApp(controller: c));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('nav-resources')));
      await tester.pumpAndSettle();
      await tester.binding.handlePopRoute();
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('disconnect')).hitTestable(), findsOneWidget);
      expect(c.vpnState, VpnState.connected);
      await tester.pumpWidget(const SizedBox.shrink());
      c.dispose();
      await vpn.events.close();
    },
  );
}
