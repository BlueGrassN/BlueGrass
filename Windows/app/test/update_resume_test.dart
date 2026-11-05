import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';

class ResumeStore implements SessionStore {
  Map<String, Object?>? value;
  @override
  Future<Map<String, Object?>?> read() async =>
      value == null ? null : Map.of(value!);
  @override
  Future<void> replace(Map<String, Object?> snapshot) async {
    value = Map.of(snapshot);
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

void main() {
  for (final changedBuild in [false, true]) {
    test(
      'update resume revalidates original resource only after changed build: $changedBuild',
      () async {
        final source = FixtureDataSource();
        await source.login(
          username: 'fixture',
          password: 'fixture',
          deviceId: 'fixture-device',
        );
        final key = source.nodes.last.key;
        final store = ResumeStore()
          ..value = {
            'version': 1,
            'phase': 'handed_off',
            'build': 1,
            'localUser': 'fixture-device',
            'mode': 'tun',
            'key': key.toJson(),
            'logicalSessionId': 'prior-logical-session',
          };
        final dart = File(Platform.resolvedExecutable).parent.uri
            .resolve(
              '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
            )
            .toFilePath();
        late NativeClient native;
        final c = AppController(
          source: source,
          updateResumeStore: store,
          installedBuild: changedBuild ? 2 : 1,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(mode: 'tun'),
          savePreferences: (_) async {},
          nativeFactory: () => native = NativeClient(
            startProcess: (_, _) => Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              'recovered-tun',
            ]),
          ),
        );
        try {
          await c.initialize();
          expect(c.connected, changedBuild);
          if (changedBuild) {
            expect(c.selected?.key, key);
            expect(c.actualExitIp, '1.1.1.1');
            expect(store.value, isNull);
          } else {
            expect(c.connection, 'paused');
            expect(store.value, isNotNull);
          }
          final snapshot = (await native.status())['snapshot'] as Map;
          expect(snapshot['commands'], isNot(contains('Disconnect')));
        } finally {
          await c.disconnect();
          c.dispose();
        }
      },
    );
  }
}
