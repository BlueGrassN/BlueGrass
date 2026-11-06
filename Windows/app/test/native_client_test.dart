import 'dart:io';

import 'package:bluegrass_client/native_client.dart';
import 'package:flutter_test/flutter_test.dart';

Matcher failsWith(String code) =>
    throwsA(isA<NativeClientException>().having((e) => e.code, 'code', code));

Map<String, dynamic> profile({String sourceId = 'resource-1'}) => {
  'key': {
    'environment': 'test',
    'dataSource': 'client-api',
    'accountKey': 'account-1',
    'kind': 'subscription',
    'sourceId': sourceId,
  },
  'protocol': 'socks5',
  'entry': {'host': '192.0.2.1', 'port': 23456},
  'auth': {'username': 'fixture-user', 'password': 'fixture-secret'},
  'validUntilUtc': null,
  'configVersion': 1,
  'configFingerprint': 'fixture-profile',
  'mode': 'system_proxy',
};

// Dart fixture startup includes JIT compilation. Arm an induced short deadline
// only once the setup commands have completed, keeping the real IPC intact.
class _FixtureClient extends NativeClient {
  _FixtureClient({required super.startProcess})
    : super(
        requestTimeout: const Duration(seconds: 5),
        shutdownTimeout: const Duration(seconds: 2),
      );

  Duration? commandTimeout;

  @override
  Duration get requestTimeout => commandTimeout ?? super.requestTimeout;
}

