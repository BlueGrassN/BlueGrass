import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class ChangingSource extends FixtureDataSource {
  String? failure;
  @override
  Future<NodeRecord> getNode(String id) {
    if (failure != null) {
      throw ApiFailure(
        failure!,
        status: failure == 'node_unavailable' ? 404 : 503,
      );
    }
    return super.getNode(id);
  }
}

void main() {
  for (final unavailable in [true, false]) {
    test(
      'authoritative resource refresh unavailable=$unavailable retains owned network state',
      () async {
        final source = ChangingSource();
        final dart = File(Platform.resolvedExecutable).parent.uri
            .resolve(
              '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
            )
            .toFilePath();
        final c = AppController(
          source: source,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(),
          savePreferences: (_) async {},
          nativeFactory: () => NativeClient(
            startProcess: (_, _) => Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              'system-proxy',
            ]),
          ),
        );
        try {
          await c.initialize();
          await c.login('fixture', 'fixture');
          await c.connect();
          expect(c.connected, isTrue);
          source.failure = unavailable ? 'node_unavailable' : 'network_error';
          await c.refreshActiveResource();
          expect(c.connected, !unavailable);
          expect(
            c.active,
            isTrue,
            reason: 'automatic pause keeps recovery/ownership until explicit disconnect',
          );
          if (unavailable) expect(c.connection, 'paused');
          expect(await c.disconnect(), isTrue);
        } finally {
          await c.disconnect();
          c.dispose();
        }
      },
    );
  }
}
