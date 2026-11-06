import 'package:bluegrass_domain/bluegrass_domain.dart' as domain;
import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

void main() {
  late FakeSource source;
  late MemoryStore store;
  late AppController controller;
  setUp(() {
    source = FakeSource();
    store = MemoryStore();
    controller = AppController(
      source: source,
      store: store,
      clock: () => testNow,
    );
  });
  tearDown(() => controller.dispose());
  Future<void> launch(WidgetTester tester) async {
    await controller.initialize();
    await tester.pumpWidget(BlueGrassApp(controller: controller));
    await tester.pumpAndSettle();
  }

  Future<void> signIn(WidgetTester tester) async {
    await tester.enterText(find.byKey(const Key('username')), 'demo');
    await tester.enterText(
      find.byKey(const Key('password')),
      'fixture-password',
    );
    await tester.tap(find.byKey(const Key('login')));
    await tester.pumpAndSettle();
  }

  testWidgets(
    'validates login then shows account, selection and honest VPN status',
    (tester) async {
      await launch(tester);
      await tester.tap(find.byKey(const Key('login')));
      await tester.pumpAndSettle();
      expect(find.text('请输入账号'), findsOneWidget);
      await signIn(tester);
      expect(find.text('香港线路 1'), findsOneWidget);
      expect(find.text('未连接'), findsOneWidget);
      final connect = tester.widget<FilledButton>(
        find.byKey(const Key('connect')),
      );
      expect(connect.onPressed, isNull);
      expect(find.text('VPN 暂不可用'), findsOneWidget);
      expect(find.textContaining('fixture-secret'), findsNothing);
      await tester.tap(find.byKey(const Key('nav-resources')));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.byKey(const ValueKey('select-2')),
        180,
        scrollable: find.byType(Scrollable).hitTestable().first,
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('select-2')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('nav-home')));
      await tester.pumpAndSettle();
      expect(find.text('香港线路 2'), findsOneWidget);
      await tester.tap(find.byKey(const Key('nav-settings')));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.byKey(const Key('logout')),
        180,
        scrollable: find.byType(Scrollable).hitTestable().first,
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('logout')));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('username')), findsOneWidget);
      expect(find.text('香港线路 2'), findsNothing);
    },
  );
  testWidgets('theme switches without losing login text or account selection', (
    tester,
  ) async {
    await launch(tester);
    await tester.enterText(find.byKey(const Key('username')), 'kept');
    await controller.setTheme(ThemeMode.dark);
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextFormField>(find.byKey(const Key('username')))
          .controller!
          .text,
      'kept',
    );
    await signIn(tester);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('theme-light')));
    await tester.pumpAndSettle();
    expect(controller.themeMode, ThemeMode.light);
    expect(controller.selected?.key.sourceId, '1');
  });
  testWidgets('small screen and large text stay scrollable with keyboard', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 1.8;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await launch(tester);
    await tester.enterText(find.byKey(const Key('username')), 'demo');
    tester.view.viewInsets = const FakeViewPadding(bottom: 240);
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const Key('password')));
    await tester.enterText(
      find.byKey(const Key('password')),
      'fixture-password',
    );
    await tester.ensureVisible(find.byKey(const Key('login')));
    expect(tester.takeException(), isNull);
    tester.view.resetViewInsets();
    await tester.pumpAndSettle();
    await controller.login('demo', 'fixture-password');
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.tap(find.byKey(const Key('nav-resources')));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });
  testWidgets('empty page offers next page instead of claiming no resources', (
    tester,
  ) async {
    source.onNodes = (cursor) async => cursor == null
        ? domain.Page(items: [], hasMore: true, nextCursor: 'next')
        : domain.Page(items: [node('3')], hasMore: false);
    await launch(tester);
    await signIn(tester);
    await tester.tap(find.byKey(const Key('nav-resources')));
    await tester.pumpAndSettle();
    expect(find.text('加载更多'), findsOneWidget);
    await tester.ensureVisible(find.text('加载更多'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('加载更多'));
    await tester.pumpAndSettle();
    expect(find.text('香港线路 3'), findsOneWidget);
  });
}
