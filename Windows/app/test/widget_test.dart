import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/widgets.dart' show Image, SizedBox;
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:flutter/services.dart';
import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';

class RecoverableSource extends FixtureDataSource {
  int attempts = 0;
  @override
  Future<Customer?> restore() async {
    if (++attempts == 1) throw const ApiFailure('timeout');
    return null;
  }
}

void main() {
  testWidgets('transient startup error exposes a working restore retry', (
    tester,
  ) async {
    final source = RecoverableSource();
    final controller = AppController(
      source: source,
      demo: true,
      getDeviceId: () async => 'fixture',
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
    );
    addTearDown(controller.dispose);
    await tester.pumpWidget(MyApp(controller: controller));
    await tester.pumpAndSettle();
    expect(find.text('重试恢复会话'), findsOneWidget);
    await tester.tap(find.text('重试恢复会话'));
    await tester.pumpAndSettle();
    expect(source.attempts, 2);
    expect(find.text('重试恢复会话'), findsNothing);
  });
  testWidgets('corrupt device store exposes explicit local recovery', (
    tester,
  ) async {
    final controller = AppController(
      source: FixtureDataSource(),
      demo: true,
      getDeviceId: () async =>
          throw PlatformException(code: 'unprotect_failed'),
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
    );
    addTearDown(controller.dispose);
    await tester.pumpWidget(MyApp(controller: controller));
    await tester.pumpAndSettle();
    expect(find.text('修复本地存储'), findsOneWidget);
    await tester.tap(find.text('修复本地存储'));
    await tester.pumpAndSettle();
    expect(find.textContaining('不会撤销服务端会话'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(controller.storageReady, isFalse);
  });
  for (final width in [480.0, 1280.0]) {
    testWidgets('authenticated pages render at width $width', (tester) async {
      tester.view.physicalSize = Size(width, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final controller = fixtureController();
      addTearDown(controller.dispose);
      await tester.pumpWidget(MyApp(controller: controller));
      await tester.pumpAndSettle();
      expect(
        find.text('登录成功后，服务端会记录登录时间和来源 IP，用于账号安全。'),
        findsNothing,
        reason: 'login page omits the removed explanatory text',
      );
      await controller.login('fixture', 'fixture');
      await tester.pumpAndSettle();
      expect(find.text('香港 · 优选 01'), findsOneWidget);
      expect(find.text('未连接'), findsOneWidget);
      expect(tester.takeException(), isNull);
      for (final page in ['我的 IP', '设置', '诊断', '关于']) {
        await tester.tap(find.text(page).last);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      }
    });
  }
  testWidgets(
    'login omits the record notice and both account links open the official website',
    (tester) async {
      final openedUrls = <String>[];
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(platform, (call) async {
            if (call.method == 'openUrl') {
              openedUrls.add((call.arguments as Map)['url'] as String);
            }
            return null;
          });
      final controller = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      try {
        await tester.pumpWidget(MyApp(controller: controller));
        await tester.pumpAndSettle();
        expect(
          find.byWidgetPredicate(
            (widget) => widget is Image && widget.semanticLabel == 'BlueGrass',
          ),
          findsWidgets,
        );
        expect(find.text('登录'), findsOneWidget);
        expect(find.text('登录成功后，服务端会记录登录时间和来源 IP，用于账号安全。'), findsNothing);
        await tester.tap(find.text('注册账号'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('找回密码'));
        await tester.pumpAndSettle();
        expect(openedUrls, ['https://sunipip.com/', 'https://sunipip.com/']);
        expect(find.text('已连接'), findsNothing);
      } finally {
        await tester.pumpWidget(const SizedBox());
        controller.dispose();
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(platform, null);
      }
    },
  );
}
