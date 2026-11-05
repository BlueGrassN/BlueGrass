import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/ui/navigation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
    'collapsed sidebar and dock keep destinations reachable on resize',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final controller = fixtureController();
      try {
        await tester.pumpWidget(MyApp(controller: controller));
        await tester.pumpAndSettle();
        await controller.login('fixture', 'fixture');
        await tester.pumpAndSettle();
        final selected = controller.selected?.key;
        await tester.tap(find.byTooltip('收起侧栏'));
        for (var frame = 0; frame < 20; frame++) {
          await tester.pump(const Duration(milliseconds: 16));
          expect(tester.takeException(), isNull, reason: 'sidebar animation');
        }
        await tester.tap(find.byTooltip('我的 IP'));
        await tester.pumpAndSettle();
        expect(find.text('连接资源'), findsOneWidget);
        // Keyboard activation uses the same live navigation item.
        // The tooltip is a child of the item's focusable InkWell.
        Focus.of(tester.element(find.byTooltip('设置'))).requestFocus();
        await tester.pump();
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await tester.pumpAndSettle();
        expect(find.text('入口 DNS'), findsOneWidget);
        tester.view.physicalSize = const Size(800, 720);
        await tester.pumpAndSettle();
        final dockHome = find.descendant(
          of: find.byType(AppDock),
          matching: find.text('首页'),
        );
        await tester.tap(dockHome);
        await tester.pumpAndSettle();
        expect(find.widgetWithText(FilledButton, '连接'), findsOneWidget);
        expect(controller.selected?.key, selected);
        expect(controller.active, isFalse);
        tester.view.physicalSize = const Size(1280, 720);
        await tester.pumpAndSettle();
        await tester.tap(find.byTooltip('展开侧栏'));
        await tester.pumpAndSettle();
        expect(find.text('退出账号').hitTestable(), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.pumpWidget(const SizedBox());
        controller.dispose();
      }
    },
  );
}
