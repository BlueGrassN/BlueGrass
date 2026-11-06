import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_presentation_test.dart' show FakeUpdateClient;

class QuerySource extends FixtureDataSource implements ResourceQueryDataSource {
  final calls = <(String, String?)>[];
  final details = <(String, String)>[];
  bool queries = true;
  Future<ResourceQueryPage> Function(String, String?)? page;
  Future<OwnedResourceRecord> Function(String, String)? detail;
  @override
  Future<DataSourceCapabilities> refreshCapabilities() async =>
      DataSourceCapabilities(resourceQueries: queries);
  @override
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async {
    calls.add((kind, cursor));
    return page == null
        ? queryPage(kind, [record(kind, '1')])
        : page!(kind, cursor);
  }

  @override
  Future<OwnedResourceRecord> getResource(
    String id, {
    String kind = 'subscription',
  }) async {
    details.add((kind, id));
    return detail == null
        ? record(kind, id, name: 'Fresh detail')
        : detail!(kind, id);
  }

  @override
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async => queryPage(kind, []);
}

OwnedResourceRecord record(
  String kind,
  String id, {
  String? name,
  String account = 'fixture-account',
}) => OwnedResourceRecord(
  summary: ResourceBrief(
    key: ResourceKey(
      environment: 'fixture',
      dataSource: 'fixture-v1',
      accountKey: account,
      kind: kind,
      sourceId: id,
    ),
    name: name ?? '$kind-$id',
    expiresAtUtc: DateTime.utc(2030),
  ),
  status: 'expired',
);
ResourceQueryPage queryPage(
  String kind,
  List<OwnedResourceRecord> items, {
  String? next,
}) => ResourceQueryPage(
  items: items,
  hasMore: next != null,
  nextCursor: next,
  kind: kind,
  ownershipCoverage: 'conservative',
  consistency: 'per_page',
);

