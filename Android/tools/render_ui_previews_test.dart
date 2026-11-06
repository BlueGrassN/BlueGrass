// Render the real app widgets with synthetic account/native-boundary fixtures.
// flutter test --no-pub tools/render_ui_previews_test.dart
import 'dart:io';
import 'dart:ui' as ui;

import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/traffic.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import '../test/support/fake_vpn.dart';
import '../test/support/fakes.dart';

class _PreviewBinding extends AutomatedTestWidgetsFlutterBinding {
  // Match product painting instead of the hard silhouettes used by unit tests.
  @override
  bool get disableShadows => false;
}

// Synthetic samples for screenshots only; native counters drive the normal app.
class _PreviewTraffic implements TrafficClient {
  _PreviewTraffic(this.vpn);
  final FakeVpnClient vpn;
  int sequence = 0;
  @override
  Future<TrafficSample> sample() async {
    final state = vpn.current;
    sequence++;
    return TrafficSample.fromMap({
      'version': 1,
      'runtimeId': state.runtimeId,
      'requestId': state.requestId,
      'resourceKey': state.resourceKey,
      'accountKey': state.accountKey,
      'generation': state.generation,
      'counterEpoch': 'preview',
      'sequence': sequence,
      'monotonicMillis': sequence * 1000,
      'available': true,
      'complete': true,
      'active': state.state.name == 'connected',
      'uploadBytes': (12 * 1024 * 1024 + sequence * 128 * 1024).toString(),
      'downloadBytes': (86 * 1024 * 1024 + sequence * 2560 * 1024).toString(),
    });
  }
}

void main() {
  _PreviewBinding();
  testWidgets('render mobile UI for visual inspection', (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    // Local preview font only; no font files are bundled into the APK.
    final fontPath =
        Platform.environment['BLUEGRASS_PREVIEW_FONT'] ??
        'C:/Windows/Fonts/msyh.ttc';
    await tester.runAsync(() async {
      final loader = FontLoader('Roboto');
      loader.addFont(
        Future.value(ByteData.sublistView(await File(fontPath).readAsBytes())),
      );
      await loader.load();
      final icons = FontLoader('MaterialIcons');
      icons.addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
      await icons.load();
    });
    final vpn = FakeVpnClient();
    final c = AppController(
      source: FakeSource(),
      store: MemoryStore()..routingMode = 'bypass_cn',
      vpnClient: vpn,
      trafficClient: _PreviewTraffic(vpn),
      clock: () => testNow,
    );
    await c.initialize();
    final boundary = GlobalKey();
    await tester.pumpWidget(
      RepaintBoundary(
        key: boundary,
        child: BlueGrassApp(controller: c),
      ),
    );
    await tester.pumpAndSettle();
    await tester.runAsync(() async {
      await precacheImage(
        const AssetImage('assets/branding/wordmark-light.png'),
        boundary.currentContext!,
      );
      await precacheImage(
        const AssetImage('assets/branding/wordmark-dark.png'),
        boundary.currentContext!,
      );
    });
    await tester.pumpAndSettle();
    Future<void> capture(String name) async {
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull, reason: name);
      final render =
          boundary.currentContext!.findRenderObject()! as RenderRepaintBoundary;
      await tester.runAsync(() async {
        final image = await render.toImage(pixelRatio: 2);
        final bytes = (await image.toByteData(format: ui.ImageByteFormat.png))!;
        final output =
            Platform.environment['BLUEGRASS_PREVIEW_DIR'] ??
            'docs/previews/traffic-20261105';
        final file = File('$output/$name.png');
        await file.parent.create(recursive: true);
        await file.writeAsBytes(bytes.buffer.asUint8List());
        image.dispose();
      });
    }

    await capture('login-light');
    await c.login('demo', 'fixture-password');
    await tester.pumpAndSettle();
    await capture('dashboard-idle-light');
    await c.connect();
    vpn.emit(
      snapshot(
        generation: vpn.current.generation + 1,
        state: 'connected',
        request: vpn.requests.single.requestId,
        observation: 'available',
        ip: '203.0.113.42',
        routingMode: 'bypass_cn',
      ),
    );
    await tester.pumpAndSettle();
    expect(c.observedExitIp, '203.0.113.42');
    await c.traffic.refresh();
    await c.traffic.refresh();
    await capture('dashboard-light');
    await c.setTheme(ThemeMode.dark);
    await capture('dashboard-dark');
    await c.setTheme(ThemeMode.light);
    await tester.tap(find.byKey(const Key('nav-resources')));
    await capture('proxies-light');
    await tester.tap(find.byKey(const Key('nav-settings')));
    await capture('settings-light');
    await c.setTheme(ThemeMode.dark);
    await capture('settings-dark');
    await tester.tap(find.byKey(const Key('nav-diagnostics')));
    await capture('diagnostics-dark');
    await tester.pumpWidget(const SizedBox.shrink());
    c.dispose();
    await vpn.events.close();
  });
}
