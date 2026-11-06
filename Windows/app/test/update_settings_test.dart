import 'package:bluegrass_client/update_presentation.dart';
import 'package:bluegrass_client/update_settings.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_presentation_test.dart' show FakeUpdateClient, ready, candidate;

void main() {
  testWidgets(
    'shell retains update readiness in settings and about after banner dismissal',
    (tester) async {
      final client = FakeUpdateClient()..checked = ready;
      final ui = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      final c = fixtureController();
      try {
        await ui.check();
        await tester.pumpWidget(MyApp(controller: c, updates: ui));
        await tester.pumpAndSettle();
        await c.login('fixture', 'fixture');
        await tester.pumpAndSettle();
        await tester.tap(find.text('稍后提醒'));
        await tester.pumpAndSettle();
        for (final destination in ['设置', '关于']) {
          await tester.tap(find.text(destination).last);
          await tester.pumpAndSettle();
          expect(find.text('立即安装'), findsOneWidget);
        }
        expect(client.calls, ['check']);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
        ui.dispose();
        await client.dispose();
      }
    },
  );
  testWidgets('unconfigured channel is honest and offers no download/install', (
    tester,
  ) async {
    final client = FakeUpdateClient()..checkFailure = 'UPDATE_UNCONFIGURED';
    final ui = UpdatePresentation(
      client: client,
      pauseBeforeInstall: () async => true,
      handoffAfterLaunch: () async => true,
      exitAfterHandoff: () async {},
    );
    try {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: UpdateSettings(updates: ui)),
        ),
      );
      expect(client.calls, isEmpty);
      await tester.tap(find.text('检查更新'));
      await tester.pumpAndSettle();
      expect(find.textContaining('更新通道未配置'), findsOneWidget);
      expect(find.text('下载更新'), findsNothing);
      expect(find.text('立即安装'), findsNothing);
    } finally {
      await tester.pumpWidget(const SizedBox());
      ui.dispose();
      await client.dispose();
    }
  });
  testWidgets(
    'ready banner is dismissible; settings install requires confirmation and pause',
    (tester) async {
      final client = FakeUpdateClient()..checked = ready;
      final ui = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async {
          client.calls.add('pause');
          return true;
        },
        handoffAfterLaunch: () async {
          client.calls.add('handoff');
          return true;
        },
        exitAfterHandoff: () async {
          client.calls.add('exit');
        },
      );
      try {
        await ui.check();
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: Column(
                children: [
                  UpdateReadyBanner(updates: ui),
                  UpdateSettings(updates: ui),
                ],
              ),
            ),
          ),
        );
        expect(find.byType(AlertDialog), findsNothing);
        await tester.tap(find.text('稍后提醒'));
        await tester.pumpAndSettle();
        expect(find.text('稍后提醒'), findsNothing);
        await tester.tap(find.text('立即安装'));
        await tester.pumpAndSettle();
        expect(find.textContaining('保留 TUN 与网络保护'), findsOneWidget);
        expect(client.calls, ['check']);
        await tester.tap(find.text('取消'));
        await tester.pumpAndSettle();
        expect(client.calls, ['check']);
        await tester.tap(find.text('立即安装'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('暂停并安装'));
        await tester.pumpAndSettle();
        expect(client.calls, [
          'check',
          'pause',
          'launch:$candidate:true',
          'handoff',
          'exit',
        ]);
        expect(find.textContaining('尚未确认安装完成'), findsOneWidget);
      } finally {
        await tester.pumpWidget(const SizedBox());
        ui.dispose();
        await client.dispose();
      }
    },
  );
  testWidgets(
    'metered retry requires separate consent for the displayed package',
    (tester) async {
      final client = FakeUpdateClient()
        ..downloadFailure = 'UPDATE_WAITING_NETWORK';
      final ui = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      try {
        await ui.check();
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(body: UpdateSettings(updates: ui)),
          ),
        );
        await tester.tap(find.text('允许本次下载'));
        await tester.pumpAndSettle();
        expect(client.calls, ['check', 'download:$candidate:false']);
        expect(find.textContaining('仅授权当前版本'), findsOneWidget);
        client.downloadFailure = null;
        await tester.tap(find.text('继续下载'));
        await tester.pumpAndSettle();
        expect(client.calls.last, 'download:$candidate:true');
        expect(find.text('立即安装'), findsOneWidget);
      } finally {
        await tester.pumpWidget(const SizedBox());
        ui.dispose();
        await client.dispose();
      }
    },
  );
}
