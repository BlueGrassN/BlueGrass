import 'dart:ui' show Tristate;

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('home takeover switches expose names to assistive technology', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1280, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final handle = tester.ensureSemantics();
    final c = fixtureController();
    try {
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      await c.login('fixture', 'fixture');
      await tester.pumpAndSettle();
      var root = tester.getSemantics(find.byType(Switch).first);
      while (root.parent != null) {
        root = root.parent!;
      }
      final labels = <String>[];
      void collectSwitches(SemanticsNode node) {
        if (node.flagsCollection.isToggled != Tristate.none) {
          labels.add(node.getSemanticsData().label);
        }
        node.visitChildren((child) {
          collectSwitches(child);
          return true;
        });
      }

      collectSwitches(root);
      expect(labels, hasLength(2));
      expect(labels, contains(contains('TUN 模式')));
      expect(labels, contains(contains('允许局域网')));
    } finally {
      handle.dispose();
      await tester.pumpWidget(const SizedBox());
      c.dispose();
    }
  });

  testWidgets(
    'restored takeover summary uses actual mode instead of saved preference',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 1200);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final c = fixtureController();
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        c.connection = 'connected';
        c.actualMode = 'tun';
        await c.updatePreferences(c.preferences.copyWith(mode: 'system_proxy'));
        await tester.pumpAndSettle();
        expect(find.text('实际接管：TUN'), findsOneWidget);
        expect(
          tester
              .widget<Switch>(find.byKey(const ValueKey('tun-mode-button')))
              .onChanged,
          isNull,
        );
        await tester.tap(find.text('设置').last);
        await tester.pumpAndSettle();
        expect(find.text('实际接管：TUN'), findsOneWidget);
        expect(c.preferences.mode, 'system_proxy');
        c.connection = 'idle';
        c.actualMode = 'none';
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(find.text('下次连接使用系统代理'), findsOneWidget);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );

  testWidgets(
    'home and settings persist independent routing and TUN controls',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 1200);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      var saved = const Preferences();
      final c = AppController(
        source: FixtureDataSource(),
        demo: true,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => saved,
        savePreferences: (value) async {
          saved = value;
        },
      );
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        await tester.pumpAndSettle();
        final routing = find.byType(SegmentedButton<String>);
        final tun = find.byKey(const ValueKey('tun-mode-button'));
        expect(tester.widget<SegmentedButton<String>>(routing).selected, {
          'bypass_cn',
        });
        // The dashboard's TUN tile is a switch; its value is the saved mode.
        expect(tester.widget<Switch>(tun).value, isFalse);
        await tester.tap(find.text('全局代理'));
        await tester.pumpAndSettle();
        expect(saved.toJson()['routingMode'], 'global');
        expect(saved.mode, 'system_proxy');
        await tester.tap(tun);
        await tester.pumpAndSettle();
        expect(saved.mode, 'tun');
        expect(saved.toJson()['routingMode'], 'global');
        expect(tester.widget<Switch>(tun).value, isTrue);
        await tester.tap(find.text('国内外分流'));
        await tester.pumpAndSettle();
        expect(saved.toJson()['routingMode'], 'bypass_cn');
        expect(saved.mode, 'tun');
        expect(c.connection, 'idle');

        await tester.tap(find.text('设置').last);
        await tester.pumpAndSettle();
        final settingsTun = find.widgetWithText(SwitchListTile, 'TUN 模式');
        expect(tester.widget<SwitchListTile>(settingsTun).value, isTrue);
        expect(tester.widget<SegmentedButton<String>>(routing).selected, {
          'bypass_cn',
        });
        await tester.ensureVisible(settingsTun);
        await tester.tap(settingsTun);
        await tester.pumpAndSettle();
        await tester.tap(find.text('全局代理'));
        await tester.pumpAndSettle();
        expect(saved.mode, 'system_proxy');
        expect(saved.toJson()['routingMode'], 'global');
        await tester.tap(find.text('首页').last);
        await tester.pumpAndSettle();
        expect(tester.widget<SegmentedButton<String>>(routing).selected, {
          'global',
        });
        expect(tester.widget<Switch>(tun).value, isFalse);
        expect(c.connection, 'idle');
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );

  for (final state in ['connected', 'connecting', 'busy']) {
    testWidgets('$state disables home and settings mode changes', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(1280, 1200);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final c = fixtureController();
      try {
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        c.connection = state == 'busy' ? 'idle' : state;
        c.busy = state == 'busy';
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        final routing = find.byType(SegmentedButton<String>);
        final tun = find.byKey(const ValueKey('tun-mode-button'));
        expect(
          tester.widget<SegmentedButton<String>>(routing).onSelectionChanged,
          isNull,
        );
        expect(tester.widget<Switch>(tun).onChanged, isNull);
        expect(
          find.textContaining(state == 'busy' ? '稍候' : '断开连接后切换'),
          findsWidgets,
        );
        await tester.tap(find.text('全局代理'));
        await tester.tap(tun);
        await tester.pump();
        expect(c.preferences.toJson()['routingMode'], 'bypass_cn');
        expect(c.preferences.mode, 'system_proxy');

        await tester.tap(find.text('设置').last);
        await tester.pumpAndSettle();
        final settingsTun = find.widgetWithText(SwitchListTile, 'TUN 模式');
        expect(tester.widget<SwitchListTile>(settingsTun).onChanged, isNull);
        expect(
          tester.widget<SegmentedButton<String>>(routing).onSelectionChanged,
          isNull,
        );
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    });
  }
}
