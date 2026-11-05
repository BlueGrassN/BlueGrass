import 'dart:io';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/native_client.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class RevokingSource extends FixtureDataSource {
  bool revoked = false;
  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) {
    if (revoked) throw const ApiFailure('session_revoked', status: 401);
    return super.listNodes(cursor: cursor, perPage: perPage);
  }
}

void main() {
  test(
    'failed native startup retains an explicit recovery requirement',
    () async {
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
        nativeFactory: () => NativeClient(
          startProcess: (_, _) async =>
              throw const ProcessException('fixture', [], 'unavailable'),
        ),
      );
      try {
        await c.initialize();
        expect(c.active, isTrue);
        expect(c.connection, 'failed');
        expect(c.recoveryRequired, isTrue);
      } finally {
        c.dispose();
      }
    },
  );

  for (final scenario in ['paused-system-proxy', 'slow-system-proxy']) {
    test('disconnect remains available while forwarding stops: $scenario', () async {
      final children = <Process>[];
      final source = RevokingSource();
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
          startProcess: (_, _) async {
            final child = await Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              scenario,
            ]);
            children.add(child);
            return child;
          },
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        expect(c.connected, isTrue);
        if (scenario == 'paused-system-proxy') {
          await Future<void>.delayed(const Duration(milliseconds: 1300));
          expect(c.connection, 'paused');
        } else {
          source.revoked = true;
          await c.refresh();
          expect(c.customer, isNull);
          expect(
            c.connection,
            'stopping',
            reason: 'auth failure must not publish idle before cleanup',
          );
          await c.login('another', 'account');
          expect(
            c.customer,
            isNull,
            reason: 'new login waits for proxy cleanup',
          );
        }
        expect(c.active, isTrue);
        expect(await c.disconnect(), isTrue);
        expect(c.active, isFalse);
      } finally {
        await c.disconnect();
        c.dispose();
        for (final child in children) {
          child.kill();
          await child.exitCode;
        }
      }
    });
  }

  test('startup recovers native state without requiring an eligible resource', () async {
    var launches = 0;
    final dart = File(Platform.resolvedExecutable).parent.uri
        .resolve(
          '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
        )
        .toFilePath();
    final c = AppController(
      source: FixtureDataSource(),
      getDeviceId: () async => 'fixture-device',
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
      nativeFactory: () => NativeClient(
        startProcess: (_, _) async {
          launches++;
          return Process.start(dart, [
            File('test/utils/native_process_fixture.dart').absolute.path,
            'system-proxy',
          ]);
        },
      ),
    );
    try {
      await c.initialize();
      expect(
        launches,
        1,
        reason:
            'startup must check durable native recovery before declaring idle',
      );
      expect(c.customer, isNull);
      expect(c.active, isFalse);
      expect(await c.disconnect(), isTrue);
      expect(
        launches,
        1,
        reason: 'verified idle must not create redundant processes',
      );
    } finally {
      await c.disconnect();
      c.dispose();
    }
  });

  test(
    'killed child can recover through a fresh host without restarting UI',
    () async {
      final children = <Process>[];
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
        nativeFactory: () => NativeClient(
          startProcess: (_, _) async {
            final child = await Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              'system-proxy',
            ]);
            children.add(child);
            return child;
          },
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        children.last.kill();
        await children.last.exitCode;
        await Future<void>.delayed(const Duration(milliseconds: 50));
        expect(
          c.active,
          isTrue,
          reason: 'failed forwarding still requires proxy release',
        );
        final count = children.length;
        expect(await c.disconnect(), isTrue);
        expect(children.length, count + 1);
        expect(c.active, isFalse);
        await c.connect();
        expect(c.connected, isTrue);
      } finally {
        await c.disconnect();
        c.dispose();
        for (final child in children) {
          child.kill();
          await child.exitCode;
        }
      }
    },
  );

  for (final scenario in ['system-proxy', 'normal']) {
    test('production controller checks actual proxy state: $scenario', () async {
      Process? child;
      final dart = File(Platform.resolvedExecutable).parent.uri
          .resolve(
            '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
          )
          .toFilePath();
      final c = AppController(
        source: FixtureDataSource(),
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
        nativeFactory: () => NativeClient(
          startProcess: (_, _) async {
            return child = await Process.start(dart, [
              File('test/utils/native_process_fixture.dart').absolute.path,
              scenario,
            ]);
          },
        ),
      );
      try {
        await c.initialize();
        await c.login('fixture', 'fixture');
        await c.connect();
        expect(c.connected, scenario == 'system-proxy');
        if (scenario == 'system-proxy') {
          expect(c.connectionLabel, '系统代理已连接');
          expect(c.actualExitIp, '1.1.1.1');
        }
        expect(await c.disconnect(), isTrue);
        expect(c.active, isFalse);
      } finally {
        await c.disconnect();
        c.dispose();
        child?.kill();
        await child?.exitCode;
      }
    });
  }
}
