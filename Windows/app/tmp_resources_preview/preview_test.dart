import 'dart:io';
import 'dart:ui' as ui;

import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import '../test/resource_ui_regression_test.dart'
    show QuerySource, UiController, cleanup, queryPage, record;
import '../test/reminder_ui_lifecycle_test.dart' show MemoryStore, Notifications;

const _out = 'E:/workspace/APP/Windows/.artifacts/screenshots/resources-page/preview';
const _capture = ValueKey('cap');

Future<void> _save(WidgetTester tester, String name) async {
  await tester.runAsync(() async {
    for (final element in find.byType(Image).evaluate()) {
      await precacheImage((element.widget as Image).image, element);
    }
  });
  await tester.pumpAndSettle();
  final boundary = tester.renderObject<RenderRepaintBoundary>(find.byKey(_capture));
  await tester.runAsync(() async {
    final image = await boundary.toImage(pixelRatio: 1);
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    await File('$_out/$name.png').writeAsBytes(data!.buffer.asUint8List());
    image.dispose();
  });
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUpAll(() async {
    await (FontLoader('Microsoft YaHei')
          ..addFont(File('C:/Windows/Fonts/msyh.ttc').readAsBytes().then((b) => ByteData.sublistView(b))))
        .load();
    await (FontLoader('MaterialIcons')..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'))).load();
    await Directory(_out).create(recursive: true);
  });
  final messenger = TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  for (final (width, dark) in [(1280.0, false), (1280.0, true), (480.0, false)]) {
    final tag = '${width.toInt()}-${dark ? 'dark' : 'light'}';
    testWidgets('preview $tag', (tester) async {
      messenger.setMockMethodCallHandler(platform, (_) async => null);
      tester.view.physicalSize = Size(width, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final reminders = ExpiryReminderCoordinator(store: MemoryStore(), notifications: Notifications());
      final source = QuerySource()
        ..page = (kind, _) async => queryPage(kind, [
              record(kind, '1', name: '办公资源'),
              record(kind, '2', name: '备用资源'),
            ]);
      final c = UiController(source);
      try {
        await tester.pumpWidget(RepaintBoundary(key: _capture, child: MyApp(controller: c, reminders: reminders)));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        await c.updatePreferences(c.preferences.copyWith(theme: dark ? ThemeMode.dark : ThemeMode.light));
        final now = c.effectiveNowUtc;
        await reminders.synchronize(
          ExpirySnapshot(
            scope: ReminderScope(localUser: 'local', environment: 'fixture', dataSource: 'fixture-v1', accountKey: c.customer!.id),
            items: [
              ExpirySummary(key: const ResourceKey(environment: 'fixture', dataSource: 'fixture-v1', accountKey: 'fixture-account', sourceId: 'e1'), name: '德国-192.0.2.10', expiresAtUtc: now.add(const Duration(days: 2))),
              ExpirySummary(key: const ResourceKey(environment: 'fixture', dataSource: 'fixture-v1', accountKey: 'fixture-account', sourceId: 'e2'), name: '美国-198.51.100.20', expiresAtUtc: now.subtract(const Duration(hours: 3))),
            ],
            complete: false,
            syncGeneration: 'round',
          ),
          effectiveNowUtc: now,
          endOfRound: true,
        );
        final key = c.nodes.first.key;
        NodeRecord node(String id, String name, {String protocol = 'socks5', String code = '', String country = '', DateTime? expiry, String remark = ''}) => NodeRecord(
          key: ResourceKey(environment: key.environment, dataSource: key.dataSource, accountKey: key.accountKey, sourceId: id),
          name: name, protocol: protocol, host: 'relay.invalid', port: 1080, countryCode: code, countryName: country, expiresAtUtc: expiry, customerRemark: remark,
          protocolConfig: protocol == 'tuic' ? ProtocolConfig.fromJson({'version': 1, 'type': 'tuic', 'tls': {'serverName': 'relay.invalid'}, 'tuic': {'password': 'x', 'uuid': '12345678-1234-4234-8234-123456789abc', 'congestionControl': 'bbr', 'udpRelayMode': 'native'}}) : null,
        );
        c.nodes = [
          node('a', '香港-203.0.113.8', code: 'HKG', country: '中国香港', expiry: now.add(const Duration(days: 40)), remark: '家用'),
          node('b', 'TUIC 资源', protocol: 'tuic', code: 'SGP', country: '新加坡', expiry: now.add(const Duration(days: 40))),
          node('c', '已到期资源', code: 'JPN', country: '日本', expiry: now.subtract(const Duration(hours: 2))),
          node('d', '未标注地区的资源', expiry: now.add(const Duration(days: 9))),
        ];
        c.selected = c.nodes.first;
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        await tester.tap(find.text('我的 IP').last);
        await tester.pumpAndSettle();
        await _save(tester, '$tag-list');
        await tester.tap(find.text('全部资源 · 到期清单').evaluate().isEmpty ? find.textContaining('项资源临近到期或已到期') : find.text('x'));
        await tester.pumpAndSettle();
        await _save(tester, '$tag-expiry-list');
        await tester.tap(find.text('德国-192.0.2.10'));
        await tester.pumpAndSettle();
        await _save(tester, '$tag-expiry-item');
        await tester.tap(find.text('关闭').last);
        await tester.pumpAndSettle();
        await tester.tap(find.text('关闭').last);
        await tester.pumpAndSettle();
        await tester.tap(find.byTooltip('资源详情').at(1));
        await tester.pumpAndSettle();
        await _save(tester, '$tag-details-unavailable');
        await tester.tap(find.text('关闭').last);
        await tester.pumpAndSettle();
        await tester.enterText(find.widgetWithText(TextField, '搜索已加载资源'), 'zzz');
        await tester.pumpAndSettle();
        await _save(tester, '$tag-empty-search');
        await tester.enterText(find.byType(TextField).first, '');
        await tester.pumpAndSettle();
        await tester.tap(find.text('全部资源'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('办公资源'));
        await tester.pumpAndSettle();
        await _save(tester, '$tag-owned-detail');
        c.nodes = [];
        await tester.tap(find.text('连接资源'));
        await c.updatePreferences(c.preferences.copyWith(allowLan: true));
        await tester.pumpAndSettle();
        await _save(tester, '$tag-empty');
        expect(tester.takeException(), isNull);
      } finally {
        await cleanup(tester, c);
        reminders.dispose();
        messenger.setMockMethodCallHandler(platform, null);
      }
    });
  }
}
