import 'dart:io';

import 'package:bluegrass_client/main.dart' as shared;
import 'package:bluegrass_mac/main.dart' as launcher;
import 'package:flutter/services.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test(
    'Mac launcher exposes the existing desktop app, with no screen fork',
    () {
      expect(const launcher.MyApp(), isA<shared.MyApp>());
    },
  );

  testWidgets(
    'production Mac entry point shows Keychain login and registers only used engines',
    (tester) async {
      tester.view.physicalSize = const Size(1280, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      try {
        launcher.main();
        await tester.pumpAndSettle();
        expect(find.text('凭据由 macOS 钥匙串加密保存'), findsOneWidget);
        final packages = <String>{};
        await for (final entry in LicenseRegistry.licenses) {
          packages.addAll(entry.packages);
        }
        expect(packages, contains('Xray-core v26.10.17'));
        expect(packages, contains('sing (network utilities)'));
        expect(packages.where((name) => name.contains('Wintun')), isEmpty);
      } finally {
        await tester.pumpWidget(const SizedBox());
        debugDefaultTargetPlatformOverride = null;
      }
    },
  );

  test(
    'Mac bundle supplies canonical branding variants and engine licenses',
    () async {
      for (final key in [
        'assets/branding/wordmark-light.png',
        'assets/branding/wordmark-dark.png',
        'assets/branding/4.0x/wordmark-light.png',
        'assets/branding/4.0x/wordmark-dark.png',
        'assets/LICENSE.Xray-core',
        'assets/LICENSE.sing',
      ]) {
        final bytes = await rootBundle.load(key);
        expect(
          bytes.buffer.asUint8List(bytes.offsetInBytes, bytes.lengthInBytes),
          await File('../../Windows/app/$key').readAsBytes(),
          reason: key,
        );
      }
    },
  );
}
