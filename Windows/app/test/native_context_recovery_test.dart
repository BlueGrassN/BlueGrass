import 'dart:io';

import 'package:bluegrass_client/native_client.dart';
import 'package:flutter_test/flutter_test.dart';

Map<String, dynamic> recoveryProfile(
  String id, {
  String account = 'account-1',
}) => {
  'key': {
    'environment': 'test',
    'dataSource': 'client-api',
    'accountKey': account,
    'kind': 'subscription',
    'sourceId': id,
  },
  'protocol': 'socks5',
  'entry': {'host': '8.8.8.8', 'port': 443},
  'auth': {'username': '', 'password': ''},
  'validUntilUtc': null,
  'configVersion': 1,
  'configFingerprint': 'fixture',
  'mode': 'tun',
};

void main() {
  final children = <Process>[];
  final clients = <NativeClient>[];
  final dart = File(Platform.resolvedExecutable).parent.uri
      .resolve(
        '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
      )
      .toFilePath();
  final fixture = File('test/utils/native_context_recovery_fixture.dart')
      .absolute
      .path;

  NativeClient client(String scenario) {
    final value = NativeClient(
      requestTimeout: const Duration(seconds: 8),
      shutdownTimeout: const Duration(seconds: 2),
      startProcess: (_, arguments) async {
        expect(arguments, ['--tun']);
        final child = await Process.start(dart, [
          fixture,
          scenario,
        ], runInShell: false);
        children.add(child);
        return child;
      },
    );
    clients.add(value);
    return value;
  }

  tearDown(() async {
    for (final native in clients) {
      try {
        await native.stop();
      } catch (_) {
        /* Expected fatal cases. */
      }
    }
    for (final child in children) {
      child.kill();
      await child.exitCode;
    }
    clients.clear();
    children.clear();
  });

  Matcher failsWith(String code) =>
      throwsA(isA<NativeClientException>().having((e) => e.code, 'code', code));

  Future<Map<String, dynamic>> bind(NativeClient native) async {
    await native.start(mode: 'tun');
    return native.request('Connect', profile: recoveryProfile('resource-A'));
  }

  test(
    'committed switch recovery clears binding without releasing protection',
    () async {
      final native = client('valid');
      final original = await bind(native);
      final originalContext = original['snapshot']['context'];
      final reply = await native.request(
        'SwitchResource',
        profile: recoveryProfile('resource-B'),
      );
      expect(reply['ok'], isFalse);
      expect(reply['snapshot']['context'], isNull);
      expect(
        reply['snapshot']['contextRecovery']['previousContext'],
        originalContext,
      );
      expect(native.available, isTrue);
      final paused = (await native.status())['snapshot'];
      expect(paused['connection'], 'paused');
      expect(paused['protectionIntent'], isTrue);
      expect(paused['businessGateOpen'], isFalse);
      await expectLater(
        native.request('Diagnose'),
        failsWith('NATIVE_INVALID_REQUEST'),
      );
      await expectLater(
        native.request(
          'SwitchResource',
          profile: recoveryProfile('resource-C'),
        ),
        failsWith('NATIVE_INVALID_REQUEST'),
      );
      final resumed = await native.request(
        'Connect',
        profile: recoveryProfile('resource-B'),
      );
      expect(resumed['ok'], isTrue);
      expect(
        resumed['snapshot']['context']['resourceKey']['sourceId'],
        'resource-B',
      );
      expect(
        resumed['snapshot']['context']['logicalSessionId'],
        'committed-session',
      );
      final diagnosis = await native.request('Diagnose');
      expect(
        diagnosis['snapshot']['receivedContext'],
        resumed['snapshot']['context'],
      );
      expect(diagnosis['snapshot']['commands'], isNot(contains('Disconnect')));
    },
  );

  test(
    'uncommitted switch rejection retains the exact original binding',
    () async {
      final native = client('before-commit');
      final original = await bind(native);
      final reply = await native.request(
        'SwitchResource',
        profile: recoveryProfile('resource-B'),
      );
      expect(reply['ok'], isFalse);
      expect(reply['snapshot']['context'], original['snapshot']['context']);
      final diagnosis = await native.request('Diagnose');
      expect(
        diagnosis['snapshot']['receivedContext'],
        original['snapshot']['context'],
      );
      expect(native.available, isTrue);
    },
  );

  for (final defect in [
    'missing-marker',
    'null-marker',
    'extra-marker-field',
    'missing-marker-field',
    'wrong-version',
    'float-version',
    'wrong-reason',
    'wrong-request-id',
    'wrong-runtime',
    'wrong-envelope-runtime',
    'wrong-client',
    'wrong-session',
    'wrong-attempt',
    'wrong-old-owner',
    'wrong-old-resource',
    'wrong-target',
    'cross-owner',
    'cross-environment',
    'cross-source',
    'same-resource',
    'context-present',
    'context-null',
    'context-malformed',
    'gate-open',
    'gate-unknown',
    'not-paused',
    'not-tun',
    'no-protection',
    'wrong-protection',
    'proxy-applied',
    'proxy-recovery',
    'missing-proxy-recovery',
    'success',
    'event',
    'on-status',
  ]) {
    test('unscoped context recovery is fatal: $defect', () async {
      final native = client(defect);
      await bind(native);
      final destination = recoveryProfile(
        defect == 'same-resource' ? 'resource-A' : 'resource-B',
        account: defect == 'cross-owner' ? 'other-account' : 'account-1',
      );
      if (defect == 'cross-environment') {
        destination['key']['environment'] = 'other';
      }
      if (defect == 'cross-source') {
        destination['key']['dataSource'] = 'other';
      }
      final request = defect == 'on-status'
          ? native.status()
          : native.request('SwitchResource', profile: destination);
      await expectLater(request, failsWith('NATIVE_CONTEXT_MISMATCH'));
      expect(native.available, isFalse);
    });
  }

  test(
    'a marker cannot retire a context accepted after the request was queued',
    () async {
      final native = client('queued-stale');
      await bind(native);
      final first = native.request(
        'SwitchResource',
        profile: recoveryProfile('resource-B'),
      );
      final queued = native.request(
        'SwitchResource',
        profile: recoveryProfile('resource-C'),
      );
      final rejected = expectLater(
        queued,
        failsWith('NATIVE_CONTEXT_MISMATCH'),
      );
      expect((await first)['ok'], isTrue);
      await rejected;
      expect(native.available, isFalse);
    },
  );

  test(
    'old bound-context events remain invalid after authorized recovery',
    () async {
      final native = client('old-event-after-recovery');
      await bind(native);
      expect(
        (await native.request(
          'SwitchResource',
          profile: recoveryProfile('resource-B'),
        ))['ok'],
        isFalse,
      );
      await expectLater(native.status(), failsWith('NATIVE_CONTEXT_MISMATCH'));
    },
  );

  test(
    'a recovery marker cannot establish the initial Hello identity',
    () async {
      final native = client('on-hello');
      await expectLater(
        native.start(mode: 'tun'),
        failsWith('NATIVE_CONTEXT_MISMATCH'),
      );
    },
  );
}
