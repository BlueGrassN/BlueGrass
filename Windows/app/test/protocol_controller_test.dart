import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class TypedSource extends FixtureDataSource {
  bool changed = false;
  @override
  List<NodeRecord> get nodes => [
    for (final node in super.nodes)
      NodeRecord(
        key: node.key,
        name: node.name,
        protocol: 'https',
        host: node.host,
        port: 443,
        expiresAtUtc: node.expiresAtUtc,
        expectedExitIpv4: node.expectedExitIpv4,
        protocolConfig: ProtocolConfig.fromJson({
          'version': 1,
          'type': 'https',
          'tls': {'serverName': 'proxy.example.invalid'},
          'http': {
            'username': 'fixture',
            'password': changed ? 'changed-fixture' : 'original-fixture',
          },
        }),
      ),
  ];
}

void main() {
  test(
    'typed resource reaches native and config-only changes pause forwarding',
    () async {
      final source = TypedSource();
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      late NativeClient native;
      final c = AppController(
        source: source,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
        nativeFactory: () => native = NativeClient(
          startProcess: (_, _) => Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'connected-system-proxy',
          ]),
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        expect(c.connected, isTrue);
        final request =
            (await native.status())['snapshot']['lastProfile'] as Map;
        expect(
          request['protocolConfig'],
          source.nodes.first.protocolConfig!.toJson(),
        );
        expect(request['auth'], {'username': '', 'password': ''});
        source.changed = true;
        await c.refreshActiveResource();
        expect(c.connection, 'paused');
        final commands =
            (await native.status())['snapshot']['commands'] as List;
        expect(commands, isNot(contains('Disconnect')));
      } finally {
        await c.disconnect();
        c.dispose();
      }
    },
  );
}