class UiController extends AppController {
  UiController(CustomerDataSource source)
    : super(
        source: source,
        demo: false,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
  final restrictions = <bool>[];
  // A boundary spy: tests verify the shell forwards verified updates and
  // removes its listener; the controller's enforcement has its own tests.
  @override
  void setUpdateUnsupported(bool value) {
    restrictions.add(value);
    super.setUpdateUnsupported(value);
  }

  @override
  DateTime get effectiveNowUtc => DateTime.utc(2029);
}

Future<void> openResources(
  WidgetTester tester,
  UiController c, {
  bool all = true,
}) async {
  tester.view.physicalSize = const Size(1400, 1800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(MyApp(controller: c));
  await tester.pumpAndSettle();
  await c.login('fixture', 'fixture');
  await tester.pumpAndSettle();
  await tester.tap(find.text('我的 IP').last);
  await tester.pumpAndSettle();
  if (all) {
    await tester.tap(find.text('全部资源'));
    await tester.pumpAndSettle();
  }
}

Future<void> cleanup(WidgetTester tester, AppController c) async {
  await tester.pumpWidget(const SizedBox());
  c.dispose();
}

void main() {
  testWidgets(
    'TUIC and AnyTLS stay visible with an Xray reason and disabled selection',
    (tester) async {
      final c = UiController(FixtureDataSource());
      try {
        await openResources(tester, c, all: false);
        final original = c.nodes.first;
        final selectionBefore = c.selected?.key;
        final unsupported = [
          for (final protocol in ['tuic', 'anytls'])
            NodeRecord(
              key: ResourceKey(
                environment: original.key.environment,
                dataSource: original.key.dataSource,
                accountKey: original.key.accountKey,
                sourceId: protocol,
              ),
              name: protocol.toUpperCase(),
              protocol: protocol,
              host: 'relay.invalid',
              port: 443,
              protocolConfig: ProtocolConfig.fromJson({
                'version': 1,
                'type': protocol,
                'tls': {'serverName': 'relay.invalid'},
                protocol: {
                  'password': 'fixture',
                  if (protocol == 'tuic') ...{
                    'uuid': '12345678-1234-4234-8234-123456789abc',
                    'congestionControl': 'bbr',
                    'udpRelayMode': 'native',
                  },
                },
              }),
            ),
        ];
        c.nodes = unsupported;
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.text('当前 Xray 内核不支持此协议，请选择其他资源。'), findsNWidgets(2));
        for (final node in unsupported) {
          final tile = find.widgetWithText(ListTile, node.name);
          expect(tile, findsOneWidget);
          final choice = find.descendant(
            of: tile,
            matching: find.widgetWithText(OutlinedButton, '暂不可连接'),
          );
          expect(tester.widget<OutlinedButton>(choice).onPressed, isNull);
          await tester.tap(find.text(node.name));
          await tester.pumpAndSettle();
          expect(find.byType(AlertDialog), findsOneWidget);
          expect(c.selected?.key, selectionBefore);
          expect(c.active, isFalse);
          await tester.tap(find.text('关闭'));
          await tester.pumpAndSettle();
        }
        expect(tester.takeException(), isNull);
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'late auth error from retired owned query cannot clear a new login before rebuild',
    (tester) async {
      final s = QuerySource();
      final pending = Completer<ResourceQueryPage>();
      s.page = (_, _) => pending.future;
      final c = UiController(s);
      await openResources(tester, c, all: false);
      await tester.tap(find.text('全部资源'));
      await tester.pump();
      expect(s.calls.length, 1);
      await c.logout();
      await c.login('new-login', 'fixture');
      final account = c.customer;
      pending.completeError(const ApiFailure('session_revoked', status: 401));
      // Let the old completion run before the next frame disposes its widget.
      await tester.idle();
      expect(c.customer, same(account));
      await cleanup(tester, c);
    },
  );
  testWidgets(
    'available owned queries load both kinds independently and reprove readonly details',
    (tester) async {
      final s = QuerySource();
      s.page = (kind, cursor) async => queryPage(kind, [
        record(kind, cursor == null ? '1' : '2'),
      ], next: cursor == null ? '$kind-next' : null);
      final c = UiController(s);
      await openResources(tester, c);
      expect(find.text('subscription-1'), findsOneWidget);
      expect(find.textContaining('部分同步'), findsWidgets);
      expect(s.calls, [('subscription', null)]);
      await tester.tap(find.text('加载更多订阅'));
      await tester.pumpAndSettle();
      expect(find.text('subscription-2'), findsOneWidget);
      await tester.tap(find.text('资产'));
      await tester.pumpAndSettle();
      expect(find.text('asset-1'), findsOneWidget);
      await tester.tap(find.text('asset-1'));
      await tester.pumpAndSettle();
      expect(s.details, [('asset', '1')]);
      expect(find.text('Fresh detail'), findsOneWidget);
      expect(find.textContaining('不代表连接资格'), findsOneWidget);
      expect(c.active, isFalse);
      expect(s.calls, [
        ('subscription', null),
        ('subscription', 'subscription-next'),
        ('asset', null),
      ]);
      await cleanup(tester, c);
    },
  );

  testWidgets(
    'empty pages remain partial and cursor cycles stop additional paging',
    (tester) async {
      final s = QuerySource()
        ..page = (kind, cursor) async => queryPage(kind, [], next: 'repeat');
      final c = UiController(s);
      await openResources(tester, c);
      expect(find.text('已加载资源中没有可显示的资源。'), findsOneWidget);
      expect(find.textContaining('不代表账号全部资产'), findsOneWidget);
      await tester.tap(find.text('加载更多订阅'));
      await tester.pumpAndSettle();
      expect(find.textContaining('分页异常'), findsOneWidget);
      expect(find.text('加载更多订阅'), findsNothing);
      expect(s.calls.length, 2);
      await cleanup(tester, c);
    },
  );

  testWidgets('leaving account drops late owned pages without revealing rows', (
    tester,
  ) async {
    final pending = Completer<ResourceQueryPage>();
    final s = QuerySource()..page = (_, _) => pending.future;
    final c = UiController(s);
    // No animated indefinite spinner is required to settle a pending query.
    await openResources(tester, c, all: false);
    await tester.tap(find.text('全部资源'));
    await tester.pump();
    expect(s.calls, [('subscription', null)]);
    await c.logout();
    await tester.pump();
    pending.complete(
      queryPage('subscription', [
        record('subscription', '1', name: 'OLD-ACCOUNT'),
      ]),
    );
    await tester.pumpAndSettle();
    expect(find.text('OLD-ACCOUNT'), findsNothing);
    expect(tester.takeException(), isNull);
    await cleanup(tester, c);
  });

  testWidgets(
    'terminal owned query errors reach controller account invalidation',
    (tester) async {
      final s = QuerySource()
        ..page = (_, _) async =>
            throw const ApiFailure('session_revoked', status: 401);
      final c = UiController(s);
      await openResources(tester, c);
      expect(c.customer, isNull);
      expect(find.text('登录'), findsOneWidget);
      await cleanup(tester, c);
    },
  );

  testWidgets(
    'nodes without expected IP remain selectable while expired nodes remain readonly',
    (tester) async {
      final c = UiController(FixtureDataSource());
      await openResources(tester, c, all: false);
      final key = c.nodes.first.key;
      NodeRecord node(
        String name, {
        String? exit = '1.1.1.1',
        DateTime? expiry,
        String country = '日本',
      }) => NodeRecord(
        key: ResourceKey(
          environment: key.environment,
          dataSource: key.dataSource,
          accountKey: key.accountKey,
          sourceId: name,
        ),
        name: name,
        protocol: 'socks5',
        host: 'relay.invalid',
        port: 1080,
        countryName: country,
        expectedExitIpv4: exit,
        expiresAtUtc: expiry,
      );
      c.nodes = [
        node('READY'),
        node('MISSING', exit: null, country: '中国香港'),
        node('EXPIRED', expiry: DateTime.utc(2029)),
      ];
      // Trigger a real shell rebuild without selecting or connecting a resource.
      await c.updatePreferences(c.preferences.copyWith(allowLan: false));
      await tester.pumpAndSettle();
      try {
        expect(find.text('READY'), findsOneWidget);
        expect(find.text('MISSING'), findsOneWidget);
        expect(find.text('EXPIRED'), findsOneWidget);
        expect(find.text('连接资源'), findsOneWidget);
        expect(find.textContaining('服务未提供固定出口 IP'), findsNothing);
        expect(find.textContaining('资源已到期'), findsOneWidget);
        final missing = find.widgetWithText(ListTile, 'MISSING');
        final action = find.descendant(
          of: missing,
          matching: find.byType(OutlinedButton),
        );
        expect(tester.widget<OutlinedButton>(action).onPressed, isNotNull);
        expect(
          tester
              .widget<OutlinedButton>(
                find.descendant(
                  of: find.widgetWithText(ListTile, 'EXPIRED'),
                  matching: find.byType(OutlinedButton),
                ),
              )
              .onPressed,
          isNull,
        );
        final selected = c.selected?.key;
        await tester.tap(
          find.descendant(of: missing, matching: find.byTooltip('资源详情')),
        );
        await tester.pumpAndSettle();
        expect(find.byType(AlertDialog), findsOneWidget);
        expect(find.text('节点出口 IP'), findsOneWidget);
        expect(find.text('暂未获取'), findsOneWidget);
        expect(find.text('预期出口 IP'), findsNothing);
        expect(c.selected?.key, selected);
        expect(c.active, isFalse);
        await tester.tap(find.text('关闭'));
        await tester.pumpAndSettle();

        final search = find.widgetWithText(TextField, '搜索已加载资源');
        await tester.enterText(search, 'MISSING');
        await tester.pumpAndSettle();
        expect(find.widgetWithText(ListTile, 'MISSING'), findsOneWidget);
        expect(find.text('READY'), findsNothing);
        await tester.enterText(search, '');
        await tester.pumpAndSettle();
        await tester.tap(find.byType(DropdownMenu<String>));
        await tester.pumpAndSettle();
        await tester.tap(find.text('中国香港').last);
        await tester.pumpAndSettle();
        expect(find.text('MISSING'), findsOneWidget);
        expect(find.text('READY'), findsNothing);
        expect(find.text('EXPIRED'), findsNothing);
        expect(c.active, isFalse);
        await tester.tap(find.text('MISSING'));
        await tester.pumpAndSettle();
        expect(c.selected?.key.sourceId, 'MISSING');
        expect(
          c.active,
          isFalse,
          reason: 'selection does not automatically connect',
        );
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'four returned nodes without expected exits remain visible on a narrow page',
    (tester) async {
      final c = UiController(FixtureDataSource());
      await openResources(tester, c, all: false);
      tester.view.physicalSize = const Size(480, 1800);
      final key = c.nodes.first.key;
      c.nodes = List.generate(
        4,
        (i) => NodeRecord(
          key: ResourceKey(
            environment: key.environment,
            dataSource: key.dataSource,
            accountKey: key.accountKey,
            sourceId: 'returned-$i',
          ),
          name: 'Returned resource $i',
          protocol: 'socks5',
          host: 'relay.invalid',
          port: 1080,
          countryName: '中国香港',
        ),
      );
      await c.updatePreferences(c.preferences.copyWith(allowLan: false));
      await tester.pumpAndSettle();
      try {
        for (var i = 0; i < 4; i++) {
          expect(find.text('Returned resource $i'), findsOneWidget);
        }
        expect(find.textContaining('服务未提供固定出口 IP'), findsNothing);
        expect(find.text('暂不可连接'), findsNothing);
        final choices = tester.widgetList<OutlinedButton>(
          find.widgetWithText(OutlinedButton, '选择'),
        );
        expect(choices.length, 4);
        expect(choices.every((button) => button.onPressed != null), isTrue);
        expect(find.byTooltip('资源详情'), findsNWidgets(4));
        expect(c.active, isFalse);
        expect(tester.takeException(), isNull);
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'home and open details track current observation without falling back to node metadata',
    (tester) async {
      final c = UiController(FixtureDataSource());
      await openResources(tester, c, all: false);
      try {
        // Keep expiry on the controller's fixed clock instead of the fixture
        // source's wall clock, so this observation test has a visible resource.
        c.nodes = [
          NodeRecord(
            key: c.nodes.first.key,
            name: 'Observed resource',
            protocol: 'socks5',
            host: 'relay.invalid',
            port: 1080,
            expiresAtUtc: c.effectiveNowUtc.add(const Duration(days: 30)),
            expectedExitIpv4: '1.1.1.1',
          ),
        ];
        await tester.tap(find.text('首页').last);
        await tester.pumpAndSettle();
        c.selected = c.nodes.single;
        c.connection = 'connected';
        c.actualMode = 'system_proxy';
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.text('暂未获取'), findsOneWidget);
        expect(find.text('1.1.1.1'), findsNothing);
        expect(find.text('预期出口 IP'), findsNothing);
        c.actualExitIp = '9.9.9.9';
        await c.updatePreferences(c.preferences.copyWith(allowLan: true));
        await tester.pumpAndSettle();
        expect(find.text('9.9.9.9'), findsOneWidget);
        await tester.tap(find.text('我的 IP').last);
        await tester.pumpAndSettle();
        await tester.tap(find.byTooltip('资源详情').first);
        await tester.pumpAndSettle();
        expect(find.text('9.9.9.9'), findsOneWidget);
        c.actualExitIp = '8.8.8.8';
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.text('8.8.8.8'), findsOneWidget);
        expect(find.text('9.9.9.9'), findsNothing);
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'home connection actions keep mode selection nearby and locked while active',
    (tester) async {
      final c = UiController(FixtureDataSource());
      await openResources(tester, c, all: false);
      try {
        c.selected = NodeRecord(
          key: c.nodes.first.key,
          name: 'Home resource',
          protocol: 'socks5',
          host: 'relay.invalid',
          port: 1080,
        );
        await tester.tap(find.text('首页').last);
        await tester.pumpAndSettle();
        final connect = find.widgetWithText(FilledButton, '连接');
        final choose = find.widgetWithText(FilledButton, '选择资源');
        final mode = find.byKey(const ValueKey('tun-mode-button'));
        // The dashboard puts the start button and the resource action in the
        // status card and the TUN switch in the control row right below it:
        // all three are on the first screen, without scrolling or navigating.
        expect(tester.widget<FilledButton>(connect).onPressed, isNotNull);
        expect(tester.widget<FilledButton>(choose).onPressed, isNotNull);
        for (final control in [connect, choose, mode]) {
          expect(control.hitTestable(), findsOneWidget);
        }
        expect(
          tester.getTopLeft(mode).dy,
          greaterThan(tester.getBottomLeft(connect).dy),
        );
        expect(
          tester.getTopLeft(mode).dy - tester.getBottomLeft(connect).dy,
          lessThan(240),
        );
        expect(tester.widget<Switch>(mode).value, isFalse);
        await tester.tap(mode);
        await tester.pumpAndSettle();
        expect(c.preferences.mode, 'tun');
        await tester.tap(mode);
        await tester.pumpAndSettle();
        expect(c.preferences.mode, 'system_proxy');

        c.connection = 'connected';
        c.actualMode = 'system_proxy';
        c.localProxyAddress = '127.0.0.1:12808';
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        expect(tester.widget<Switch>(mode).onChanged, isNull);
        expect(find.text('127.0.0.1:12808'), findsOneWidget);
        expect(
          tester
              .widget<FilledButton>(find.widgetWithText(FilledButton, '断开连接'))
              .onPressed,
          isNotNull,
        );
        expect(tester.widget<FilledButton>(choose).onPressed, isNotNull);
        tester.view.physicalSize = const Size(480, 1800);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        expect(mode.hitTestable(), findsOneWidget);
        await tester.tap(choose);
        await tester.pumpAndSettle();
        expect(
          find.byKey(const ValueKey('home-resource-picker')),
          findsOneWidget,
        );
        expect(
          find.byKey(const ValueKey('resource-scope-control')),
          findsNothing,
        );
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'production mode label prioritizes actual mode then selected mode',
    (tester) async {
      final c = UiController(FixtureDataSource());
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      await c.login('fixture', 'fixture');
      await c.updatePreferences(c.preferences.copyWith(mode: 'tun'));
      await tester.pumpAndSettle();
      final label = find.byKey(const ValueKey('connection-mode'));
      expect(tester.widget<Text>(label).data, 'TUN 接管');
      c.actualMode = 'unknown';
      await c.updatePreferences(c.preferences.copyWith(allowLan: true));
      await tester.pumpAndSettle();
      expect(tester.widget<Text>(label).data, contains('待确认'));
      c.actualMode = 'system_proxy';
      await c.updatePreferences(c.preferences.copyWith(allowLan: false));
      await tester.pumpAndSettle();
      expect(tester.widget<Text>(label).data, '系统代理');
      c.actualMode = 'tun';
      await c.updatePreferences(c.preferences.copyWith(mode: 'system_proxy'));
      await tester.pumpAndSettle();
      expect(tester.widget<Text>(label).data, 'TUN 接管');
      await cleanup(tester, c);
    },
  );

  testWidgets(
    'verified initial/update snapshots forward restriction; transient absence and disposal do not clear it',
    (tester) async {
      final client = FakeUpdateClient();
      final updates = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      // Up-to-date with unsupported is a validated helper result with no download.
      client.checked = const UpdateSnapshot(
        state: UpdateState.upToDate,
        unsupported: true,
      );
      await updates.check();
      final c = UiController(FixtureDataSource());
      await tester.pumpWidget(MyApp(controller: c, updates: updates));
      await tester.pumpAndSettle();
      expect(c.restrictions.last, isTrue);
      expect(c.updateUnsupported, isTrue);
      client.checkFailure = 'UPDATE_TIMEOUT';
      await updates.check();
      expect(c.restrictions.last, isTrue);
      client.checkFailure = null;
      client.checked = const UpdateSnapshot(state: UpdateState.upToDate);
      await updates.check();
      expect(c.restrictions.last, isFalse);
      expect(c.updateUnsupported, isFalse);
      await tester.pumpWidget(const SizedBox());
      final count = c.restrictions.length;
      await updates.check();
      expect(c.restrictions.length, count);
      c.dispose();
      updates.dispose();
      await client.dispose();
    },
  );
}
