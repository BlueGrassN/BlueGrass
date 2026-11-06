import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final state in ['connecting', 'connected', 'stopping', 'busy']) {
    for (final change in ['mode', 'routingMode']) {
      test('$state rejects $change changes before persisting', () async {
        final saved = <Preferences>[];
        final c = AppController(
          source: FixtureDataSource(),
          demo: true,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(),
          savePreferences: (value) async => saved.add(value),
        );
        try {
          await c.initialize();
          c.connection = state == 'busy' ? 'idle' : state;
          c.busy = state == 'busy';
          final original = c.preferences;
          final requested = Preferences.fromJson({
            ...original.toJson(),
            change: change == 'mode' ? 'tun' : 'global',
          });
          await c.updatePreferences(requested);
          expect(c.preferences, same(original));
          expect(saved, isEmpty);
          expect(c.connection, state == 'busy' ? 'idle' : state);
          expect(c.message, contains(state == 'busy' ? '稍候' : '断开'));

          // Unrelated preferences remain editable without reconnecting.
          await c.updatePreferences(original.copyWith(theme: ThemeMode.dark));
          expect(saved.single.theme, ThemeMode.dark);
          c.busy = false;
          await c.disconnect();
          await c.updatePreferences(requested);
          expect(c.preferences, same(requested));
          expect(c.connection, 'idle');
        } finally {
          c.dispose();
        }
      });
    }
  }

  for (final mode in ['system_proxy', 'tun']) {
    for (final routingMode in ['global', 'bypass_cn']) {
      test('$mode sends $routingMode and retains it until disconnect', () async {
        final dart = File(Platform.resolvedExecutable).parent.uri
            .resolve(
              '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
            )
            .toFilePath();
        late NativeClient native;
        final saved = <Preferences>[];
        final c = AppController(
          source: FixtureDataSource(),
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async =>
              Preferences.fromJson({'mode': mode, 'routingMode': routingMode}),
          savePreferences: (value) async => saved.add(value),
          nativeFactory: () => native = NativeClient(
            startProcess: (_, _) => Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              mode == 'tun' ? 'connected-tun' : 'connected-system-proxy',
            ]),
          ),
        );
        try {
          await c.initialize();
          await c.login('fixture', 'fixture');
          await c.connect();
          expect(c.connected, isTrue);
          final before = (await native.status())['snapshot'] as Map;
          expect(before['lastProfile']['mode'], mode);
          expect(before['lastProfile']['routingMode'], routingMode);

          // Paused forwarding still owns a native session and must stay locked.
          c.connection = 'paused';
          final original = c.preferences;
          for (final change in [
            {'mode': mode == 'tun' ? 'system_proxy' : 'tun'},
            {'routingMode': routingMode == 'global' ? 'bypass_cn' : 'global'},
          ]) {
            await c.updatePreferences(
              Preferences.fromJson({...original.toJson(), ...change}),
            );
            expect(c.preferences, same(original));
          }
          expect(saved, isEmpty);
          final after = (await native.status())['snapshot'] as Map;
          expect(after['lastProfile'], before['lastProfile']);
          final commands = after['commands'] as List;
          expect(commands.where((value) => value == 'Connect').length, 1);
          expect(commands, isNot(contains('Disconnect')));
          expect(commands, isNot(contains('SwitchResource')));
          expect(await c.disconnect(), isTrue);
          await c.updatePreferences(
            Preferences.fromJson({
              ...original.toJson(),
              'routingMode': routingMode == 'global' ? 'bypass_cn' : 'global',
            }),
          );
          expect(saved, hasLength(1));
          expect(c.connection, 'idle');
        } finally {
          await c.disconnect();
          c.dispose();
        }
      });
    }
  }
}
