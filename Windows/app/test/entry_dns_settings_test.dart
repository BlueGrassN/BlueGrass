import 'package:bluegrass_client/entry_dns_policy.dart';
import 'package:bluegrass_client/entry_dns_settings.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('DNS editor limits additions to eight ordered servers', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: EntryDnsSettings(
            value: EntryDnsPolicy.custom(
              List.generate(
                8,
                (i) => EntryDnsCandidate(address: '1.1.1.${i + 1}'),
              ),
            ),
            onSave: (_) async => true,
          ),
        ),
      ),
    );
    await tester.tap(find.text('入口 DNS'));
    await tester.pumpAndSettle();
    final add = find.widgetWithText(TextButton, '添加 DNS');
    await tester.ensureVisible(add);
    expect(tester.widget<TextButton>(add).onPressed, isNull);
    await tester.ensureVisible(find.byTooltip('删除 DNS 8'));
    await tester.tap(find.byTooltip('删除 DNS 8'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextButton>(add).onPressed, isNotNull);
    expect(find.byKey(const ValueKey('dns-address-7')), findsNothing);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
  });
  testWidgets(
    'DNS editor validates numeric endpoints and saves explicit ordered policy',
    (tester) async {
      EntryDnsPolicy? saved;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: EntryDnsSettings(
              value: const EntryDnsPolicy.auto(),
              onSave: (value) async {
                saved = value;
                return true;
              },
            ),
          ),
        ),
      );
      await tester.tap(find.text('入口 DNS'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('自定义'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('dns-address-0')),
        'dns.invalid',
      );
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(saved, isNull);
      expect(find.textContaining('IPv4 或 IPv6'), findsWidgets);
      await tester.enterText(
        find.byKey(const ValueKey('dns-address-0')),
        '9.9.9.9',
      );
      await tester.tap(find.text('添加 DNS'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('dns-address-1')),
        '2606:4700:4700::1111',
      );
      await tester.enterText(find.byKey(const ValueKey('dns-port-1')), '5353');
      await tester.ensureVisible(find.byKey(const ValueKey('dns-transport-1')));
      await tester.tap(find.byKey(const ValueKey('dns-transport-1')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('TCP').last);
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.byTooltip('上移 DNS 2'));
      await tester.tap(find.byTooltip('上移 DNS 2'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(saved!.toJson(), {
        'mode': 'custom',
        'candidates': [
          {'address': '2606:4700:4700::1111', 'port': 5353, 'transport': 'tcp'},
          {'address': '9.9.9.9', 'port': 53, 'transport': 'udp'},
        ],
      });
      expect(find.text('保存'), findsNothing);
    },
  );
  testWidgets(
    'failed preference persistence keeps editor open with no success claim',
    (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: EntryDnsSettings(
              value: const EntryDnsPolicy.auto(),
              onSave: (_) async => false,
            ),
          ),
        ),
      );
      await tester.tap(find.text('入口 DNS'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('保存'));
      await tester.pumpAndSettle();
      expect(find.text('保存'), findsOneWidget);
      expect(find.textContaining('未能保存'), findsOneWidget);
    },
  );
}