void main() {
  final children = <Process>[];
  final clients = <NativeClient>[];
  final dart = File(Platform.resolvedExecutable).parent.uri
      .resolve(
        '../../../dart-sdk/bin/${Platform.isWindows ? 'dart.exe' : 'dart'}',
      )
      .toFilePath();
  final fixture = File('test/utils/native_process_fixture.dart').absolute.path;

  _FixtureClient client(String scenario) {
    final value = _FixtureClient(
      startProcess: (executable, arguments) async {
        // This asserts the production launch boundary; the stand-in implements
        // only the other side of the pipe, never the client under test.
        expect(
          File(executable).parent.path,
          File(Platform.resolvedExecutable).parent.path,
        );
        expect(executable, endsWith('BlueGrass.Native.exe'));
        expect(arguments, ['--system-proxy']);
        final child = await Process.start(dart, [
          fixture,
          scenario,
          // Exercise the startup/command deadline boundary independently of
          // parallel host load, without delaying the actual protocol replies.
          if (scenario == 'timeout' || scenario == 'disconnect-timeout') '1200',
        ], runInShell: false);
        children.add(child);
        return child;
      },
    );
    clients.add(value);
    return value;
  }

  tearDown(() async {
    for (final value in clients) {
      try {
        await value.stop();
      } catch (_) {
        /* Failure is asserted by tests. */
      }
    }
    for (final child in children) {
      // Only PIDs created by this test; never discover or touch other services.
      child.kill();
      await child.exitCode;
    }
    children.clear();
    clients.clear();
  });

  test(
    'diagnosis carries only the already authenticated current context',
    () async {
      final native = client('normal');
      await native.start();
      await expectLater(
        native.request('Diagnose'),
        failsWith('NATIVE_INVALID_REQUEST'),
      );
      final connected = await native.request('Connect', profile: profile());
      final response = await native.request('Diagnose');
      final request = response['snapshot']['received'] as Map;
      expect(request['context'], connected['snapshot']['context']);
      expect(request.keys.toSet(), {'id', 'command', 'context'});
    },
  );

  for (final scenario in ['crash', 'malformed']) {
    test(
      'confirmed child crash signal excludes protocol cleanup: $scenario',
      () async {
        final native = client(scenario);
        final events = <Map<String, dynamic>>[];
        final subscription = native.events.listen(events.add);
        await native.start();
        await expectLater(
          native.status(),
          throwsA(isA<NativeClientException>()),
        );
        await Future<void>.delayed(const Duration(milliseconds: 150));
        expect(
          events.where((e) => e['event'] == 'crash').length,
          scenario == 'crash' ? 1 : 0,
        );
        await subscription.cancel();
      },
    );
  }

  test('private child handshake shares concurrent start and keeps secrets off argv', () async {
    final native = client('normal');
    final hello = await Future.wait([native.start(), native.start()]);
    expect(children, hasLength(1));
    expect(hello.first['runtimeEpoch'], 'fixture-runtime');
    expect(hello.last['capabilities'], [
      'local_http_proxy',
      'socks5',
      'exit_ipv4_verification',
    ]);
    expect(native.available, isTrue);
    final reply = await native.request(
      'Connect',
      profile: profile(),
      mode: 'system_proxy',
      allowLan: true,
    );
    final received = (reply['snapshot'] as Map)['received'] as Map;
    expect(received['command'], 'Connect');
    expect(received['profile']['auth']['password'], 'fixture-secret');
    expect(received['allowLan'], isTrue);
    expect(received['mode'], 'system_proxy');
    expect(
      received['id'],
      matches(
        RegExp(
          r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
        ),
      ),
    );
    await native.stop();
    expect(await children.single.exitCode, 0);
    expect(native.available, isFalse);
    await expectLater(native.status(), failsWith('NATIVE_CLOSED'));
  });

  test(
    'serial requests preserve call order and freeze queued credentials',
    () async {
      final native = client('serial');
      await native.start();
      final events = <Map<String, dynamic>>[];
      final subscription = native.events.listen(events.add);
      addTearDown(subscription.cancel);
      final queuedProfile = profile();
      (queuedProfile['auth'] as Map)['password'] = 'before';
      final first = native.status();
      final second = native.request('Connect', profile: queuedProfile);
      (queuedProfile['auth'] as Map)['password'] = 'after';
      (queuedProfile['key'] as Map)['sourceId'] = 'mutated-after-submit';
      final results = await Future.wait([first, second, native.status()]);
      expect(results.map((r) => r['snapshot']['sequence']), [2, 3, 4]);
      expect(
        results[1]['snapshot']['received']['profile']['auth']['password'],
        'before',
      );
      expect(results.last['snapshot']['commands'], [
        'Hello',
        'GetStatus',
        'Connect',
        'GetStatus',
      ]);
      expect(events.where((e) => e['event'] == 'status'), hasLength(3));
      expect(results.map((r) => r['id']).toSet(), hasLength(3));
    },
  );

  test('fragmented UTF8 JSON and CRLF are decoded as one reply', () async {
    final native = client('fragmented');
    await native.start();
    final reply = await native.request(
      'Connect',
      profile: {...profile(), 'label': '测试🌿'},
    );
    expect(reply['snapshot']['received']['profile']['label'], '测试🌿');
  });

  test(
    'stderr is drained without becoming protocol or exception text',
    () async {
      final native = client('stderr');
      await native.start();
      expect((await native.status())['ok'], isTrue);
    },
  );

  for (final item in <String, String>{
    'crash': 'NATIVE_EXITED',
    'timeout': 'NATIVE_TIMEOUT',
    'oversized': 'NATIVE_MESSAGE_TOO_LARGE',
    'malformed': 'NATIVE_PROTOCOL_ERROR',
    'invalid-utf8': 'NATIVE_PROTOCOL_ERROR',
    'wrong-id': 'NATIVE_PROTOCOL_ERROR',
    'stale-id': 'NATIVE_PROTOCOL_ERROR',
    'duplicate-reply': 'NATIVE_PROTOCOL_ERROR',
    'truncated': 'NATIVE_PROTOCOL_ERROR',
    'missing-snapshot': 'NATIVE_PROTOCOL_ERROR',
  }.entries) {
    test(
      '${item.key} invalidates current and queued requests without success',
      () async {
        final native = client(item.key);
        await native.start();
        if (item.key == 'timeout') {
          native.commandTimeout = const Duration(seconds: 1);
        }
        final results = await Future.wait([
          expectLater(native.status(), failsWith(item.value)),
          expectLater(native.request('Pause'), failsWith(item.value)),
        ]);
        expect(results, hasLength(2));
        expect(native.available, isFalse);
        await expectLater(native.status(), failsWith(item.value));
        await expectLater(native.stop(), failsWith(item.value));
        await children.single.exitCode.timeout(const Duration(seconds: 3));
      },
    );
  }

  test(
    'invalid requests never reach the child or poison the valid session',
    () async {
      final native = client('normal');
      await native.start();
      await expectLater(
        native.request('Connect', profile: {'password': 'x' * 65536}),
        failsWith('NATIVE_MESSAGE_TOO_LARGE'),
      );
      await expectLater(
        native.request('Connect', profile: {'label': '测' * 22000}),
        failsWith('NATIVE_MESSAGE_TOO_LARGE'),
      );
      await expectLater(
        native.request('unsupported'),
        failsWith('NATIVE_INVALID_REQUEST'),
      );
      await expectLater(
        native.request('Connect', profile: {'bad': Object()}),
        failsWith('NATIVE_INVALID_REQUEST'),
      );
      final result = await native.status();
      expect(result['snapshot']['commands'], ['Hello', 'GetStatus']);
      expect(native.available, isTrue);
    },
  );

  test(
    'backend rejection preserves fixed code while transport remains usable',
    () async {
      final native = client('rejected');
      await native.start();
      final result = await native.request('Connect');
      expect(result['ok'], isFalse);
      expect(result['error'], 'INVALID_MODE');
      expect(native.available, isTrue);
    },
  );

  test('untrusted backend error text is replaced with a fixed code', () async {
    final native = client('error-secret');
    await native.start();
    expect((await native.status())['error'], 'NATIVE_REJECTED');
  });

  for (final item in <String, String>{
    'disconnect-rejected': 'OWNERSHIP_LOST',
    'disconnect-open-gate': 'NATIVE_DISCONNECT_UNCONFIRMED',
    'disconnect-applied-proxy': 'NATIVE_DISCONNECT_UNCONFIRMED',
    'disconnect-pending-recovery': 'NATIVE_DISCONNECT_UNCONFIRMED',
    'unknown-gate': 'NATIVE_DISCONNECT_UNCONFIRMED',
    'disconnect-timeout': 'NATIVE_TIMEOUT',
    'bad-exit': 'NATIVE_EXITED',
    'hang-exit': 'NATIVE_SHUTDOWN_TIMEOUT',
  }.entries) {
    test('stop with ${item.key} never reports restoration success', () async {
      final native = client(item.key);
      await native.start();
      await native.request('Connect', profile: profile());
      if (item.key == 'disconnect-timeout') {
        native.commandTimeout = const Duration(seconds: 1);
      }
      await expectLater(
        native.stop(),
        throwsA(
          isA<NativeClientException>()
              .having((e) => e.code, 'code', item.value)
              .having((e) => e.recoveryUnknown, 'recoveryUnknown', isTrue),
        ),
      );
      expect(native.available, isFalse);
    });
  }

  test(
    'stop is shared and rejects new work while draining existing commands',
    () async {
      final native = client('serial');
      await native.start();
      final pending = native.status();
      final stopping = native.stop();
      final sameStop = native.stop();
      await expectLater(native.status(), failsWith('NATIVE_CLOSED'));
      expect((await pending)['ok'], isTrue);
      await Future.wait([stopping, sameStop]);
      expect(await children.single.exitCode, 0);
    },
  );

  test('failed hello cannot expose a ready client', () async {
    final native = client('hello-rejected');
    await expectLater(native.start(), failsWith('OWNER_MISMATCH'));
    expect(native.available, isFalse);
  });

  test('startup failures expose only a fixed error code', () async {
    final native = NativeClient(
      startProcess: (_, _) async => throw const ProcessException(
        'secret-executable',
        ['secret-argument'],
        'secret-detail',
      ),
    );
    clients.add(native);
    await expectLater(native.start(), failsWith('NATIVE_START_FAILED'));
    await expectLater(native.stop(), failsWith('NATIVE_START_FAILED'));
  });

  test('64 KiB response is accepted at the byte boundary', () async {
    final native = client('boundary-size');
    await native.start();
    expect((await native.status())['ok'], isTrue);
    expect(native.available, isTrue);
  });

  test('paused event consumer cannot prevent child shutdown', () async {
    final native = client('normal');
    await native.start();
    final subscription = native.events.listen((_) {})..pause();
    // Resume before tearDown even on failure so the current buggy behavior
    // cannot hang the runner; timeout here still exposes the lifecycle defect.
    try {
      await native.stop().timeout(const Duration(seconds: 2));
    } finally {
      subscription.resume();
      await subscription.cancel();
    }
  });

  test('silent Hello times out and closes only its child', () async {
    final native = client('hello-timeout')
      ..commandTimeout = const Duration(seconds: 1);
    await expectLater(native.start(), failsWith('NATIVE_TIMEOUT'));
    expect(native.available, isFalse);
    await children.single.exitCode.timeout(const Duration(seconds: 3));
  });

  test('stop before start never launches the native host', () async {
    final native = client('normal');
    await native.stop();
    expect(children, isEmpty);
    await expectLater(native.start(), failsWith('NATIVE_CLOSED'));
  });

  test(
    'idle host without a controller acknowledges a safe no-op disconnect',
    () async {
      final native = client('idle-host');
      await native.start();
      await native.stop();
      expect(await children.single.exitCode, 0);
    },
  );

  for (final defect in [
    'missing-top-runtime',
    'missing-runtime',
    'runtime',
    'unexpected-context',
    'mode',
    'open',
    'proxy',
    'unknown',
  ]) {
    test('Hello rejects $defect before binding a runtime', () async {
      final native = client('hello:$defect');
      await expectLater(native.start(), failsWith('NATIVE_CONTEXT_MISMATCH'));
      expect(native.available, isFalse);
      await expectLater(native.stop(), failsWith('NATIVE_CONTEXT_MISMATCH'));
    });
  }

  for (final defect in [
    'runtime',
    'context-runtime',
    'schema',
    'missing-context',
    'empty-client',
    'empty-session',
    'empty-attempt',
    'key-environment',
    'key-dataSource',
    'key-accountKey',
    'key-kind',
    'key-sourceId',
  ]) {
    test('Connect refuses $defect from the applied context', () async {
      final native = client('connect:$defect');
      await native.start();
      await expectLater(
        native.request('Connect', profile: profile()),
        failsWith('NATIVE_CONTEXT_MISMATCH'),
      );
      expect(native.available, isFalse);
      await expectLater(native.stop(), failsWith('NATIVE_CONTEXT_MISMATCH'));
    });
  }

  for (final defect in [
    'runtime',
    'top-runtime',
    'missing-runtime',
    'context-runtime',
    'schema',
    'client',
    'session',
    'attempt',
    'key-accountKey',
    'key-sourceId',
    'missing-context',
  ]) {
    test('GetStatus rejects $defect after a validated Connect', () async {
      final native = client('status:$defect');
      await native.start();
      await native.request('Connect', profile: profile());
      await expectLater(native.status(), failsWith('NATIVE_CONTEXT_MISMATCH'));
      expect(native.available, isFalse);
      await expectLater(native.stop(), failsWith('NATIVE_CONTEXT_MISMATCH'));
    });
  }

  for (final defect in ['runtime', 'attempt', 'key-accountKey']) {
    test('status event with $defect is never published to callers', () async {
      final native = client('event:$defect');
      await native.start();
      await native.request('Connect', profile: profile());
      final received = <Map<String, dynamic>>[];
      final subscription = native.events.listen(received.add);
      addTearDown(subscription.cancel);
      await expectLater(native.status(), failsWith('NATIVE_CONTEXT_MISMATCH'));
      await expectLater(native.stop(), failsWith('NATIVE_CONTEXT_MISMATCH'));
      expect(received.where((e) => e['event'] == 'status'), isEmpty);
      expect(received.where((e) => e['event'] == 'failure'), hasLength(1));
    });
  }

  for (final defect in [
    'runtime',
    'attempt',
    'key-accountKey',
    'missing-context',
  ]) {
    test(
      'stop rejects $defect despite closed gate and successful ack',
      () async {
        final native = client('stop:$defect');
        await native.start();
        await native.request('Connect', profile: profile());
        await expectLater(native.stop(), failsWith('NATIVE_CONTEXT_MISMATCH'));
        expect(native.available, isFalse);
      },
    );
  }

  test(
    'reconnect cannot adopt the preceding attempt in a later status',
    () async {
      final native = client('status:old-attempt');
      await native.start();
      await native.request('Connect', profile: profile());
      await native.request('Connect', profile: profile(sourceId: 'resource-2'));
      await expectLater(native.status(), failsWith('NATIVE_CONTEXT_MISMATCH'));
    },
  );

  test(
    'rejected Connect retains the previous account and resource context',
    () async {
      final native = client('reject-second');
      await native.start();
      final first = await native.request('Connect', profile: profile());
      final other = profile(sourceId: 'resource-2');
      (other['key'] as Map)['accountKey'] = 'account-2';
      final rejected = await native.request('Connect', profile: other);
      expect(rejected['ok'], isFalse);
      expect(rejected['error'], 'INVALID_CONFIGURATION');
      expect(rejected['snapshot']['context'], first['snapshot']['context']);
      expect(
        (await native.status())['snapshot']['context'],
        first['snapshot']['context'],
      );
      await native.stop();
      expect(await children.single.exitCode, 0);
    },
  );

  test(
    'returned contexts cannot mutate the identity bound by the client',
    () async {
      final native = client('normal');
      await native.start();
      final connected = await native.request('Connect', profile: profile());
      connected['snapshot']['context']['attemptId'] = 'caller-mutated';
      connected['snapshot']['context']['resourceKey']['accountKey'] =
          'caller-mutated';
      final current = (await native.status())['snapshot']['context'];
      expect(current['attemptId'], 'attempt-1');
      expect(current['resourceKey']['accountKey'], 'account-1');
      await native.stop();
    },
  );

  test(
    'a new successful Connect replaces context and Disconnect retains it',
    () async {
      final native = client('normal');
      final hello = await native.start();
      expect(hello['snapshot']['context'], isNull);
      await native.request('Connect', profile: profile());
      final second = await native.request(
        'Connect',
        profile: profile(sourceId: 'resource-2'),
      );
      expect(second['snapshot']['context']['attemptId'], 'attempt-2');
      final disconnected = await native.request('Disconnect');
      expect(
        disconnected['snapshot']['context'],
        second['snapshot']['context'],
      );
      expect(disconnected['snapshot']['businessGateOpen'], isFalse);
      expect(
        (await native.status())['snapshot']['context'],
        second['snapshot']['context'],
      );
      await native.stop();
    },
  );
}
