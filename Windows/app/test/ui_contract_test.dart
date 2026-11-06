import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
    'manual verified connection prompts once and dismissing does not enable startup',
    (tester) async {
      var saved = const Preferences(socksWarningShown: true);
      final c = AppController(
        source: FixtureDataSource(),
        demo: true,
        getDeviceId: () async => 'fixture',
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
        await tester.tap(find.text('连接'));
        await tester.pump();
        await tester.pump(const Duration(seconds: 1));
        await tester.pumpAndSettle();
        expect(find.text('登录 Windows 时启动 BlueGrass？'), findsOneWidget);
        await tester.tap(find.text('暂不开启'));
        await tester.pumpAndSettle();
        expect(saved.startupPromptShown, isTrue);
        expect(saved.startWithWindows, isFalse);
        expect(saved.autoConnect, isFalse);
        await c.disconnect();
        await tester.pumpAndSettle();
        await tester.tap(find.text('连接'));
        await tester.pump();
        await tester.pump(const Duration(seconds: 1));
        await tester.pumpAndSettle();
        expect(find.text('登录 Windows 时启动 BlueGrass？'), findsNothing);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );
  testWidgets('SMS remains a visibly reserved disabled login option', (
    tester,
  ) async {
    final c = fixtureController();
    try {
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      final sms = find.widgetWithText(OutlinedButton, '短信登录（预留）');
      expect(sms, findsOneWidget);
      expect(tester.widget<OutlinedButton>(sms).onPressed, isNull);
      expect(find.textContaining('本次 Windows Beta 暂不提供短信登录'), findsNothing);
      expect(find.text('短信登录暂不可用'), findsNothing);
      expect(find.textContaining('当前服务：'), findsNothing);
    } finally {
      await tester.pumpWidget(const SizedBox());
      c.dispose();
    }
  });

  testWidgets(
    'settings can persist auto-connect without connecting or enabling autorun',
    (tester) async {
      var saved = const Preferences();
      final c = AppController(
        source: FixtureDataSource(),
        demo: true,
        getDeviceId: () async => 'fixture',
        readPreferences: () async => saved,
        savePreferences: (value) async {
          saved = value;
        },
      );
      try {
        tester.view.physicalSize = const Size(1280, 1200);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        await tester.pumpWidget(MyApp(controller: c));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        await tester.pumpAndSettle();
        await tester.tap(find.text('设置').last);
        await tester.pumpAndSettle();
        final toggle = find.widgetWithText(SwitchListTile, '启动后自动连接');
        expect(tester.widget<SwitchListTile>(toggle).onChanged, isNotNull);
        await tester.ensureVisible(toggle);
        await tester.tap(toggle);
        await tester.pumpAndSettle();
        expect(saved.toJson()['autoConnect'], isTrue);
        expect(saved.toJson()['startWithWindows'] == true, isFalse);
        expect(c.connected, isFalse);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );
}
