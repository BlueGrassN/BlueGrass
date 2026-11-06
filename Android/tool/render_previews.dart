// Local visual QA with fictional data. Not part of the production entrypoint.
// Run from Android: flutter test tool/render_previews.dart
import 'dart:io';
import 'dart:ui' as ui;

import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import '../test/support/fakes.dart';
import '../test/support/fake_vpn.dart';

void main() {
  testWidgets('render Android development previews with fixture resources', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(412, 892);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await tester.runAsync(() async {
      final font = File('${Platform.environment['WINDIR']}/Fonts/msyh.ttc');
      final loader = FontLoader('Roboto')
        ..addFont(font.readAsBytes().then(ByteData.sublistView));
      await loader.load();
      final icon = File(
        'E:/tools/flutter/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf',
      );
      await (FontLoader(
        'MaterialIcons',
      )..addFont(icon.readAsBytes().then(ByteData.sublistView))).load();
    });
    final store = MemoryStore();
    final vpn = FakeVpnClient();
    vpn.onConnect = (request) async => vpn.current = snapshot(
      generation: vpn.current.generation + 1,
      state: 'connected',
      request: request.requestId,
      resource: request.resourceKey,
    );
    addTearDown(vpn.events.close);
    final controller = AppController(
      source: FakeSource(),
      store: store,
      clock: () => testNow,
      vpnClient: vpn,
    );
    addTearDown(controller.dispose);
    await controller.initialize();
    final capture = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: capture,
        child: BlueGrassApp(controller: controller),
      ),
    );
    await tester.pumpAndSettle();
    Future<void> save(String name) async {
      final boundary =
          capture.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      await tester.runAsync(() async {
        final image = await boundary.toImage(pixelRatio: 2);
        final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
        final file = File('docs/previews/vpn-$name.png');
        await file.parent.create(recursive: true);
        await file.writeAsBytes(bytes!.buffer.asUint8List());
        image.dispose();
      });
    }

    await save('login-light');
    await controller.login('demo', 'FAKE_PASSWORD');
    await tester.pumpAndSettle();
    await controller.connect();
    await tester.pumpAndSettle();
    await save('home-light');
    await tester.tap(find.byKey(const Key('nav-resources')));
    await tester.pumpAndSettle();
    await save('resources-light');
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    await controller.setTheme(ThemeMode.dark);
    await tester.pumpAndSettle();
    await save('settings-dark');
    expect(tester.takeException(), isNull);
    await controller.disconnect();
    await tester.pumpAndSettle();
  });
}
