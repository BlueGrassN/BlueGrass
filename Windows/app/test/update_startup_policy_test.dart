import 'dart:async';

import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_presentation_test.dart' show FakeUpdateClient, candidate, ready;

class StartupClient extends FakeUpdateClient {
  UpdateStartupPolicy policy = const UpdateStartupPolicy(
    state: UpdateStartupPolicyState.noHistory,
  );
  String? policyFailure;
  Completer<UpdateStartupPolicy>? policyGate;
  Completer<UpdateSnapshot>? checkGate;
  @override
  Future<UpdateStartupPolicy> readStartupPolicy() async {
    calls.add('startup_policy');
    if (policyFailure != null) throw UpdateException(policyFailure!);
    return policyGate == null ? policy : policyGate!.future;
  }

  @override
  Future<UpdateSnapshot> check() async {
    calls.add('check');
    if (checkFailure != null) throw UpdateException(checkFailure!);
    return checkGate == null ? checked : checkGate!.future;
  }

  @override
  void cancel() {
    super.cancel();
    if (policyGate != null && !policyGate!.isCompleted) {
      policyGate!.completeError(const UpdateException('UPDATE_CANCELLED'));
    }
    if (checkGate != null && !checkGate!.isCompleted) {
      checkGate!.completeError(const UpdateException('UPDATE_CANCELLED'));
    }
  }
}

void main() {
  late StartupClient client;
  late UpdatePresentation ui;
  UpdatePresentation create() => UpdatePresentation(
    client: client,
    pauseBeforeInstall: () async => throw StateError('unexpected install'),
    handoffAfterLaunch: () async => throw StateError('unexpected handoff'),
    exitAfterHandoff: () async => throw StateError('unexpected exit'),
  );
  setUp(() {
    client = StartupClient();
    ui = create();
  });
  tearDown(() async {
    ui.dispose();
    await client.dispose();
  });
  Matcher failure(String code) =>
      throwsA(isA<UpdateException>().having((e) => e.code, 'code', code));

  test('restart re-reads signed expired denial without granting a package candidate', () async {
    client.policy = const UpdateStartupPolicy(
      state: UpdateStartupPolicyState.verified,
      unsupported: true,
      minimumVersion: '9.0.0',
      manifestExpired: true,
    );
    await ui.prepareStartup();
    await expectLater(ui.requireStartupDecision(), failure('UPDATE_REQUIRED'));
    expect(ui.verifiedUnsupported, isTrue);
    expect(ui.snapshot, isNull);
    expect(ui.canInstall, isFalse);
    expect(ui.canDownload, isFalse);
    expect(ui.statusText, contains('9.0.0'));
    ui.dispose();
    ui = create();
    await ui.prepareStartup();
    await expectLater(ui.requireStartupDecision(), failure('UPDATE_REQUIRED'));
    expect(client.calls, ['startup_policy', 'startup_policy']);
    client.checkFailure = 'UPDATE_TIMEOUT';
    await ui.check();
    expect(ui.verifiedUnsupported, isTrue);
    await expectLater(ui.requireStartupDecision(), failure('UPDATE_REQUIRED'));
  });

  test('invalid signed storage is unknown, never an implicit permission after failed refresh', () async {
    client.policyFailure = 'UPDATE_STATE_INVALID';
    client.checkFailure = 'UPDATE_CHECK_FAILED';
    await ui.prepareStartup();
    expect(ui.verifiedUnsupported, isNull);
    await expectLater(
      ui.requireStartupDecision(),
      failure('UPDATE_STARTUP_UNCONFIRMED'),
    );
    expect(ui.statusText, contains('无法核验'));
    client.checkFailure = null;
    client.checked = const UpdateSnapshot(
      state: UpdateState.upToDate,
      minimumVersion: '0.1.0',
    );
    await ui.check();
    await ui.requireStartupDecision();
    expect(ui.verifiedUnsupported, isFalse);
  });

  test('policy absence plus offline check preserves ordinary use without claiming verified support', () async {
    client.checkFailure = 'UPDATE_CHECK_FAILED';
    await ui.prepareStartup();
    await ui.requireStartupDecision();
    expect(ui.verifiedUnsupported, isNull);
    expect(ui.snapshot, isNull);
  });

  test('unconfigured policy finishes without a network request or fabricated support', () async {
    client.policyFailure = 'UPDATE_UNCONFIGURED';
    await ui.prepareStartup();
    await ui.requireStartupDecision();
    await ui.startBackground();
    expect(client.calls, ['startup_policy']);
    expect(ui.verifiedUnsupported, isNull);
    expect(ui.statusText, contains('未配置'));
  });

  test('eligible package transfer starts only after startup decision, without duplicate check or install', () async {
    await Future.wait([ui.prepareStartup(), ui.prepareStartup()]);
    expect(client.calls, ['startup_policy', 'check']);
    expect(ui.snapshot?.state, UpdateState.available);
    await ui.requireStartupDecision();
    await ui.startBackground();
    expect(client.calls, [
      'startup_policy',
      'check',
      'download:$candidate:false',
    ]);
    expect(ui.snapshot, ready);
  });

  testWidgets(
    'stalled check is bounded, cancels helper, and releases startup only after confirmed absence',
    (tester) async {
      client.checkGate = Completer<UpdateSnapshot>();
      var finished = false;
      final startup = ui.prepareStartup().then((_) => finished = true);
      await tester.pump();
      expect(finished, isFalse);
      await tester.pump(const Duration(seconds: 13));
      await startup;
      await ui.requireStartupDecision();
      expect(finished, isTrue);
      expect(ui.errorCode, 'UPDATE_TIMEOUT');
      expect(client.calls, ['startup_policy', 'check', 'cancel']);
      expect(ui.snapshot, isNull);
    },
  );

  testWidgets(
    'stalled offline policy plus failed refresh remains unconfirmed',
    (tester) async {
      client.policyGate = Completer<UpdateStartupPolicy>();
      client.checkFailure = 'UPDATE_CHECK_FAILED';
      final startup = ui.prepareStartup();
      await tester.pump();
      await tester.pump(const Duration(seconds: 11));
      await startup;
      await expectLater(
        ui.requireStartupDecision(),
        failure('UPDATE_STARTUP_UNCONFIRMED'),
      );
      expect(client.calls, ['startup_policy', 'cancel', 'check']);
    },
  );

  testWidgets('suspending startup cannot reuse partial no-history permission', (
    tester,
  ) async {
    client.checkGate = Completer<UpdateSnapshot>();
    final startup = ui.prepareStartup();
    await tester.pump();
    ui.stopBackground();
    await startup;
    await expectLater(
      ui.requireStartupDecision(),
      failure('UPDATE_STARTUP_UNCONFIRMED'),
    );
    expect(ui.verifiedUnsupported, isNull);
  });
}
