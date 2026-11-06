import 'package:client/app.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/fake_vpn.dart';

void main() {
  late FakeSource source;
  late MemoryStore store;
  late FakeVpnClient vpn;
  late AppController controller;
  var websiteVisits = 0;
  var failWebsite = false;

  setUp(() {
    source = FakeSource();
    store = MemoryStore();
    vpn = FakeVpnClient();
    websiteVisits = 0;
    failWebsite = false;
  });
  tearDown(() async {
    controller.dispose();
    await vpn.events.close();
  });

  Future<void> launch(WidgetTester tester, {bool signedIn = false}) async {
    controller = AppController(
      source: source,
      store: store,
      vpnClient: vpn,
      clock: () => testNow,
      websiteLauncher: () async {
        websiteVisits++;
        if (failWebsite) throw StateError('PRIVATE_PLATFORM_DETAIL');
      },
    );
    if (signedIn) source.customer = testCustomer;
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
    await tester.pumpAndSettle();
  }

  testWidgets(
    'login keeps input through theme changes and opens both official links',
    (tester) async {
      await launch(tester);
      expect(find.text('欢迎回来'), findsOneWidget);
      await tester.enterText(find.byKey(const Key('username')), 'kept-account');
      await controller.setTheme(ThemeMode.dark);
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextFormField>(find.byKey(const Key('username')))
            .controller!
            .text,
        'kept-account',
      );
      for (final text in ['注册账号', '找回密码']) {
        await reveal(tester, find.text(text));
        await tester.tap(find.text(text));
        await tester.pumpAndSettle();
      }
      expect(websiteVisits, 2);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'all resource scopes preserve selection and show read-only details',
    (tester) async {
      await launch(tester, signedIn: true);
      await tester.tap(find.byKey(const Key('nav-resources')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('全部资源'));
      await tester.pumpAndSettle();
      expect(controller.view, ResourceView.subscriptions);
      await tester.tap(find.text('资产'));
      await tester.pumpAndSettle();
      expect(controller.view, ResourceView.assets);
      expect(find.text('静态资产'), findsOneWidget);
      expect(find.byKey(const ValueKey('select-1')), findsNothing);
      expect(controller.selected?.key.sourceId, '1');
      await tester.tap(find.text('连接资源'));
      await tester.pumpAndSettle();
      await reveal(tester, find.byKey(const ValueKey('details-1')));
      await tester.tap(find.byKey(const ValueKey('details-1')));
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsOneWidget);
      expect(find.textContaining('fixture-secret'), findsNothing);
      await tester.tap(find.text('前往官网'));
      expect(websiteVisits, 1);
      await tester.tap(find.text('关闭'));
      await tester.pumpAndSettle();
      expect(controller.selected?.key.sourceId, '1');
    },
  );

  testWidgets('diagnostics observes real session and exports only safe state', (
    tester,
  ) async {
    await launch(tester, signedIn: true);
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: 3,
        state: 'connected',
        request: vpn.requests.single.requestId,
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('nav-diagnostics')));
    await tester.pumpAndSettle();
    expect(find.text('连接检查'), findsOneWidget);
    vpn.onObserve = () async => vpn.current = snapshot(
      generation: 4,
      state: 'connected',
      request: vpn.requests.single.requestId,
      observation: 'available',
      ip: '8.8.4.4',
    );
    await reveal(tester, find.byKey(const Key('diagnostic-observe-exit')));
    await tester.tap(find.byKey(const Key('diagnostic-observe-exit')));
    await tester.pumpAndSettle();
    expect(controller.observedExitIp, '8.8.4.4');
    expect(find.text('8.8.4.4'), findsOneWidget);
    String? copied;
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied = (call.arguments as Map)['text'] as String;
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    await tester.drag(
      find.byType(Scrollable).hitTestable().first,
      const Offset(0, 1200),
    );
    await tester.pumpAndSettle();
    await reveal(tester, find.byKey(const Key('copy-diagnostics')));
    await tester.tap(find.byKey(const Key('copy-diagnostics')));
    await tester.pumpAndSettle();
    expect(copied, contains('connected'));
    expect(copied, isNot(contains('fixture-secret')));
    expect(copied, isNot(contains('accountKey')));
    expect(copied, isNot(contains('resourceKey')));
    expect(copied, isNot(contains('example.invalid')));
    expect(controller.vpnState, VpnState.connected);
    await controller.disconnect();
    await tester.pumpAndSettle();
    expect(find.text('8.8.4.4'), findsNothing);
  });

  testWidgets(
    'official website failures are visible and sanitized on login and settings',
    (tester) async {
      await launch(tester);
      failWebsite = true;
      await reveal(tester, find.text('注册账号'));
      await tester.tap(find.text('注册账号'));
      await tester.pumpAndSettle();
      expect(find.text(controller.websiteError!), findsOneWidget);
      expect(find.textContaining('PRIVATE_PLATFORM_DETAIL'), findsNothing);
      await controller.login('demo', 'fixture-password');
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('nav-settings')));
      await tester.pumpAndSettle();
      await reveal(tester, find.text('官方网站'));
      await tester.tap(find.text('官方网站'));
      await tester.pumpAndSettle();
      expect(find.text(controller.websiteError!), findsOneWidget);
      expect(find.textContaining('PRIVATE_PLATFORM_DETAIL'), findsNothing);
      expect(websiteVisits, 2);
    },
  );

  testWidgets(
    'small screen with keyboard and large text can reach every page and action',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 1.8;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.view.resetViewInsets);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      await launch(tester);
      tester.view.viewInsets = const FakeViewPadding(bottom: 240);
      await tester.pumpAndSettle();
      await tester.enterText(find.byKey(const Key('username')), 'demo');
      await reveal(tester, find.byKey(const Key('password')));
      await tester.enterText(
        find.byKey(const Key('password')),
        'fixture-password',
      );
      await reveal(tester, find.byKey(const Key('login')));
      expect(
        tester.getSize(find.byKey(const Key('login'))).height,
        greaterThanOrEqualTo(48),
      );
      expect(tester.takeException(), isNull);
      tester.view.resetViewInsets();
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('login')));
      await tester.pumpAndSettle();
      for (final page in ['resources', 'diagnostics', 'settings', 'home']) {
        await tester.tap(find.byKey(Key('nav-$page')));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: page);
      }
      await reveal(tester, find.byKey(const Key('connect')));
      expect(
        tester.getSize(find.byKey(const Key('connect'))).height,
        greaterThanOrEqualTo(48),
      );
      await tester.tap(find.byKey(const Key('nav-settings')));
      await tester.pumpAndSettle();
      await reveal(tester, find.byKey(const Key('theme-dark')));
      await tester.tap(find.byKey(const Key('theme-dark')));
      await tester.pumpAndSettle();
      expect(store.theme, ThemeMode.dark);
      expect(controller.selected?.key.sourceId, '1');
      await reveal(tester, find.byKey(const Key('logout')));
      await tester.tap(find.byKey(const Key('logout')));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('username')), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'recovered unknown resource never borrows the preselected identity',
    (tester) async {
      vpn.current = snapshot(
        state: 'connected',
        resource: node('99').key.stableId,
        observation: 'available',
        ip: '8.8.4.4',
      );
      source.onNode = (_) async => throw const ApiFailure('network_error');
      await launch(tester, signedIn: true);
      expect(controller.selected?.key.sourceId, '1');
      expect(controller.activeResource, isNull);
      expect(controller.vpnState, VpnState.connected);
      expect(find.text('香港线路 1'), findsNothing);
      expect(find.text('连接资源待同步'), findsOneWidget);
      expect(find.text('8.8.4.4'), findsOneWidget);
      await controller.disconnect();
      await tester.pumpAndSettle();
    },
  );

  testWidgets('pending selection never borrows the active resource exit IP', (
    tester,
  ) async {
    await launch(tester, signedIn: true);
    await controller.connect();
    vpn.emit(
      snapshot(
        generation: 3,
        state: 'connected',
        request: vpn.requests.single.requestId,
        observation: 'available',
        ip: '8.8.4.4',
      ),
    );
    controller.select(controller.resources.last);
    await tester.pumpAndSettle();
    expect(find.text('香港线路 1'), findsOneWidget);
    expect(find.text('待切换资源：香港线路 2'), findsOneWidget);
    expect(find.text('8.8.4.4'), findsOneWidget);
    await tester.tap(find.byKey(const Key('nav-resources')));
    await tester.pumpAndSettle();
    await reveal(tester, find.byKey(const ValueKey('details-2')));
    await tester.tap(find.byKey(const ValueKey('details-2')));
    await tester.pumpAndSettle();
    expect(find.text('8.8.4.4'), findsNothing);
    expect(find.text('暂未获取'), findsOneWidget);
    await tester.tap(find.text('关闭'));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('nav-home')));
    await controller.disconnect();
    await tester.pumpAndSettle();
    expect(find.text('香港线路 2'), findsOneWidget);
    expect(find.text('8.8.4.4'), findsNothing);
  });

  testWidgets(
    'manual diagnostics reports a revoked connection as paused with protection retained',
    (tester) async {
      await launch(tester, signedIn: true);
      await controller.connect();
      vpn.emit(
        snapshot(
          generation: 3,
          state: 'connected',
          request: vpn.requests.single.requestId,
        ),
      );
      await tester.pumpAndSettle();
      source.onNode = (_) async => node('1', expired: true);
      await tester.tap(find.byKey(const Key('nav-diagnostics')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('run-diagnostics')));
      await tester.pumpAndSettle();
      await reveal(tester, find.text('已暂停'));
      expect(find.text('已暂停'), findsOneWidget);
      expect(find.text('已保留'), findsOneWidget);
      expect(controller.vpnSnapshot?.tunActive, isTrue);
      expect(find.text('通过'), findsNothing);
      await reveal(tester, find.byKey(const Key('diagnostic-observe-exit')));
      expect(
        tester
            .widget<OutlinedButton>(
              find.byKey(const Key('diagnostic-observe-exit')),
            )
            .onPressed,
        isNull,
      );
      await controller.disconnect();
      await tester.pumpAndSettle();
    },
  );
}
