import 'dart:convert';

import 'package:bluegrass_client/app_version.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final (platform, name) in [
    (TargetPlatform.windows, 'Windows'),
    (TargetPlatform.macOS, 'macOS'),
  ]) {
    for (final openLicenses in [false, true]) {
      testWidgets(
        '$name ${openLicenses ? 'licenses' : 'about'} shows only the public version while diagnostics retain build',
        (tester) async {
          debugDefaultTargetPlatformOverride = platform;
          tester.view.physicalSize = const Size(1280, 1000);
          tester.view.devicePixelRatio = 1;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          final controller = fixtureController();
          addTearDown(controller.dispose);
          try {
            await tester.pumpWidget(MyApp(controller: controller));
            await tester.pumpAndSettle();
            await controller.login('fixture', 'fixture');
            await tester.pumpAndSettle();
            expect(
              jsonDecode(controller.diagnosticJson())['version'],
              '$clientVersion+$clientBuild',
            );
            await tester.tap(find.text('关于').last);
            await tester.pumpAndSettle();
            if (openLicenses) {
              await tester.ensureVisible(find.text('开源许可'));
              await tester.tap(find.text('开源许可'));
              await tester.pumpAndSettle();
              expect(
                tester
                    .widget<LicensePage>(find.byType(LicensePage))
                    .applicationVersion,
                clientVersion,
              );
              expect(find.text(clientVersion), findsOneWidget);
            } else {
              expect(find.text('$name 客户端 $clientVersion'), findsOneWidget);
            }
            expect(
              find.textContaining('$clientVersion+$clientBuild'),
              findsNothing,
            );
            expect(tester.takeException(), isNull);
          } finally {
            await tester.pumpWidget(const SizedBox.shrink());
            debugDefaultTargetPlatformOverride = null;
          }
        },
      );
    }
  }
}
