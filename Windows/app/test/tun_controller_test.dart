import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'startup retains TUN protection and exposes explicit recovery',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final launches = <List<String>>[];
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (_) async {},
        nativeFactory: () => NativeClient(
          startProcess: (_, args) {
            launches.add(args);
            return Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              'recovered-tun',
            ]);
          },
        ),
      );
      try {
        await c.initialize();
        expect(launches, [
          ['--tun'],
        ]);
        expect(c.active, isTrue);
        expect(c.connection, 'paused');
        expect(c.message, contains('保护'));
        expect(await c.disconnect(), isTrue);
        expect(c.active, isFalse);
      } finally {
        await c.disconnect();
        c.dispose();
      }
    },
  );
}
