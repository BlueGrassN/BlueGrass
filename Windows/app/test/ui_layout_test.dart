import 'dart:io';
import 'dart:ui' as ui;

import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

// Optional review images use only synthetic data and the installed Windows font.
const _output = String.fromEnvironment('BLUEGRASS_UI_SCREENSHOTS');
const _capture = ValueKey('layout-capture');

// Widget tests normally flatten shadows; review images should show the same
// soft elevation as the desktop renderer.
class _ScreenshotBinding extends AutomatedTestWidgetsFlutterBinding {
  @override
  bool get disableShadows => false;
}

void main() {
  if (_output.isNotEmpty) _ScreenshotBinding();
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    if (_output.isNotEmpty) {
      final font = File('C:/Windows/Fonts/msyh.ttc');
      final loader = FontLoader('Microsoft YaHei')
        ..addFont(
          font.readAsBytes().then((bytes) => ByteData.sublistView(bytes)),
        );
      await loader.load();
      final icons = FontLoader('MaterialIcons')
        ..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
      await icons.load();
      await Directory(_output).create(recursive: true);
    }
  });

  for (final scenario in [
    (width: 480.0, scale: 1.0, dark: false),
    (width: 900.0, scale: 1.0, dark: false),
    (width: 1280.0, scale: 1.0, dark: false),
    (width: 1920.0, scale: 1.0, dark: false),
    (width: 480.0, scale: 1.3, dark: false),
    (width: 1280.0, scale: 1.0, dark: true),
  ]) {
    final name =
        '${scenario.width.toInt()}-${scenario.scale}-${scenario.dark ? 'dark' : 'light'}';
    testWidgets('all pages and connection states fit $name', (tester) async {
      tester.view.physicalSize = Size(scenario.width, 720);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = scenario.scale;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final c = fixtureController();
      try {
        await tester.pumpWidget(
          RepaintBoundary(
            key: _capture,
            child: MyApp(controller: c),
          ),
        );
        await tester.pumpAndSettle();
        await c.updatePreferences(
          c.preferences.copyWith(
            theme: scenario.dark ? ThemeMode.dark : ThemeMode.light,
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: 'login');
        await _save(tester, '$name-login');
        await c.login('preview', 'fixture');
        c.nodes = [
          for (var i = 0; i < 3; i++)
            NodeRecord(
              key: ResourceKey(
                environment: 'fixture',
                dataSource: 'fixture-v1',
                accountKey: 'fixture-account',
                sourceId: 'preview-$i',
              ),
              name: ['德国-192.0.2.10', '美国-198.51.100.20', '日本-203.0.113.30'][i],
              customerRemark: ['办公网络', '团队协作', '这是一条用于检查窄窗口布局的较长客户备注'][i],
              protocol: 'socks5',
              host: 'relay.invalid',
              port: 1080,
              countryCode: ['DEU', 'USA', 'JPN'][i],
              countryName: ['德国', '美国', '日本'][i],
              expiresAtUtc: DateTime.utc(2027, 1, 31, 15, 59, 59),
            ),
        ];
        c.selected = c.nodes.first;
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: 'idle home');
        await _save(tester, '$name-home');
        c.connection = 'connected';
        c.actualMode = 'system_proxy';
        c.actualExitIp = '192.0.2.10';
        c.localProxyAddress = '127.0.0.1:12808';
        await c.updatePreferences(c.preferences);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: 'connected home');
        await _save(tester, '$name-connected');
        // Capture the same real presentation path with explicitly synthetic
        // telemetry. No data is injected into the production data source.
        if (_output.isNotEmpty) {
          const samples = [
            8,
            12,
            18,
            15,
            9,
            5,
            7,
            14,
            23,
            28,
            21,
            16,
            12,
            9,
            14,
            20,
            29,
            31,
            25,
            18,
            15,
            22,
            27,
            24,
            16,
            12,
            18,
            26,
            30,
            23,
          ];
          var upload = BigInt.zero;
          var download = BigInt.zero;
          for (final sample in samples) {
            upload += BigInt.from(sample * 1024);
            download += BigInt.from(sample * 32768);
            c.traffic = TrafficView(
              uploadBytes: upload,
              downloadBytes: download,
              uploadBytesPerSecond: sample * 1024.0,
              downloadBytesPerSecond: sample * 32768.0,
              available: true,
              complete: true,
            );
            await c.updatePreferences(c.preferences);
            await tester.pump(const Duration(seconds: 1));
          }
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull, reason: 'traffic charts');
          await _save(tester, '$name-connected-traffic');
        }
        await tester.tap(find.widgetWithText(FilledButton, '选择资源'));
        await tester.pumpAndSettle();
        expect(
          find.byKey(const ValueKey('home-resource-picker')),
          findsOneWidget,
        );
        expect(tester.takeException(), isNull, reason: 'home resource picker');
        await _save(tester, '$name-resource-picker');
        await tester.tap(find.byTooltip('关闭资源选择'));
        await tester.pumpAndSettle();
        for (final (label, file) in [
          ('我的 IP', 'resources'),
          ('设置', 'settings'),
          ('诊断', 'diagnostics'),
          ('关于', 'about'),
        ]) {
          await tester.tap(find.text(label).last);
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull, reason: label);
          await _save(tester, '$name-$file');
          if (label == '我的 IP') {
            await tester.tap(find.byTooltip('资源详情').first);
            await tester.pumpAndSettle();
            expect(tester.takeException(), isNull, reason: 'resource details');
            await _save(tester, '$name-details');
            await tester.tap(find.text('关闭'));
            await tester.pumpAndSettle();
          }
        }
        c.connection = 'idle';
        c.nodes = [];
        c.selected = null;
        await c.updatePreferences(c.preferences);
        await tester.tap(find.text('首页').last);
        await tester.pumpAndSettle();
        expect(find.text('选择连接资源'), findsOneWidget);
        expect(tester.takeException(), isNull, reason: 'empty home');
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
      }
    });
  }
}

Future<void> _save(WidgetTester tester, String name) async {
  if (_output.isEmpty) return;
  // Asset decoding runs outside the fake test clock. A settled theme animation
  // does not guarantee its newly selected logo has decoded before capture.
  await tester.runAsync(() async {
    for (final element in find.byType(Image).evaluate()) {
      await precacheImage((element.widget as Image).image, element);
    }
  });
  await tester.pumpAndSettle();
  final boundary = tester.renderObject<RenderRepaintBoundary>(
    find.byKey(_capture),
  );
  await tester.runAsync(() async {
    final image = await boundary.toImage(pixelRatio: 1);
    try {
      final data = await image.toByteData(format: ui.ImageByteFormat.png);
      await File('$_output/$name.png').writeAsBytes(data!.buffer.asUint8List());
    } finally {
      image.dispose();
    }
  });
}
