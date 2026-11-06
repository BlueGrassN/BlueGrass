import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'resource_ui_regression_test.dart'
    show QuerySource, UiController, cleanup, queryPage, record;

const _output = String.fromEnvironment('BLUEGRASS_TOOLBAR_SCREENSHOTS');
const _capture = ValueKey('toolbar-capture');
final _scope = find.byKey(const ValueKey('resource-scope-control'));
final _search = find.byKey(const ValueKey('resource-search-control'));
final _country = find.byKey(const ValueKey('resource-country-control'));
final _field = find.descendant(of: _search, matching: find.byType(TextField));

Future<void> _open(
  WidgetTester tester,
  UiController c,
  double width,
  double scale,
) async {
  debugDefaultTargetPlatformOverride = TargetPlatform.windows;
  tester.view.physicalSize = Size(width, 900);
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = scale;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  await tester.pumpWidget(
    RepaintBoundary(
      key: _capture,
      child: MyApp(controller: c),
    ),
  );
  await tester.pumpAndSettle();
  await c.login('fixture', 'fixture');
  c.nodes = [
    for (var i = 0; i < 2; i++)
      NodeRecord(
        key: ResourceKey(
          environment: 'fixture',
          dataSource: 'fixture-v1',
          accountKey: 'fixture-account',
          sourceId: 'node-$i',
        ),
        name: i == 0 ? '办公资源' : '备用资源',
        protocol: 'socks5',
        host: 'relay.invalid',
        port: 1080,
        countryName: i == 0 ? '德国' : '美国',
        countryCode: i == 0 ? 'DEU' : 'USA',
        expiresAtUtc: DateTime.utc(2030),
      ),
  ];
  await c.updatePreferences(c.preferences.copyWith(theme: ThemeMode.light));
  await tester.pumpAndSettle();
  await tester.tap(find.text('我的 IP').last);
  await tester.pumpAndSettle();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    if (_output.isEmpty) return;
    final font = FontLoader('Microsoft YaHei')
      ..addFont(
        File('C:/Windows/Fonts/msyh.ttc')
            .readAsBytes()
            .then((bytes) => ByteData.sublistView(bytes)),
      );
    await font.load();
    await (FontLoader(
      'MaterialIcons',
    )..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    await Directory(_output).create(recursive: true);
  });

  for (final (width, scale) in [
    (480.0, 1.0),
    (900.0, 1.0),
    (1280.0, 1.0),
    (480.0, 1.3),
  ]) {
    testWidgets(
      'resource toolbar stays aligned through switching and loading at $width/$scale',
      (tester) async {
        final pending = Completer<ResourceQueryPage>();
        final source = QuerySource()..page = (_, _) => pending.future;
        final c = UiController(source);
        try {
          await _open(tester, c, width, scale);
          final controls = [_scope, _search, _country];
          final positions = controls.map(tester.getRect).toList();
          final labels = [
            tester.getRect(find.text('连接资源')),
            tester.getRect(find.text('全部资源')),
          ];
          for (final rect in positions) {
            expect(rect.height, 48);
          }
          expect(tester.getSize(find.byType(SegmentedButton<bool>)).height, 48);
          // The outer border can be taller than the internally painted button.
          final segmentMaterial = find
              .ancestor(of: find.text('连接资源'), matching: find.byType(Material))
              .first;
          expect(tester.getSize(segmentMaterial).height, 48);
          expect(
            tester.getCenter(find.text('连接资源')).dy,
            closeTo(tester.getCenter(_scope).dy, 1),
          );
          expect(
            tester.getCenter(find.text('搜索已加载资源')).dy,
            // Scaled font leading can shift the glyph box by a fraction of a
            // pixel; still reject the 4px desktop-density regression.
            closeTo(tester.getCenter(_search).dy, 2),
          );
          if (width == 1280) {
            expect(positions.map((rect) => rect.top).toSet(), hasLength(1));
          }
          await _save(tester, '${width.toInt()}-$scale-connect');
          await tester.tap(find.text('全部资源'));
          for (final elapsed in [16, 250]) {
            await tester.pump(Duration(milliseconds: elapsed));
            expect(controls.map(tester.getRect).toList(), positions);
            expect(tester.getRect(find.text('连接资源')), labels[0]);
            expect(tester.getRect(find.text('全部资源')), labels[1]);
          }
          expect(
            tester
                .widget<DropdownMenu<String>>(find.byType(DropdownMenu<String>))
                .enabled,
            isFalse,
          );
          expect(find.byTooltip('全部资源暂不支持地区筛选'), findsOneWidget);
          expect(_field, findsOneWidget);
          pending.complete(
            queryPage('subscription', [
              record('subscription', '1', name: '办公资源'),
              record('subscription', '2', name: '备用资源'),
            ]),
          );
          await tester.pumpAndSettle();
          expect(controls.map(tester.getRect).toList(), positions);
          await _save(tester, '${width.toInt()}-$scale-all');
          await tester.tap(find.text('连接资源'));
          await tester.pumpAndSettle();
          expect(controls.map(tester.getRect).toList(), positions);
          expect(
            tester
                .widget<DropdownMenu<String>>(find.byType(DropdownMenu<String>))
                .enabled,
            isTrue,
          );
          expect(tester.takeException(), isNull);
        } finally {
          try {
            await cleanup(tester, c);
          } finally {
            debugDefaultTargetPlatformOverride = null;
          }
        }
      },
    );
  }

  testWidgets(
    'shared search filters owned names IDs and status without refetch; connection country survives',
    (tester) async {
      final source = QuerySource()
        ..page = (kind, _) async => queryPage(kind, [
          record(kind, '1', name: '办公资源'),
          record(kind, '2', name: '备用资源'),
        ]);
      final c = UiController(source);
      try {
        await _open(tester, c, 1280, 1);
        await tester.tap(find.byType(DropdownMenu<String>));
        await tester.pumpAndSettle();
        await tester.tap(find.text('德国').last);
        await tester.pumpAndSettle();
        expect(find.widgetWithText(ListTile, '备用资源'), findsNothing);
        await tester.enterText(_field, '办公');
        await tester.tap(find.text('全部资源'));
        await tester.pumpAndSettle();
        expect(tester.widget<TextField>(_field).controller!.text, '办公');
        expect(find.widgetWithText(ListTile, '办公资源'), findsOneWidget);
        expect(find.widgetWithText(ListTile, '备用资源'), findsNothing);
        final regionText = find.descendant(
          of: _country,
          matching: find.byType(TextField),
        );
        expect(tester.widget<TextField>(regionText).controller!.text, '全部地区');
        await tester.enterText(_field, '2');
        await tester.pumpAndSettle();
        expect(find.widgetWithText(ListTile, '备用资源'), findsOneWidget);
        expect(find.widgetWithText(ListTile, '办公资源'), findsNothing);
        await tester.enterText(_field, ' EXPIRED ');
        await tester.pumpAndSettle();
        expect(find.widgetWithText(ListTile, '备用资源'), findsOneWidget);
        expect(find.widgetWithText(ListTile, '办公资源'), findsOneWidget);
        expect(source.calls, [('subscription', null)]);
        await tester.tap(find.text('资产'));
        await tester.pumpAndSettle();
        expect(source.calls, [('subscription', null), ('asset', null)]);
        expect(find.widgetWithText(ListTile, '备用资源'), findsOneWidget);
        await tester.enterText(_field, '');
        await tester.tap(find.text('连接资源'));
        await tester.pumpAndSettle();
        expect(tester.widget<TextField>(regionText).controller!.text, '德国');
        expect(find.widgetWithText(ListTile, '办公资源'), findsOneWidget);
        expect(find.widgetWithText(ListTile, '备用资源'), findsNothing);
        expect(tester.takeException(), isNull);
      } finally {
        try {
          await cleanup(tester, c);
        } finally {
          debugDefaultTargetPlatformOverride = null;
        }
      }
    },
  );
}

Future<void> _save(WidgetTester tester, String name) async {
  if (_output.isEmpty) return;
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
