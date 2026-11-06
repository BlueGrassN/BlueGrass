import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'resource_ui_regression_test.dart'
    show QuerySource, UiController, cleanup, openResources, queryPage, record;

class ClockController extends UiController {
  ClockController(super.source);
  DateTime now = DateTime.utc(2029);
  bool nativeRecoveryPending = false;
  @override
  DateTime get effectiveNowUtc => now;
  @override
  bool get recoveryRequired => nativeRecoveryPending || super.recoveryRequired;
}

OwnedResourceRecord owned(String kind, String id, DateTime? expiry) =>
    OwnedResourceRecord(
      summary: ResourceBrief(
        key: record(kind, id).summary.key,
        name: id,
        expiresAtUtc: expiry,
      ),
      status: 'expired',
    );

void main() {
  testWidgets(
    'connection list hides only expiry older than 72 hours; home keeps recovery',
    (tester) async {
      final c = ClockController(QuerySource());
      await openResources(tester, c, all: false);
      try {
        final scope = c.nodes.first.key;
        NodeRecord node(String id, DateTime? expiry) => NodeRecord(
          key: ResourceKey(
            environment: scope.environment,
            dataSource: scope.dataSource,
            accountKey: scope.accountKey,
            sourceId: id,
          ),
          name: id,
          protocol: 'socks5',
          host: 'relay.invalid',
          port: 1080,
          expiresAtUtc: expiry,
        );
        c.nodes = [
          node(
            'OLD',
            c.now.subtract(const Duration(hours: 72, microseconds: 1)),
          ),
          node('BOUNDARY', c.now.subtract(const Duration(hours: 72))),
          node('RECENT', c.now.subtract(const Duration(hours: 1))),
          node('ACTIVE', c.now.add(const Duration(hours: 1))),
          node('UNKNOWN', null),
        ];
        c.selected = c.nodes.first;
        await c.updatePreferences(const Preferences(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.text('OLD'), findsNothing);
        for (final id in ['BOUNDARY', 'RECENT', 'ACTIVE', 'UNKNOWN']) {
          expect(find.text(id), findsOneWidget);
        }
        expect(
          c.nodes,
          hasLength(5),
          reason: 'visibility must not alter pagination or ownership data',
        );
        expect(prepareProfile(c.nodes[1], c.now), isA<Rejected>());
        c.connection = 'paused';
        c.actualMode = 'system_proxy';
        // A paused native session still needs recovery; changing only the
        // connection label would leave this controller without an active proxy.
        c.nativeRecoveryPending = true;
        expect(c.active, isTrue);
        await tester.tap(find.text('首页').last);
        await tester.pumpAndSettle();
        expect(find.text('OLD'), findsNothing);
        expect(
          tester
              .widget<FilledButton>(
                find.widgetWithText(FilledButton, '恢复并断开代理'),
              )
              .onPressed,
          isNotNull,
        );
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'hidden-only owned page preserves next cursor and renewals reappear',
    (tester) async {
      final source = QuerySource();
      final c = ClockController(source);
      final old = c.now.subtract(const Duration(hours: 73));
      source.page = (kind, cursor) async => cursor == null
          ? queryPage(kind, [owned(kind, 'OLD', old)], next: 'next')
          : queryPage(kind, [owned(kind, '美国-198.87.96.137', null)]);
      await openResources(tester, c);
      try {
        expect(find.text('OLD'), findsNothing);
        await tester.tap(find.text('加载更多订阅'));
        await tester.pumpAndSettle();
        expect(source.calls, contains(('subscription', 'next')));
        expect(find.text('美国-198.87.96.137'), findsOneWidget);
        source.page = (kind, _) async => queryPage(kind, [
          owned(kind, 'OLD', c.now.add(const Duration(days: 1))),
        ]);
        await tester.tap(find.text('资产').last);
        await tester.pumpAndSettle();
        expect(find.text('OLD'), findsOneWidget);
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'owned rows and open details leave display after the 72 hour boundary',
    (tester) async {
      final source = QuerySource();
      final c = ClockController(source);
      final expiry = c.now.subtract(const Duration(hours: 72));
      source.page = (kind, _) async =>
          queryPage(kind, [owned(kind, 'BOUNDARY', expiry)]);
      source.detail = (kind, id) async => owned(kind, id, expiry);
      await openResources(tester, c);
      try {
        await tester.tap(find.text('BOUNDARY'));
        await tester.pumpAndSettle();
        expect(find.text('BOUNDARY'), findsNWidgets(2));
        c.now = c.now.add(const Duration(microseconds: 1));
        await c.updatePreferences(const Preferences(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.text('BOUNDARY'), findsNothing);
        expect(find.text('只读资源详情'), findsNothing);
      } finally {
        await cleanup(tester, c);
      }
    },
  );
}
