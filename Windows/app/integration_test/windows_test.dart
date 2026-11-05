import 'dart:io';
import 'dart:ui' as ui;

import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('Windows DPAPI roundtrip atomic replace and key confinement', (
    tester,
  ) async {
    final key = 'integration_${DateTime.now().microsecondsSinceEpoch}';
    final store = WindowsSessionStore(key);
    try {
      await store.replace({
        'test': 'FAKE-SECRET-NOT-A-CREDENTIAL',
        'generation': 1,
      });
      final file = File(
        '${Platform.environment['LOCALAPPDATA']}\\BlueGrass\\client\\$key.dpapi',
      );
      final bytes = await file.readAsBytes();
      expect(
        String.fromCharCodes(bytes),
        isNot(contains('FAKE-SECRET-NOT-A-CREDENTIAL')),
      );
      expect((await WindowsSessionStore(key).read())?['generation'], 1);
      await store.replace({'generation': 2});
      expect(await store.read(), {'generation': 2});
      await expectLater(
        platform.invokeMethod<void>('writeRecord', {
          'key': '../escape',
          'value': 'test',
        }),
        throwsA(isA<PlatformException>()),
      );
    } finally {
      await store.clear();
    }
    expect(await store.read(), isNull);
  });

  testWidgets(
    'native Windows fixture login selection connection theme and logout',
    (tester) async {
      final c = fixtureController();
      addTearDown(c.dispose);
      const capture = Key('capture');
      await tester.pumpWidget(
        RepaintBoundary(
          key: capture,
          child: MyApp(controller: c),
        ),
      );
      await tester.pumpAndSettle();
      await saveScreenshot(tester, capture, 'login');
      await tester.enterText(
        find.widgetWithText(TextField, '账号或手机号'),
        'fixture-user',
      );
      await tester.enterText(
        find.widgetWithText(TextField, '密码'),
        'fixture-password',
      );
      await tester.tap(find.widgetWithText(FilledButton, '登录'));
      await tester.pumpAndSettle();
      expect(find.text('未连接'), findsOneWidget);
      expect(find.text('香港 · 优选 01'), findsOneWidget);
      await saveScreenshot(tester, capture, 'home-light');
      await tester.tap(find.widgetWithText(FilledButton, '连接'));
      await tester.pumpAndSettle();
      expect(find.text('SOCKS5 传输说明'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, '继续'));
      await tester.pumpAndSettle();
      expect(find.text('模拟已连接'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '暂不开启'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, '断开连接'));
      await tester.pumpAndSettle();
      expect(c.actualExitIp, isNull);
      await tester.tap(find.text('设置').first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('深色'));
      await tester.pumpAndSettle();
      expect(c.preferences.theme, ThemeMode.dark);
      expect(c.customer, isNotNull);
      await saveScreenshot(tester, capture, 'settings-dark');
      await tester.tap(find.text('退出账号').first);
      await tester.pumpAndSettle();
      expect(find.text('欢迎回来'), findsOneWidget);
    },
  );
}

Future<void> saveScreenshot(WidgetTester tester, Key key, String name) async {
  const output = String.fromEnvironment('BLUEGRASS_SCREENSHOTS');
  if (output.isEmpty) return;
  final boundary = tester.renderObject<RenderRepaintBoundary>(find.byKey(key));
  final image = await boundary.toImage(pixelRatio: 1);
  final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
  await Directory(output).create(recursive: true);
  await File('$output/$name.png').writeAsBytes(bytes!.buffer.asUint8List());
  image.dispose();
}
