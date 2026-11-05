import 'dart:async';

import 'package:bluegrass_client/owned_resources_view.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'resource_ui_regression_test.dart' show QuerySource, record, queryPage;

Future<void> signIn(QuerySource source) => source
    .login(username: 'fixture', password: 'fixture', deviceId: 'fixture')
    .then((_) {});
Widget view(QuerySource source, List<Object> failures, {int revision = 0}) =>
    MaterialApp(
      home: Scaffold(
        body: SingleChildScrollView(
          child: OwnedResourcesView(
            key: ValueKey((source, source.customer)),
            source: source,
            accountKey: source.customer!.id,
            onDataFailure: failures.add,
            openWebsite: () {},
            refreshRevision: revision,
          ),
        ),
      ),
    );

void main() {
  testWidgets('same account new session cannot receive previous late detail', (
    tester,
  ) async {
    final s = QuerySource(), errors = <Object>[];
    await signIn(s);
    await tester.pumpWidget(view(s, errors));
    await tester.pumpAndSettle();
    final pending = Completer<OwnedResourceRecord>();
    s.detail = (_, _) => pending.future;
    await tester.tap(find.text('subscription-1'));
    await tester.pump();
    expect(s.details.length, 1);
    await s.logout();
    await signIn(s);
    await tester.pumpWidget(view(s, errors));
    await tester.pumpAndSettle();
    pending.complete(record('subscription', '1', name: 'RETIRED-DETAIL'));
    await tester.pumpAndSettle();
    expect(find.text('RETIRED-DETAIL'), findsNothing);
    expect(find.text('subscription-1'), findsOneWidget);
    expect(errors, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('foreign account rows and changed detail identity never render', (
    tester,
  ) async {
    final s = QuerySource(), errors = <Object>[];
    await signIn(s);
    s.page = (kind, _) async =>
        queryPage(kind, [record(kind, '1', account: 'other', name: 'FOREIGN')]);
    await tester.pumpWidget(view(s, errors));
    await tester.pumpAndSettle();
    expect(find.text('FOREIGN'), findsNothing);
    expect(errors.single, isA<ApiFailure>());
    s.page = null;
    await tester.pumpWidget(view(s, errors, revision: 1));
    await tester.pumpAndSettle();
    s.detail = (kind, _) async =>
        record(kind, 'DIFFERENT', name: 'WRONG-DETAIL');
    await tester.tap(find.text('subscription-1'));
    await tester.pumpAndSettle();
    expect(find.text('WRONG-DETAIL'), findsNothing);
    expect(find.textContaining('无法确认此资源'), findsOneWidget);
    expect(errors.length, 2);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('refresh spam cannot overlap pending page requests', (
    tester,
  ) async {
    final s = QuerySource(), errors = <Object>[];
    final pending = Completer<ResourceQueryPage>();
    s.page = (_, _) => pending.future;
    await signIn(s);
    await tester.pumpWidget(view(s, errors));
    await tester.pump();
    for (var i = 1; i <= 5; i++) {
      await tester.pumpWidget(view(s, errors, revision: i));
      await tester.pump();
    }
    expect(s.calls.length, 1);
    pending.complete(queryPage('subscription', [record('subscription', '1')]));
    await tester.pumpAndSettle();
    expect(find.text('subscription-1'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets(
    'distinct cursors still stop at bounded traversal without background scanning',
    (tester) async {
      final s = QuerySource(), errors = <Object>[];
      var sequence = 0;
      s.page = (kind, _) async =>
          queryPage(kind, [], next: 'next-${++sequence}');
      await signIn(s);
      await tester.pumpWidget(view(s, errors));
      await tester.pumpAndSettle();
      expect(s.calls.length, 1);
      for (var i = 0; i < 40; i++) {
        await tester.tap(find.text('加载更多订阅'));
        await tester.pumpAndSettle();
      }
      expect(s.calls.length, 40);
      expect(find.text('加载更多订阅'), findsNothing);
      expect(find.textContaining('本轮查询上限'), findsOneWidget);
      expect(errors, isEmpty);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('unsupported capability issues no list or detail query', (
    tester,
  ) async {
    final s = QuerySource()..queries = false, errors = <Object>[];
    await signIn(s);
    await tester.pumpWidget(view(s, errors));
    await tester.pumpAndSettle();
    expect(find.textContaining('未开放资源查询'), findsOneWidget);
    expect(s.calls, isEmpty);
    expect(s.details, isEmpty);
    await tester.pumpWidget(const SizedBox());
  });
}
