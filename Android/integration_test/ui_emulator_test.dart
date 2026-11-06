import 'package:client/app.dart';
import 'package:client/app_controller.dart';
import 'package:client/platform/vpn_client.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import '../test/support/fakes.dart';

/// Runs the real Flutter UI and Android VPN channels. Account/resources are
/// explicit fixtures; the .invalid entry deliberately exercises failed startup.
void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets('mobile pages and real Android VPN consent lifecycle', (
    tester,
  ) async {
    final source = FakeSource();
    final vpn = MethodChannelVpnClient();
    final controller = AppController(
      source: source,
      store: MemoryStore(),
      vpnClient: vpn,
      clock: () => testNow,
    );
    addTearDown(() async {
      try {
        await vpn.disconnect().timeout(const Duration(seconds: 10));
      } finally {
        controller.dispose();
      }
    });
    Future<void> until(bool Function() condition, {int seconds = 45}) async {
      final deadline = DateTime.now().add(Duration(seconds: seconds));
      while (!condition() && DateTime.now().isBefore(deadline)) {
        await tester.pump(const Duration(milliseconds: 150));
      }
      expect(
        condition(),
        isTrue,
        reason: 'Timed out waiting for native/UI state',
      );
    }

    Future<void> capture(String name) async {
      await tester.pumpAndSettle();
      // Android IME/window metrics and the image surface settle outside
      // Flutter's animation scheduler; do not capture a keyboard transition.
      await Future<void>.delayed(const Duration(milliseconds: 500));
      await until(
        () =>
            tester.view.viewInsets.bottom == 0 &&
            !controller.authBusy &&
            !controller.vpnBusy &&
            !controller.loading,
      );
      await tester.pumpAndSettle();
      await binding.takeScreenshot(name);
    }

    await vpn.disconnect();
    await tester.pumpWidget(BlueGrassApp(controller: controller));
    await controller.initialize();
    await tester.pumpAndSettle();
    await binding.convertFlutterSurfaceToImage();
    await tester.pumpAndSettle();
    expect(find.text('注册账号'), findsOneWidget);
    expect(find.text('找回密码'), findsOneWidget);
    await capture('login-light');

    await tester.enterText(
      find.byKey(const Key('username')),
      'emulator-fixture',
    );
    await tester.enterText(
      find.byKey(const Key('password')),
      'TEST_ONLY_PASSWORD',
    );
    await tester.ensureVisible(find.byKey(const Key('login')));
    await tester.tap(find.byKey(const Key('login')));
    await until(() => controller.customer != null && !controller.loading);
    await capture('home-light');
    expect(controller.vpnState, VpnState.idle);

    await tester.tap(find.byKey(const Key('nav-resources')));
    await tester.pumpAndSettle();
    expect(find.text('香港线路 1'), findsWidgets);
    await capture('resources-light');
    await tester.tap(find.byKey(const Key('nav-settings')));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('深色').last);
    await tester.tap(find.text('深色').last);
    await until(() => controller.themeMode == ThemeMode.dark);
    await capture('settings-dark');
    await tester.tap(find.byKey(const Key('nav-diagnostics')));
    await capture('diagnostics-dark');
    await controller.setTheme(ThemeMode.light);
    await tester.tap(find.byKey(const Key('nav-home')));
    await tester.pumpAndSettle();

    // Host automation clicks the actual Android system dialog by resource ID.
    // It does not grant AppOps or substitute a permission result.
    debugPrint('BG_VPN_PERMISSION_DENY');
    await tester.ensureVisible(find.byKey(const Key('connect')));
    await tester.tap(find.byKey(const Key('connect')));
    await until(
      () => controller.vpnError != null && !controller.vpnBusy,
      seconds: 90,
    );
    expect((await vpn.status()).state, VpnState.idle);
    await capture('vpn-permission-denied');

    debugPrint('BG_VPN_PERMISSION_ALLOW');
    await tester.ensureVisible(find.byKey(const Key('connect')));
    await tester.tap(find.byKey(const Key('connect')));
    await until(() => controller.vpnState == VpnState.paused, seconds: 90);
    final paused = await vpn.status();
    expect(paused.tunActive, isTrue);
    expect(paused.state, VpnState.paused);
    // The entry is intentionally nonexistent. Never label this as forwarding.
    await capture('vpn-start-failed-protected');
    await tester.ensureVisible(find.byKey(const Key('disconnect')));
    await tester.tap(find.byKey(const Key('disconnect')));
    await until(
      () => controller.vpnState == VpnState.idle && !controller.vpnBusy,
    );
    expect((await vpn.status()).tunActive, isFalse);
    expect(controller.canConnect, isTrue);
    await capture('vpn-disconnected');
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}
