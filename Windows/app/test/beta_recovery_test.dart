import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class RevokedSource extends FixtureDataSource {
  bool revoked = false;
  ApiFailure? detailFailure;
  int detailReads = 0;
  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) async {
    if (revoked) throw const ApiFailure('session_revoked', status: 401);
    return super.listNodes(cursor: cursor, perPage: perPage);
  }

  @override
  Future<NodeRecord> getNode(String id) {
    detailReads++;
    if (detailFailure != null) return Future.error(detailFailure!);
    return super.getNode(id);
  }
}

void main() {
  for (final diagnostic in [false, true]) {
    for (final code in [
      'session_revoked',
      'not_authenticated',
      'stale_refresh',
    ]) {
      test('invalid session $code from ${diagnostic ? 'diagnosis' : 'detail'} pauses and clears login', () async {
        final source = RevokedSource();
        final clients = <NativeClient>[];
        final dart = File(Platform.resolvedExecutable).parent.uri
            .resolve(
              '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
            )
            .toFilePath();
        final c = AppController(
          source: source,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(mode: 'tun'),
          savePreferences: (_) async {},
          nativeFactory: () {
            final client = NativeClient(
              startProcess: (_, _) => Process.start(dart, [
                File('test/utils/native_process_fixture.dart').absolute.path,
                'connected-tun',
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
          source.detailFailure = ApiFailure(
            code,
            status: code == 'session_revoked' ? 401 : null,
          );
          await (diagnostic ? c.diagnose() : c.refreshActiveResource());
          await Future<void>.delayed(const Duration(milliseconds: 150));
          expect(c.customer, isNull);
          expect(c.connection, 'paused');
          expect(c.canLogin, isTrue);
          final before =
              ((await clients.last.status())['snapshot']['commands'] as List)
                  .length;
          await Future<void>.delayed(const Duration(milliseconds: 2200));
          final snapshot = (await clients.last.status())['snapshot'] as Map;
          expect(
            (snapshot['commands'] as List).length,
            greaterThan(before + 1),
            reason:
                'paused protection must retain its authenticated control pipe',
          );
          expect(snapshot['commands'], isNot(contains('Disconnect')));
        } finally {
          await c.disconnect();
          c.dispose();
        }
      });
    }
  }
  test(
    'failed first TUN prepare adopts protection and retains monitoring',
    () async {
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      late NativeClient client;
      final c = AppController(
        source: RevokedSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(mode: 'tun'),
        savePreferences: (_) async {},
        nativeFactory: () => client = NativeClient(
          startProcess: (_, _) => Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'failed-prepare-tun',
          ]),
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        expect(c.actualMode, 'tun');
        expect(c.connected, isFalse);
        expect(c.connection, 'paused');
        expect(c.canRetryProtected, isTrue);
        final before =
            ((await client.status())['snapshot']['commands'] as List).length;
        await Future<void>.delayed(const Duration(milliseconds: 2200));
        final snapshot = (await client.status())['snapshot'] as Map;
        expect((snapshot['commands'] as List).length, greaterThan(before + 1));
        expect(snapshot['commands'], isNot(contains('Disconnect')));
      } finally {
        await c.disconnect();
        c.dispose();
      }
    },
  );
  for (final change in [false, true]) {
    test(
      change
          ? 'network change revalidates original resource without release'
          : 'terminal auth pauses TUN without releasing protection',
      () async {
        final source = RevokedSource();
        final clients = <NativeClient>[];
        final dart = File(Platform.resolvedExecutable).parent.uri
            .resolve(
              '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
            )
            .toFilePath();
        final c = AppController(
          source: source,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(mode: 'tun'),
          savePreferences: (_) async {},
          nativeFactory: () {
            final native = NativeClient(
              startProcess: (_, _) => Process.start(dart, [
                File('test/utils/native_process_fixture.dart').absolute.path,
                change ? 'network-change-tun' : 'connected-tun',
              ]),
            );
            clients.add(native);
            return native;
          },
        );
        try {
          await c.initialize();
          await c.login('fixture', 'fixture');
          await c.connect();
          expect(c.connected, isTrue);
          final before = source.detailReads;
          final key = c.selected!.key;
          if (change) {
            await Future<void>.delayed(const Duration(seconds: 4));
            expect(c.connected, isTrue);
            expect(c.selected!.key, key);
            expect(source.detailReads, greaterThan(before));
          } else {
            source.revoked = true;
            await c.refresh();
            await Future<void>.delayed(const Duration(milliseconds: 150));
            expect(c.customer, isNull);
            expect(c.connection, 'paused');
            expect(c.active, isTrue);
          }
          final snapshot = (await clients.last.status())['snapshot'] as Map;
          final commands = (snapshot['commands'] as List).cast<String>();
          expect(
            commands.where((c) => c == 'Disconnect'),
            isEmpty,
            reason: 'only explicit user disconnect may release this session',
          );
          expect(snapshot['protectionIntent'], isTrue);
        } finally {
          await c.disconnect();
          c.dispose();
        }
      },
    );
  }
}
