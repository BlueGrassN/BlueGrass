import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/ui/kit.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart' hide Page;
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const panelKey = ValueKey('home-resource-picker');
final panel = find.byKey(panelKey);
Finder option(String name) =>
    find.descendant(of: panel, matching: find.widgetWithText(ListTile, name));

Future<void> openHome(WidgetTester tester, AppController c) async {
  tester.view.physicalSize = const Size(1280, 720);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(MyApp(controller: c));
  await tester.pumpAndSettle();
  await c.login('fixture', 'fixture');
  await tester.pumpAndSettle();
}

Future<void> openPicker(WidgetTester tester) async {
  await tester.tap(find.widgetWithText(FilledButton, '选择资源'));
  await tester.pumpAndSettle();
  expect(panel, findsOneWidget);
}

Future<void> cleanUp(WidgetTester tester, AppController c) async {
  await tester.pumpWidget(const SizedBox());
  c.dispose();
}

void main() {
  testWidgets(
    'account change removes only the picker when another dialog is above it',
    (tester) async {
      final c = fixtureController();
      try {
        await openHome(tester, c);
        await openPicker(tester);
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        unawaited(
          showDialog<void>(
            context: tester.element(panel),
            builder: (context) => AlertDialog(
              title: const Text('通知弹窗'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('关闭通知'),
                ),
              ],
            ),
          ),
        );
        await tester.pumpAndSettle();
        c.customer = null;
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(find.text('通知弹窗'), findsOneWidget);
        await tester.tap(find.text('关闭通知'));
        await tester.pumpAndSettle();
        expect(panel, findsNothing);
        expect(find.text('欢迎回来'), findsOneWidget);
        expect(
          tester.state<NavigatorState>(find.byType(Navigator).first).canPop(),
          isFalse,
        );
      } finally {
        await cleanUp(tester, c);
      }
    },
  );

  testWidgets(
    'account logout dismisses the picker without applying a stale choice',
    (tester) async {
      final c = fixtureController();
      try {
        await openHome(tester, c);
        await openPicker(tester);
        // Session invalidation can be the first change after opening.
        c.customer = null;
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(panel, findsNothing);
        expect(find.text('欢迎回来'), findsOneWidget);
        expect(c.customer, isNull);
      } finally {
        await cleanUp(tester, c);
      }
    },
  );

  testWidgets('empty picker refreshes in place in a short window', (
    tester,
  ) async {
    final c = fixtureController();
    try {
      await openHome(tester, c);
      tester.view.physicalSize = const Size(480, 420);
      c.nodes = [];
      c.selected = null;
      await c.updatePreferences(c.preferences);
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.widgetWithText(FilledButton, '选择资源'));
      await openPicker(tester);
      expect(find.text('暂无连接资源，请刷新后重试'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.tap(find.byTooltip('刷新资源列表'));
      await tester.pumpAndSettle();
      expect(panel, findsOneWidget);
      expect(option('香港 · 优选 01'), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally {
      await cleanUp(tester, c);
    }
  });

  testWidgets(
    'home picker searches and selects without navigating or connecting',
    (tester) async {
      final c = fixtureController();
      try {
        await openHome(tester, c);
        await openPicker(tester);
        expect(tester.widget<PageHeader>(find.byType(PageHeader)).title, '首页');
        expect(
          find.byKey(const ValueKey('resource-scope-control')),
          findsNothing,
        );
        expect(tester.widget<ListTile>(option('香港 · 优选 01')).selected, isTrue);
        await tester.enterText(
          find.byKey(const ValueKey('home-resource-search')),
          '东京',
        );
        await tester.pumpAndSettle();
        expect(option('香港 · 优选 01'), findsNothing);
        await tester.tap(option('东京 · 低延迟'));
        await tester.pumpAndSettle();
        expect(panel, findsNothing);
        expect(c.selected?.name, '东京 · 低延迟');
        expect(c.active, isFalse);
        expect(tester.widget<PageHeader>(find.byType(PageHeader)).title, '首页');
        expect(find.widgetWithText(FilledButton, '连接'), findsOneWidget);
      } finally {
        await cleanUp(tester, c);
      }
    },
  );

  testWidgets('escape and outside dismiss preserve the selected resource', (
    tester,
  ) async {
    final c = fixtureController();
    try {
      await openHome(tester, c);
      final original = c.selected?.key;
      await openPicker(tester);
      await tester.sendKeyEvent(LogicalKeyboardKey.escape);
      await tester.pumpAndSettle();
      expect(panel, findsNothing);
      expect(c.selected?.key, original);
      await openPicker(tester);
      await tester.tapAt(const Offset(1160, 650));
      await tester.pumpAndSettle();
      expect(panel, findsNothing);
      expect(c.selected?.key, original);
    } finally {
      await cleanUp(tester, c);
    }
  });

  testWidgets(
    'active resource change still requires confirmation and never reconnects automatically',
    (tester) async {
      final c = fixtureController();
      try {
        await openHome(tester, c);
        c.connection = 'connected';
        c.actualMode = 'system_proxy';
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        await openPicker(tester);
        await tester.tap(option('香港 · 优选 01'));
        await tester.pumpAndSettle();
        expect(find.text('切换资源'), findsNothing);
        expect(c.active, isTrue);
        await openPicker(tester);
        await tester.tap(option('东京 · 低延迟'));
        await tester.pumpAndSettle();
        expect(find.text('切换资源'), findsOneWidget);
        await tester.tap(find.text('取消'));
        await tester.pumpAndSettle();
        expect(c.selected?.name, '香港 · 优选 01');
        expect(c.active, isTrue);
        await openPicker(tester);
        await tester.tap(option('东京 · 低延迟'));
        await tester.pumpAndSettle();
        await tester.tap(find.widgetWithText(FilledButton, '继续'));
        await tester.pumpAndSettle();
        expect(c.selected?.name, '东京 · 低延迟');
        expect(c.active, isFalse);
        expect(tester.widget<PageHeader>(find.byType(PageHeader)).title, '首页');
      } finally {
        await cleanUp(tester, c);
      }
    },
  );

  testWidgets(
    'picker prevents expired selection and hides resources outside retention',
    (tester) async {
      final c = fixtureController();
      try {
        await openHome(tester, c);
        final original = c.selected!;
        NodeRecord expired(String id, int hours) => NodeRecord(
          key: ResourceKey(
            environment: 'fixture',
            dataSource: 'fixture-v1',
            accountKey: 'fixture-account',
            sourceId: id,
          ),
          name: id,
          protocol: 'socks5',
          host: 'relay.invalid',
          port: 1080,
          expiresAtUtc: c.effectiveNowUtc.subtract(Duration(hours: hours)),
        );
        c.nodes = [
          original,
          expired('recently-expired', 2),
          expired('hidden-expired', 80),
        ];
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        await openPicker(tester);
        expect(option('hidden-expired'), findsNothing);
        expect(
          tester.widget<ListTile>(option('recently-expired')).enabled,
          isFalse,
        );
        await tester.tap(option('recently-expired'));
        await tester.pumpAndSettle();
        expect(c.selected?.key, original.key);
        expect(panel, findsOneWidget);
      } finally {
        await cleanUp(tester, c);
      }
    },
  );

  testWidgets('search can load additional pages without leaving the picker', (
    tester,
  ) async {
    final c = AppController(
      source: PickerPages(),
      demo: true,
      getDeviceId: () async => 'fixture',
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
    );
    try {
      await openHome(tester, c);
      await openPicker(tester);
      await tester.enterText(
        find.byKey(const ValueKey('home-resource-search')),
        '东京',
      );
      await tester.pumpAndSettle();
      expect(option('东京 · 低延迟'), findsNothing);
      await tester.tap(find.text('加载更多资源'));
      await tester.pumpAndSettle();
      expect(option('东京 · 低延迟'), findsOneWidget);
      await tester.tap(option('东京 · 低延迟'));
      await tester.pumpAndSettle();
      expect(c.selected?.name, '东京 · 低延迟');
      expect(c.active, isFalse);
    } finally {
      await cleanUp(tester, c);
    }
  });
}

class PickerPages extends FixtureDataSource {
  @override
  Future<Page<NodeRecord>> listNodes({
    String? cursor,
    int perPage = 50,
  }) async => cursor == null
      ? Page(items: [nodes.first], hasMore: true, nextCursor: 'next')
      : Page(items: nodes.skip(1).toList(), hasMore: false);
}
