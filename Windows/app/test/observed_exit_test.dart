import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class ObservationSource extends FixtureDataSource {
  String? metadataExit;
  @override
  List<NodeRecord> get nodes => [
    for (final node in super.nodes)
      NodeRecord(
        key: node.key,
        name: node.name,
        protocol: node.protocol,
        host: node.host,
        port: node.port,
        expiresAtUtc: node.expiresAtUtc,
        expectedExitIpv4: metadataExit,
      ),
  ];
}

void main() {
  for (final scenario in [
    'unknown-exit-system-proxy',
    'empty-exit-system-proxy',
    'invalid-exit-system-proxy',
    'pending-exit-system-proxy',
    'changed-exit-system-proxy',
    'diagnostic-change-system-proxy',
    'probe-failure-system-proxy',
    'probe-failure-tun',
    'stale-observation-system-proxy',
  ]) {
    test('connection observation is advisory and context bound: $scenario', () async {
      final source = ObservationSource();
      // Legacy metadata must neither select a destination nor gate observations.
      if (scenario.startsWith('changed-exit')) source.metadataExit = '4.2.2.2';
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      late NativeClient native;
      final c = AppController(
        source: source,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => Preferences(
          mode: scenario.endsWith('tun') ? 'tun' : 'system_proxy',
        ),
        savePreferences: (_) async {},
        nativeFactory: () => native = NativeClient(
          startProcess: (_, _) => Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            scenario,
          ]),
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        expect(c.nodes.length, 3);
        expect(c.selected?.key, source.nodes.first.key);
        await c.connect();
        expect(c.connected, isTrue);
        expect(
          c.actualExitIp,
          scenario.startsWith('changed-exit') ||
                  scenario.startsWith('diagnostic-change')
              ? '8.8.8.8'
              : scenario.startsWith('stale-observation')
              ? '1.1.1.1'
              : null,
        );
        if (scenario.startsWith('stale-observation')) {
          await Future<void>.delayed(const Duration(milliseconds: 1250));
          expect(c.connected, isFalse);
          expect(c.actualExitIp, isNull);
          expect(native.available, isFalse);
          return;
        }
        final sent = (await native.status())['snapshot']['lastProfile'] as Map;
        expect(sent.containsKey('expectedExitIpv4'), isFalse);
        if (scenario.startsWith('pending-exit') ||
            scenario.startsWith('changed-exit')) {
          await Future<void>.delayed(const Duration(milliseconds: 1250));
          expect(c.actualExitIp, '9.9.9.9');
        } else if (scenario.startsWith('diagnostic-change') ||
            scenario.startsWith('probe-failure')) {
          await c.diagnose();
          expect(
            c.actualExitIp,
            scenario.startsWith('diagnostic-change') ? '9.9.9.9' : null,
          );
          expect(
            c.diagnostics.where((d) => d.name == '独立出口诊断').single.passed,
            !scenario.startsWith('probe-failure'),
          );
        }
        source.metadataExit = '4.2.2.1';
        await c.refreshActiveResource();
        expect(
          c.connected,
          isTrue,
          reason: 'expected-IP metadata changes cannot pause a valid session',
        );
        final status = (await native.status())['snapshot'] as Map;
        expect(status['commands'], isNot(contains('Pause')));
        expect(status['commands'], isNot(contains('Disconnect')));
        expect(c.localProxyAddress, isNot(endsWith(':10808')));
      } finally {
        await c.disconnect();
        c.dispose();
      }
    });
  }
}
