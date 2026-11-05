import 'dart:io';

import 'package:bluegrass_client/update_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  const candidate =
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
  final children = <Process>[];
  final clients = <UpdateClient>[];
  final dart = File(Platform.resolvedExecutable).parent.uri
      .resolve(
        '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
      )
      .toFilePath();
  UpdateClient make(String scenario, {Duration? timeout}) {
    final c = UpdateClient.withProcessStarter(
      (executable, arguments) async {
        expect(
          File(executable).parent.path,
          File(Platform.resolvedExecutable).parent.path,
        );
        expect(executable, endsWith('BlueGrass.Update.exe'));
        expect(arguments, ['--rpc']);
        final child = await Process.start(dart, [
          File('test/utils/update_process_fixture.dart').absolute.path,
          scenario,
        ], runInShell: false);
        children.add(child);
        return child;
      },
      checkTimeout: timeout ?? const Duration(seconds: 5),
      startupPolicyTimeout: timeout ?? const Duration(seconds: 5),
    );
    clients.add(c);
    return c;
  }

  Matcher fails(String code) =>
      throwsA(isA<UpdateException>().having((e) => e.code, 'code', code));
  tearDown(() async {
    for (final c in clients) {
      await c.dispose();
    }
    for (final p in children) {
      p.kill();
      await p.exitCode;
    }
    clients.clear();
    children.clear();
  });
  test('unconfigured is explicit and sends no release config', () async {
    await expectLater(
      make('unconfigured').check(),
      fails('UPDATE_UNCONFIGURED'),
    );
  });
  test(
    'offline startup exposes expired restriction without candidate authority',
    () async {
      final p = await make('startup-expired').readStartupPolicy();
      expect(p.state, UpdateStartupPolicyState.verified);
      expect(p.minimumVersion, '1.1.0');
      expect(p.unsupported, isTrue);
      expect(p.manifestExpired, isTrue);
    },
  );
  test(
    'startup no history is distinct from corrupt state or unconfigured',
    () async {
      final p = await make('startup-absent').readStartupPolicy();
      expect(p.state, UpdateStartupPolicyState.noHistory);
      expect(p.minimumVersion, isEmpty);
      expect(p.unsupported, isFalse);
      expect(p.manifestExpired, isFalse);
      await expectLater(
        make('startup-corrupt').readStartupPolicy(),
        fails('UPDATE_STATE_INVALID'),
      );
      await expectLater(
        make('unconfigured').readStartupPolicy(),
        fails('UPDATE_UNCONFIGURED'),
      );
    },
  );
  for (final scenario in [
    'startup-mixed',
    'startup-candidate',
    'startup-bad-absent',
    'startup-progress',
    'startup-no-expiry',
    'startup-missing-minimum',
    'startup-null-minimum',
    'startup-error-policy',
  ]) {
    test('startup rejects $scenario', () async {
      await expectLater(
        make(scenario).readStartupPolicy(),
        fails('UPDATE_PROTOCOL_ERROR'),
      );
    });
  }
  test('ordinary check rejects startup policy response', () async {
    await expectLater(
      make('startup-expired').check(),
      fails('UPDATE_PROTOCOL_ERROR'),
    );
  });
  test('offline startup timeout remains explicit', () async {
    await expectLater(
      make(
        'hang',
        timeout: const Duration(milliseconds: 250),
      ).readStartupPolicy(),
      fails('UPDATE_TIMEOUT'),
    );
  });
  test('offline startup shares busy and cancellation boundary', () async {
    final c = make('hang');
    final pending = expectLater(
      c.readStartupPolicy(),
      fails('UPDATE_CANCELLED'),
    );
    await expectLater(c.check(), fails('UPDATE_BUSY'));
    c.cancel();
    await pending;
    expect(c.busy, isFalse);
  });
  test(
    'check and scoped download expose typed candidate and progress',
    () async {
      final c = make('normal');
      final checked = await c.check();
      expect(checked.candidateId, candidate);
      expect(checked.state, UpdateState.available);
      final events = <UpdateSnapshot>[];
      final sub = c.progress.listen(events.add);
      await expectLater(c.download(candidate), fails('UPDATE_WAITING_NETWORK'));
      final ready = await c.download(candidate, allowMetered: true);
      expect(ready.state, UpdateState.ready);
      expect(ready.remaining, 0);
      expect(events.single.downloaded, 3);
      await sub.cancel();
    },
  );
  test(
    'launch requires explicit consent and only candidate ID reaches helper',
    () async {
      final c = make('normal');
      await expectLater(
        c.launch(candidate, installConsent: false),
        fails('UPDATE_INSTALL_CONSENT_REQUIRED'),
      );
      expect(children, isEmpty);
      expect(
        (await c.launch(candidate, installConsent: true)).state,
        UpdateState.launchRequested,
      );
    },
  );
  test('invalid candidate paths never start a child', () async {
    await expectLater(
      make('normal').download('../setup.exe', allowMetered: true),
      fails('UPDATE_CANDIDATE_INVALID'),
    );
    expect(children, isEmpty);
  });
  for (final scenario in [
    'wrong-id',
    'duplicate-final',
    'oversized',
    'wrong-candidate',
  ]) {
    test('rejects $scenario response', () async {
      await expectLater(
        make(scenario).download(candidate, allowMetered: true),
        fails('UPDATE_PROTOCOL_ERROR'),
      );
    });
  }
  test('bounded timeout only kills the private helper', () async {
    await expectLater(
      make('hang', timeout: const Duration(milliseconds: 250)).check(),
      fails('UPDATE_TIMEOUT'),
    );
  });
}
