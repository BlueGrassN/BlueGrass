import 'dart:async';

import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:bluegrass_client/update_user_state.dart';
import 'package:flutter_test/flutter_test.dart';

import 'update_presentation_test.dart' show FakeUpdateClient, candidate, ready;

class ManualTimer implements Timer {
  ManualTimer(this.delay, this.callback);
  final Duration delay;
  final void Function() callback;
  bool active = true;
  @override
  bool get isActive => active;
  @override
  int get tick => active ? 0 : 1;
  @override
  void cancel() => active = false;
  void fire() {
    if (active) {
      active = false;
      callback();
    }
  }
}

void main() {
  late FakeUpdateClient client;
  late UpdatePresentation ui;
  late MemoryUpdatePauseStore store;
  final timers = <ManualTimer>[];
  UpdatePresentation create() => UpdatePresentation(
    client: client,
    pauseStore: store,
    pauseBeforeInstall: () async => throw StateError('must not pause'),
    handoffAfterLaunch: () async => throw StateError('must not handoff'),
    exitAfterHandoff: () async => throw StateError('must not exit'),
    schedule: (delay, callback) {
      final timer = ManualTimer(delay, callback);
      timers.add(timer);
      return timer;
    },
  );
  setUp(() {
    client = FakeUpdateClient();
    store = MemoryUpdatePauseStore();
    timers.clear();
    ui = create();
  });
  tearDown(() async {
    ui.dispose();
    await client.dispose();
  });
  Future<void> settle() => Future<void>.delayed(Duration.zero);

  test(
    'eligible check starts download without metered consent or install',
    () async {
      await ui.check();
      expect(client.calls, ['check', 'download:$candidate:false']);
      expect(ui.snapshot, ready);
    },
  );
  test('network wait retries boundedly without consent; user pause survives restart', () async {
    client.downloadFailure = 'UPDATE_WAITING_NETWORK';
    await ui.startBackground();
    expect(ui.waitingForNetworkConsent, isTrue);
    for (var i = 0; i < 3; i++) {
      final retry = timers.lastWhere(
        (timer) => timer.isActive && timer.delay < const Duration(hours: 6),
      );
      retry.fire();
      await settle();
    }
    expect(client.calls.where((c) => c.startsWith('download:')), hasLength(4));
    expect(
      timers.where(
        (timer) => timer.isActive && timer.delay < const Duration(hours: 6),
      ),
      isEmpty,
    );
    await ui.cancelDownload();
    ui.dispose();
    ui = create();
    client.downloadFailure = null;
    final before = client.calls.length;
    await ui.startBackground();
    expect(client.calls.skip(before), ['check']);
    expect(ui.userPaused, isTrue);
    await ui.download();
    expect(client.calls.last, 'download:$candidate:false');
    expect(ui.snapshot, ready);
  });
  test('network recovery resumes with no consent and timer does not overlap helper', () async {
    client.downloadFailure = 'UPDATE_WAITING_NETWORK';
    await ui.startBackground();
    client.downloadFailure = null;
    timers
        .lastWhere((t) => t.isActive && t.delay < const Duration(hours: 6))
        .fire();
    await settle();
    expect(ui.snapshot, ready);
    expect(client.calls, [
      'check',
      'download:$candidate:false',
      'download:$candidate:false',
    ]);
    expect(client.calls.any((c) => c.startsWith('launch')), isFalse);
  });
  test(
    'dispose cancels scheduled work and active download; no late notification',
    () async {
      client.downloadResult = Completer<UpdateSnapshot>();
      final checking = ui.startBackground();
      await settle();
      expect(ui.downloading, isTrue);
      ui.dispose();
      await checking;
      expect(client.calls.last, 'cancel');
      expect(timers.where((t) => t.isActive), isEmpty);
      // TearDown may repeat dispose; it must be idempotent.
    },
  );
  test(
    'unconfigured/trust errors never trigger automatic retries or download',
    () async {
      client.checkFailure = 'UPDATE_UNCONFIGURED';
      await ui.startBackground();
      expect(client.calls, ['check']);
      expect(
        timers.where((t) => t.isActive).single.delay,
        const Duration(hours: 6),
      );
      expect(ui.snapshot, isNull);
    },
  );
}
