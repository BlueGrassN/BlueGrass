import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  testWidgets(
    'connect UI follows service events and shows failed observation separately',
    (tester) async {
      final vpn = FakeVpnClient();
      final controller = AppController(
        source: FakeSource()..customer = testCustomer,
        store: MemoryStore(),
        clock: () => testNow,
        vpnClient: vpn,
      );
      await controller.initialize();
      await tester.pumpWidget(BlueGrassApp(controller: controller));
      await tester.tap(find.byKey(const Key('connect')));
      await tester.pump();
      expect(find.text('连接中'), findsOneWidget);
      expect(
        tester.widget<FilledButton>(find.byKey(const Key('connect'))).onPressed,
        isNull,
      );
      vpn.emit(
        snapshot(
          generation: 3,
          state: 'connected',
          request: vpn.requests.single.requestId,
          observation: 'available',
          ip: '8.8.4.4',
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('已连接'), findsOneWidget);
      await tester.scrollUntilVisible(
        find.text('8.8.4.4'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('8.8.4.4'), findsOneWidget);
      expect(find.textContaining('fixture-secret'), findsNothing);
      vpn.emit(
        snapshot(
          generation: 4,
          state: 'connected',
          request: vpn.requests.single.requestId,
          observation: 'failed',
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('出口检测失败'), findsOneWidget);
      expect(controller.vpnState, VpnState.connected);
      vpn.emit(
        snapshot(
          generation: 5,
          state: 'paused',
          request: vpn.requests.single.requestId,
        ),
      );
      await tester.pumpAndSettle();
      await tester.drag(find.byType(Scrollable).first, const Offset(0, 900));
      await tester.pumpAndSettle();
      expect(find.text('已暂停'), findsOneWidget);
      await tester.ensureVisible(find.byKey(const Key('disconnect')));
      await tester.tap(find.byKey(const Key('disconnect')));
      await tester.pumpAndSettle();
      await tester.drag(find.byType(Scrollable).first, const Offset(0, 900));
      await tester.pumpAndSettle();
      expect(find.text('未连接'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      controller.dispose();
    },
  );

  testWidgets('lifecycle resume recovers native state without reconnect', (
    tester,
  ) async {
    final vpn = FakeVpnClient();
    final controller = AppController(
      source: FakeSource()..customer = testCustomer,
      store: MemoryStore(),
      clock: () => testNow,
      vpnClient: vpn,
    );
    await controller.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: controller));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    vpn.current = snapshot(generation: 5, state: 'connected');
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(find.text('已连接'), findsOneWidget);
    expect(vpn.requests, isEmpty);
    await tester.pumpWidget(const SizedBox.shrink());
    controller.dispose();
  });

  testWidgets(
    'periodic eligibility refresh pauses revoked resource without user action',
    (tester) async {
      final vpn = FakeVpnClient();
      final source = FakeSource()..customer = testCustomer;
      final controller = AppController(
        source: source,
        store: MemoryStore(),
        clock: () => testNow,
        vpnClient: vpn,
        vpnRefreshInterval: const Duration(seconds: 10),
      );
      await controller.initialize();
      await tester.pumpWidget(BlueGrassApp(controller: controller));
      await tester.tap(find.byKey(const Key('connect')));
      await tester.pump();
      vpn.emit(
        snapshot(
          generation: 3,
          state: 'connected',
          request: vpn.requests.single.requestId,
        ),
      );
      source.onNode = (_) async => node('1', expired: true);
      await tester.pump(const Duration(seconds: 11));
      await tester.pumpAndSettle();
      expect(controller.vpnState, VpnState.paused);
      expect(controller.customer, testCustomer);
      expect(vpn.pauses, contains('RESOURCE_EXPIRED'));
      await tester.pumpWidget(const SizedBox.shrink());
      controller.dispose();
    },
  );
}
