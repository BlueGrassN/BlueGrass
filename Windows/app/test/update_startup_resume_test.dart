import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_resume_test.dart' show ResumeStore;
import 'update_startup_policy_test.dart' show StartupClient;

void main() {
  for (final scenario in [
    'expired-denial',
    'corrupt-offline',
    'verified-allow',
  ]) {
    test('startup gate preserves protected update resume: $scenario', () async {
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
        installedBuild: 2,
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
      final client = StartupClient();
      if (scenario == 'expired-denial') {
        client.policy = const UpdateStartupPolicy(
          state: UpdateStartupPolicyState.verified,
          unsupported: true,
          minimumVersion: '9.0.0',
          manifestExpired: true,
        );
      } else if (scenario == 'corrupt-offline') {
        client.policyFailure = 'UPDATE_STATE_INVALID';
        client.checkFailure = 'UPDATE_CHECK_FAILED';
      } else {
        client.checked = const UpdateSnapshot(state: UpdateState.upToDate);
      }
      final ui = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => false,
        handoffAfterLaunch: () async => false,
        exitAfterHandoff: () async {},
      );
      void sync() {
        if (ui.verifiedUnsupported != null) {
          c.setUpdateUnsupported(ui.verifiedUnsupported!);
        }
      }

      ui.addListener(sync);
      try {
        await ui.prepareStartup();
        sync();
        await c.initialize(beforeAutomaticConnect: ui.requireStartupDecision);
        final snapshot = (await native.status())['snapshot'] as Map;
        final commands = snapshot['commands'] as List;
        expect(
          c.customer,
          isNotNull,
          reason: 'version gate must preserve restored account',
        );
        expect(
          commands,
          isNot(contains('Disconnect')),
          reason: 'version gate must not release TUN protection',
        );
        if (scenario == 'verified-allow') {
          expect(c.connected, isTrue);
          expect(c.selected?.key, key);
          expect(commands, contains('Connect'));
          expect(store.value, isNull);
        } else {
          expect(c.connection, 'paused');
          expect(commands, isNot(contains('Connect')));
          expect(store.value, isNotNull);
          // A parameterless retry must retain the original gate.
          final protectedChannel = native;
          await c.initialize();
          expect(
            native,
            same(protectedChannel),
            reason: 'retry must retain the authenticated protection channel',
          );
          final retried = (await native.status())['snapshot'] as Map;
          expect(c.connected, isFalse);
          expect(retried['commands'], isNot(contains('Connect')));
        }
      } finally {
        ui.removeListener(sync);
        ui.dispose();
        await client.dispose();
        await c.disconnect();
        c.dispose();
      }
    });
  }
}
