import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
    'production LAN preference is editable while idle with mode-specific limits',
    (tester) async {
      var saved = const Preferences();
      final c = AppController(
        source: FixtureDataSource(),
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
        await tester.tap(find.text('设置').last);
        await tester.pumpAndSettle();
        final lan = find.widgetWithText(SwitchListTile, '允许局域网访问');
        expect(tester.widget<SwitchListTile>(lan).onChanged, isNotNull);
        expect(find.textContaining('仅影响遵循系统代理的应用'), findsOneWidget);
        await tester.ensureVisible(lan);
        await tester.tap(lan);
        await tester.pumpAndSettle();
        expect(saved.allowLan, isFalse);
        await c.updatePreferences(saved.copyWith(mode: 'tun'));
        await tester.pumpAndSettle();
        expect(find.textContaining('普通局域网直连例外'), findsOneWidget);
        c.connection = 'connected';
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(tester.widget<SwitchListTile>(lan).onChanged, isNull);
        await tester.ensureVisible(find.text('入口 DNS'));
        await tester.tap(find.text('入口 DNS'));
        await tester.pumpAndSettle();
        expect(find.textContaining('下次解析生效，不中断当前连接'), findsOneWidget);
        await tester.tap(find.text('自定义'));
        await tester.pumpAndSettle();
        await tester.enterText(
          find.byKey(const ValueKey('dns-address-0')),
          '9.9.9.9',
        );
        await tester.tap(find.text('保存'));
        await tester.pumpAndSettle();
        expect(c.preferences.entryDns.mode, 'custom');
        expect(saved.entryDns.candidates.single.address, '9.9.9.9');
        expect(c.connection, 'connected');
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    },
  );
}
