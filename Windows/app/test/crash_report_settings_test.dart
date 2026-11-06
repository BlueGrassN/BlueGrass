import 'dart:convert';

import 'package:bluegrass_client/crash_reports.dart';
import 'package:bluegrass_client/crash_report_settings.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'crash_reports_test.dart' show MemoryCrashStore, signal;

void main() {
  testWidgets(
    'consent required; unconfigured receiver unavailable; preview before export',
    (tester) async {
      final reports = CrashReportCoordinator(store: MemoryCrashStore());
      String? exported;
      await reports.initialize();
      try {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: CrashReportSettings(
                reports: reports,
                exportFile: (value) async {
                  exported = value;
                  return 'test-export.json';
                },
              ),
            ),
          ),
        );
        final toggle = find.widgetWithText(SwitchListTile, '崩溃报告');
        expect(tester.widget<SwitchListTile>(toggle).value, isFalse);
        expect(find.textContaining('CONFIG_EMPTY'), findsWidgets);
        await tester.tap(toggle);
        await tester.pumpAndSettle();
        expect(reports.enabled, isFalse);
        expect(find.textContaining('不包含账号'), findsOneWidget);
        await tester.tap(find.text('同意并开启'));
        await tester.pumpAndSettle();
        await reports.record(signal);
        await tester.pumpAndSettle();
        expect(reports.enabled, isTrue);
        final send = find.widgetWithText(OutlinedButton, '发送报告');
        expect(tester.widget<OutlinedButton>(send).onPressed, isNull);
        await tester.tap(find.text('预览并导出'));
        await tester.pumpAndSettle();
        expect(exported, isNull);
        await tester.tap(find.text('保存 JSON 文件'));
        await tester.pumpAndSettle();
        expect((jsonDecode(exported!)['reports'] as List), hasLength(1));
        expect(find.textContaining('test-export.json'), findsOneWidget);
        await tester.tap(find.text('关闭'));
        await tester.pumpAndSettle();
        await tester.tap(toggle);
        await tester.pumpAndSettle();
        expect(reports.enabled, isFalse);
        expect(reports.count, 0);
      } finally {
        await tester.pumpWidget(const SizedBox());
        reports.dispose();
      }
    },
  );
}
