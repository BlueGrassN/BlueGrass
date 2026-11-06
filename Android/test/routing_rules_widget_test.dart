import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_client/app_version.dart';
import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/routing_rules_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  const channel = MethodChannel('bluegrass/routing-rules');
  late AppController controller;
  late FakeVpnClient vpn;
  late Map<String, Object> current;
  final calls = <String>[];
  Future<Object?> Function(MethodCall)? onCall;

  setUp(() {
    calls.clear();
    onCall = null;
    current = {
      'version': '202610232206',
      'source': 'bundled',
      'checkedAt': '',
      'updatedAt': '',
      'updating': false,
      'lastError': '',
    };
    vpn = FakeVpnClient();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
          calls.add(call.method);
          expect(call.arguments, isNull);
          if (onCall != null) return await onCall!(call);
          return jsonEncode(current);
        });
  });

  tearDown(() async {
    controller.dispose();
    await vpn.events.close();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });

  Future<void> launch(WidgetTester tester) async {
    controller = AppController(
      source: FakeSource()..customer = testCustomer,
      store: MemoryStore(),
      vpnClient: vpn,
      routingRulesClient: const MethodChannelRoutingRulesClient(),
      clock: () => testNow,
    );
    await controller.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: controller));
    await tester.pumpAndSettle();
  }

  Future<void> reveal(WidgetTester tester, Finder target) async {
    await tester.scrollUntilVisible(
      target,
      180,
      scrollable: find.byType(Scrollable).hitTestable().first,
    );
    await tester.pump();
    await tester.pump();
  }

  Future<void> settings(WidgetTester tester) async {
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pump();
    await reveal(tester, find.byKey(const Key('routing-rules-update')));
  }

  testWidgets('settings shows daily rules status without starting an update', (
    tester,
  ) async {
    await launch(tester);
    expect(calls, isEmpty);
    await settings(tester);
    expect(find.text('规则自动更新'), findsOneWidget);
    expect(find.text('默认每天自动检查'), findsOneWidget);
    expect(find.text('202610232206'), findsOneWidget);
    expect(find.text('内置规则'), findsOneWidget);
    expect(calls, ['status']);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('settings and licenses display the injected public version', (
    tester,
  ) async {
    await launch(tester);
    await settings(tester);
    await reveal(tester, find.byKey(const Key('app-version')));
    expect(find.text('Android 客户端 · $clientVersion'), findsOneWidget);
    expect(find.textContaining('$clientVersion+$clientBuild'), findsNothing);
    await reveal(tester, find.text('开源许可'));
    await tester.tap(find.text('开源许可'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<LicensePage>(find.byType(LicensePage)).applicationVersion,
      clientVersion,
    );
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets('loading status disables updates and pending status can detach', (
    tester,
  ) async {
    final pending = Completer<Object?>();
    onCall = (_) => pending.future;
    await launch(tester);
    await settings(tester);
    expect(find.text('读取状态中…'), findsOneWidget);
    expect(
      tester
          .widget<OutlinedButton>(find.byKey(const Key('routing-rules-update')))
          .onPressed,
      isNull,
    );
    await tester.pumpWidget(const SizedBox.shrink());
    pending.complete(jsonEncode(current));
    await tester.pump();
    await tester.pump(const Duration(minutes: 3));
    expect(calls, ['status']);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'long update stays single flight and shows the completed version',
    (tester) async {
      final pending = Completer<Object?>();
      onCall = (call) async =>
          call.method == 'update' ? pending.future : jsonEncode(current);
      await launch(tester);
      await settings(tester);
      final button = find.byKey(const Key('routing-rules-update'));
      await tester.tap(button);
      await tester.pump();
      expect(find.text('正在更新…'), findsOneWidget);
      await tester.tap(button);
      // Native may still report idle before its worker starts. Local flight wins.
      await tester.pump(const Duration(minutes: 3));
      expect(tester.widget<OutlinedButton>(button).onPressed, isNull);
      expect(calls.where((c) => c == 'update'), hasLength(1));
      current = {
        ...current,
        'version': '202610240901',
        'source': 'downloaded',
        'checkedAt': '2026-10-24T09:00:00Z',
        'updatedAt': '2026-10-24T09:01:00Z',
      };
      pending.complete(jsonEncode(current));
      await tester.pumpAndSettle();
      expect(find.text('202610240901'), findsOneWidget);
      expect(find.text('已下载规则'), findsOneWidget);
      expect(find.text('2026-10-24 17:00（北京时间）'), findsOneWidget);
      expect(find.text('2026-10-24 17:01（北京时间）'), findsOneWidget);
      expect(tester.widget<OutlinedButton>(button).onPressed, isNotNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  for (final code in [
    'UPDATE_FAILED',
    'CACHE_INVALID',
    'UPDATE_CANCELLED',
    'RULES_CONFIG_FAILED',
    'RULES_UPDATE_FAILED',
    'RULES_UNAVAILABLE',
    'PRIVATE_NATIVE_DETAIL',
  ]) {
    testWidgets('update code $code uses a safe message and keeps usable data', (
      tester,
    ) async {
      onCall = (call) async {
        if (call.method == 'update') current['lastError'] = code;
        return jsonEncode(current);
      };
      await launch(tester);
      await settings(tester);
      await tester.tap(find.byKey(const Key('routing-rules-update')));
      await tester.pumpAndSettle();
      expect(controller.routingRules.error, isNotNull);
      expect(find.text(controller.routingRules.error!), findsOneWidget);
      expect(find.text('202610232206'), findsOneWidget);
      expect(find.textContaining('PRIVATE_NATIVE_DETAIL'), findsNothing);
      expect(find.text(code), findsNothing);
      expect(find.text('立即更新'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets(
    'platform update failure remains visible with last valid status',
    (tester) async {
      final pending = Completer<Object?>();
      await launch(tester);
      await settings(tester);
      onCall = (call) async {
        if (call.method == 'update') {
          return await pending.future;
        }
        return jsonEncode(current);
      };
      await tester.tap(find.byKey(const Key('routing-rules-update')));
      await tester.pump();
      await tester.pump(const Duration(seconds: 2));
      pending.completeError(
        PlatformException(code: 'UNKNOWN', message: 'PRIVATE_NATIVE_DETAIL'),
      );
      await tester.pumpAndSettle();
      expect(find.text('规则更新失败，请稍后重试。'), findsOneWidget);
      expect(find.text('202610232206'), findsOneWidget);
      expect(find.textContaining('PRIVATE_NATIVE_DETAIL'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('polls native progress only on visible resumed settings', (
    tester,
  ) async {
    await launch(tester);
    await settings(tester);
    current['updating'] = true;
    await tester.pump(const Duration(seconds: 30));
    await tester.pump();
    expect(find.text('正在更新…'), findsOneWidget);
    current = {...current, 'version': '202610240901', 'updating': false};
    await tester.pump(const Duration(seconds: 2));
    await tester.pump();
    expect(find.text('202610240901'), findsOneWidget);
    final beforePause = calls.length;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(minutes: 3));
    expect(calls, hasLength(beforePause));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(calls, hasLength(beforePause + 1));
    await tester.tap(find.byKey(const Key('nav-home')));
    await tester.pumpAndSettle();
    final beforeHidden = calls.length;
    await tester.pump(const Duration(minutes: 3));
    expect(calls, hasLength(beforeHidden));
    await settings(tester);
    expect(calls.length, greaterThan(beforeHidden));
    await tester.pumpWidget(const SizedBox.shrink());
    final beforeDetach = calls.length;
    await tester.pump(const Duration(minutes: 3));
    expect(calls, hasLength(beforeDetach));
    expect(calls, everyElement('status'));
  });

  testWidgets(
    'missing platform preserves displayed status and never gates VPN',
    (tester) async {
      await launch(tester);
      await settings(tester);
      onCall = (_) async =>
          throw MissingPluginException('PRIVATE_NATIVE_DETAIL');
      await tester.pump(const Duration(seconds: 30));
      await tester.pumpAndSettle();
      expect(find.text('202610232206'), findsOneWidget);
      expect(find.text('暂时无法读取规则状态，请稍后重试。'), findsOneWidget);
      expect(find.textContaining('PRIVATE_NATIVE_DETAIL'), findsNothing);
      final failedCalls = calls.length;
      await tester.pump(const Duration(minutes: 3));
      expect(calls, hasLength(failedCalls));
      expect(controller.canConnect, isTrue);
      await controller.connect();
      expect(vpn.requests, hasLength(1));
      await controller.disconnect();
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  for (final fails in [false, true]) {
    testWidgets('pending update handles detach (failure: $fails)', (
      tester,
    ) async {
      final pending = Completer<Object?>();
      onCall = (call) async =>
          call.method == 'update' ? pending.future : jsonEncode(current);
      await launch(tester);
      await settings(tester);
      await tester.tap(find.byKey(const Key('routing-rules-update')));
      await tester.pump();
      await tester.pumpWidget(const SizedBox.shrink());
      final beforeDetach = calls.length;
      if (fails) {
        pending.completeError(PlatformException(code: 'UPDATE_FAILED'));
      } else {
        pending.complete(jsonEncode(current));
      }
      await tester.pump();
      await tester.pump(const Duration(minutes: 3));
      expect(calls, hasLength(beforeDetach));
      expect(controller.routingRules.updating, isFalse);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'narrow settings retains controls and active VPN is not reconnected',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 1.8;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      vpn.current = snapshot(state: 'connected');
      await launch(tester);
      await settings(tester);
      expect(find.textContaining('新规则将在下次连接时生效'), findsOneWidget);
      final button = find.byKey(const Key('routing-rules-update'));
      expect(tester.getSize(button).height, greaterThanOrEqualTo(48));
      await tester.tap(button);
      await tester.pumpAndSettle();
      expect(vpn.disconnects, 0);
      expect(vpn.requests, isEmpty);
      await controller.disconnect();
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await reveal(tester, find.byKey(const Key('app-version')));
      expect(
        find.byKey(const Key('app-version')).hitTestable(),
        findsOneWidget,
      );
      await tester.tap(find.byKey(const Key('nav-home')));
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
