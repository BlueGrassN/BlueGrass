import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'resource_ui_regression_test.dart'
    show QuerySource, UiController, cleanup, openResources, queryPage, record;

void main() {
  const asset = '美国-198.87.96.137', remark = '我的办公节点';
  testWidgets(
    'connection list and detail use asset title with a separate customer remark',
    (tester) async {
      final c = UiController(QuerySource());
      await openResources(tester, c, all: false);
      try {
        c.nodes = [
          NodeRecord(
            key: c.nodes.first.key,
            name: asset,
            customerRemark: remark,
            protocol: 'socks5',
            host: 'relay.invalid',
            port: 1080,
          ),
        ];
        await c.updatePreferences(c.preferences.copyWith(allowLan: false));
        await tester.pumpAndSettle();
        expect(find.widgetWithText(ListTile, asset), findsOneWidget);
        expect(find.widgetWithText(ListTile, remark), findsOneWidget);
        expect(
          tester.getTopLeft(find.text(remark)).dx,
          greaterThan(tester.getTopRight(find.text(asset)).dx),
        );
        expect(
          (tester.getCenter(find.text(remark)).dy -
                  tester.getCenter(find.text(asset)).dy)
              .abs(),
          lessThan(4),
        );
        await tester.tap(find.byTooltip('资源详情'));
        await tester.pumpAndSettle();
        expect(
          tester.widget<AlertDialog>(find.byType(AlertDialog)).title,
          isA<Text>().having((t) => t.data, 'asset title', asset),
        );
        expect(find.text('客户备注'), findsOneWidget);
        expect(
          find.descendant(
            of: find.byType(AlertDialog),
            matching: find.text(remark),
          ),
          findsOneWidget,
        );
        await tester.tap(find.text('关闭'));
        await tester.pumpAndSettle();

        const longRemark = '办公使用的美国节点，供团队日常工作和远程连接使用';
        c.nodes = [
          NodeRecord(
            key: c.nodes.first.key,
            name: asset,
            customerRemark: longRemark,
            protocol: 'socks5',
            host: 'relay.invalid',
            port: 1080,
          ),
        ];
        tester.view.physicalSize = const Size(480, 1800);
        await c.updatePreferences(c.preferences.copyWith(allowLan: true));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        expect(find.widgetWithText(ListTile, longRemark), findsOneWidget);
        expect(find.byTooltip(longRemark), findsOneWidget);
        expect(
          tester.getTopLeft(find.text(longRemark)).dx,
          greaterThan(tester.getTopRight(find.text(asset)).dx),
        );
      } finally {
        await cleanup(tester, c);
      }
    },
  );

  testWidgets(
    'owned resource keeps asset title while detail shows customer remark',
    (tester) async {
      final source = QuerySource();
      OwnedResourceRecord row(String kind) => OwnedResourceRecord(
        summary: ResourceBrief(
          key: record(kind, '11').summary.key,
          name: asset,
          customerRemark: remark,
        ),
        status: 'active',
      );
      source.page = (kind, _) async => queryPage(kind, [row(kind)]);
      source.detail = (kind, _) async => row(kind);
      final c = UiController(source);
      await openResources(tester, c);
      try {
        expect(find.widgetWithText(ListTile, asset), findsOneWidget);
        expect(find.textContaining(remark), findsNothing);
        await tester.tap(find.text(asset));
        await tester.pumpAndSettle();
        expect(find.text('客户备注：$remark'), findsOneWidget);
        expect(find.text(asset), findsNWidgets(2));
      } finally {
        await cleanup(tester, c);
      }
    },
  );
}
