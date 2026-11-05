import 'dart:io';

import 'package:bluegrass_client/desktop_platform.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

import 'api_endpoint_test.dart' show ConfigTransport, config, jsonResponse;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => debugDefaultTargetPlatformOverride = TargetPlatform.macOS);
  tearDown(() => debugDefaultTargetPlatformOverride = null);

  test(
    'Mac exposes administrator TUN through the shared capability policy',
    () {
      expect(DesktopPlatform.current.supportsTun, isTrue);
    },
  );

  test('Mac TUN reaches the adjacent authorized broker', () async {
    var launches = 0;
    final client = NativeClient(
      startProcess: (path, arguments) async {
        launches++;
        expect(path, endsWith('BlueGrass.Native'));
        expect(arguments, ['--tun']);
        throw const ProcessException('fixture', [], 'no process needed');
      },
    );
    await expectLater(
      client.start(mode: 'tun'),
      throwsA(
        isA<NativeClientException>().having(
          (e) => e.code,
          'code',
          'NATIVE_START_FAILED',
        ),
      ),
    );
    expect(launches, 1);
  });

  test(
    'unknown Mac recovery blocks bootstrap and ordinary account traffic',
    () async {
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(
        platform,
        (call) async =>
            call.method == 'readRecord' &&
                (call.arguments as Map)['key'] == 'device_id'
            ? 'b5e5d810-8032-4e02-9f05-a756531f4b88'
            : null,
      );
      final direct = ConfigTransport(jsonResponse({}, status: 503));
      final bootstrap = ConfigTransport(
        jsonResponse(config(2, 'https://api-next.sunip.vip/api/client/v1')),
      );
      final c = AppController.production(
        unprotectedTransport: direct,
        bootstrapTransport: bootstrap,
        nativeFactory: () => NativeClient(
          startProcess: (_, _) async {
            throw const ProcessException(
              'fixture',
              [],
              'authorization unavailable',
            );
          },
        ),
      );
      try {
        await c.initialize();
        expect(c.recoveryRequired, isTrue);
        expect(c.actualMode, 'none');
        expect(bootstrap.requests, isEmpty);
        await expectLater(
          c.source.login(
            username: 'fixture',
            password: 'fixture',
            deviceName: 'test',
            deviceId: 'fixture',
          ),
          throwsA(isA<ApiFailure>()),
        );
        expect(direct.requests, isEmpty);
      } finally {
        c.dispose();
        messenger.setMockMethodCallHandler(platform, null);
      }
    },
  );

  test('production Mac recovery uses protected API resolution without direct fallback', () async {
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    final records = <String, String>{
      'device_id': 'b5e5d810-8032-4e02-9f05-a756531f4b88',
    };
    messenger.setMockMethodCallHandler(platform, (call) async {
      final args = call.arguments as Map?;
      if (call.method == 'readRecord') return records[args!['key']];
      if (call.method == 'writeRecord') records[args!['key']] = args['value'];
      if (call.method == 'deleteRecord') records.remove(args!['key']);
      return null;
    });
    final direct = ConfigTransport(jsonResponse({}, status: 503));
    final bootstrap = ConfigTransport(
      jsonResponse(config(2, 'https://api-next.sunip.vip/api/client/v1')),
    );
    final c = AppController.production(
      unprotectedTransport: direct,
      bootstrapTransport: bootstrap,
      nativeFactory: () => NativeClient(
        startProcess: (_, _) {
          final dart = File(Platform.resolvedExecutable).parent.uri
              .resolve(
                '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
              )
              .toFilePath();
          return Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'mac-recovered-tun',
          ]);
        },
      ),
    );
    try {
      await c.initialize();
      expect(c.actualMode, 'tun');
      expect(c.connection, 'paused');
      expect(bootstrap.requests, isEmpty);
      // The real broker fixture provides no address pin. This must fail at the
      // protected resolver, before account credentials reach any transport.
      await expectLater(
        c.source.login(
          username: 'fixture',
          password: 'fixture',
          deviceName: 'test',
          deviceId: 'fixture',
        ),
        throwsA(isA<ApiFailure>()),
      );
      expect(direct.requests, isEmpty);
      expect(await c.disconnect(), isTrue);
    } finally {
      c.dispose();
      messenger.setMockMethodCallHandler(platform, null);
    }
  });
}
