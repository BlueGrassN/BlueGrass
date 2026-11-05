import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final stale in [false, true]) {
    test('selected TUN diagnostics bind current session: stale=$stale', () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      late NativeClient native;
      final controller = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (_) async {},
        nativeFactory: () => native = NativeClient(
          startProcess: (_, _) => Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            stale ? 'stale-diagnostics-tun' : 'diagnostics-tun',
          ]),
        ),
      );
      try {
        await controller.initialize();
        await controller.login('fixture', 'fixture');
        await controller.connect();
        await controller.diagnose();
        expect(
          controller.connected,
          isTrue,
          reason:
              'skipped UDP/invalid report does not disconnect working traffic',
        );
        if (stale) {
          expect(
            controller.diagnostics.where((d) => d.name == '独立出口诊断'),
            isEmpty,
          );
          expect(
            controller.diagnostics.where((d) => d.name == '原生诊断').single.passed,
            isFalse,
          );
        } else {
          expect(
            controller.diagnostics
                .where((d) => d.name == '独立出口诊断')
                .single
                .passed,
            isTrue,
          );
          expect(
            controller.diagnostics
                .where((d) => d.name == 'UDP nonce 往返')
                .single
                .passed,
            isNull,
          );
        }
        final commands =
            (await native.status())['snapshot']['commands'] as List;
        expect(commands, contains('Diagnose'));
        expect(commands, isNot(contains('Disconnect')));
      } finally {
        await controller.disconnect();
        controller.dispose();
      }
    });
  }
}
