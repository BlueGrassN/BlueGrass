import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final scenario in [
    'switch-prepare-retry-tun',
    'first-prepare-retry-tun',
  ]) {
    test('accepted failed attempt retains current resource identity: $scenario', () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final clients = <NativeClient>[];
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (_) async {},
        nativeFactory: () {
          final client = NativeClient(
            startProcess: (_, _) => Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              scenario,
            ]),
          );
          clients.add(client);
          return client;
        },
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        expect(c.connected, scenario == 'switch-prepare-retry-tun');
        final client = clients.last;
        final next = c.nodes[1];
        await c.select(next);
        expect(c.canRetryProtected, isTrue);
        await c.retryProtectedConnection();
        expect(c.connected, isTrue);
        expect(identical(clients.last, client), isTrue);
        final snapshot = (await client.status())['snapshot'] as Map;
        final commands = (snapshot['commands'] as List)
            .where((v) => v == 'Connect' || v == 'SwitchResource')
            .toList();
        expect(
          commands,
          scenario == 'switch-prepare-retry-tun'
              ? ['Connect', 'SwitchResource', 'Connect']
              : ['Connect', 'SwitchResource'],
        );
        expect(snapshot['context']['resourceKey'], next.key.toJson());
        expect(snapshot['commands'], isNot(contains('Disconnect')));
      } finally {
        await c.disconnect();
        c.dispose();
      }
    });
  }
  test(
    'TUN resource selection pauses and switches without releasing protection',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final clients = <NativeClient>[];
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (_) async {},
        nativeFactory: () {
          final n = NativeClient(
            startProcess: (_, _) => Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              'connected-tun',
            ]),
          );
          clients.add(n);
          return n;
        },
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        final before = clients.last;
        final next = c.nodes[1];
        await c.select(next);
        expect(c.connection, 'paused');
        expect(c.selected?.key, next.key);
        expect(c.canRetryProtected, isTrue);
        var snapshot = (await before.status())['snapshot'] as Map;
        expect(snapshot['protectionIntent'], isTrue);
        expect(snapshot['commands'], isNot(contains('Disconnect')));
        await c.retryProtectedConnection();
        expect(c.connected, isTrue);
        expect(identical(clients.last, before), isTrue);
        snapshot = (await before.status())['snapshot'] as Map;
        expect(snapshot['context']['resourceKey'], next.key.toJson());
        expect(snapshot['commands'], contains('SwitchResource'));
        expect(snapshot['commands'], isNot(contains('Disconnect')));
      } finally {
        await c.disconnect();
        c.dispose();
      }
    },
  );
}
