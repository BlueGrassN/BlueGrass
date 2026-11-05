import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class RevocableSource extends FixtureDataSource {
  bool revoked = false;
  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) async {
    if (revoked) throw const ApiFailure('session_revoked', status: 401);
    return super.listNodes(cursor: cursor, perPage: perPage);
  }
}

class DeferredReminderStore extends ReminderStore {
  final entered = Completer<void>();
  final release = Completer<void>();
  @override
  Future<String?> read(ReminderScope scope) async => null;
  @override
  Future<void> write(ReminderScope scope, String text) async {
    if (!entered.isCompleted) entered.complete();
    await release.future;
  }

  @override
  Future<void> delete(ReminderScope scope) async {}
}

class MemoryNotices extends ReminderNotifications {
  int submitted = 0;
  @override
  Future<bool> submit(ReminderNotice notice) async {
    submitted++;
    return true;
  }

  @override
  Future<void> cancel(String id) async {}
}

class UndeletableResumeStore implements SessionStore {
  Map<String, Object?>? marker;
  @override
  Future<Map<String, Object?>?> read() async =>
      marker == null ? null : Map.of(marker!);
  @override
  Future<void> replace(Map<String, Object?> value) async {
    marker = Map.of(value);
  }

  @override
  Future<void> clear() async {
    throw StateError('fixture delete failed');
  }
}

class SlowFirstLogoutSource extends FixtureDataSource {
  int logoutCalls = 0;
  final releaseFirst = Completer<void>();
  @override
  Future<void> logout() async {
    final call = ++logoutCalls;
    await super.logout();
    if (call == 1) await releaseFirst.future;
  }
}

void main() {
  test(
    'overlapping logout must not clear a subsequently logged-in account',
    () async {
      final source = SlowFirstLogoutSource();
      final controller = AppController(
        source: source,
        demo: true,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      Future<void>? first;
      try {
        await controller.initialize();
        await controller.login('account-a', 'fixture');
        first = controller.logout();
        final second = controller.logout();
        await second;
        await Future<void>.delayed(Duration.zero);
        expect(source.logoutCalls, 1);
        await controller.login('account-b', 'fixture');
        expect(controller.customer?.username, isNot('account-b'));
        source.releaseFirst.complete();
        await first;
        await controller.login('account-b', 'fixture');
        expect(source.customer?.username, 'account-b');
        expect(
          controller.customer?.username,
          'account-b',
          reason: 'old logout completion must not overwrite a new login',
        );
      } finally {
        if (!source.releaseFirst.isCompleted) source.releaseFirst.complete();
        if (first != null) await first;
        controller.dispose();
      }
    },
  );
  test(
    'terminal invalidation must retire pending account notifications',
    () async {
      final source = RevocableSource();
      final store = DeferredReminderStore();
      final notices = MemoryNotices();
      final reminders = ExpiryReminderCoordinator(
        store: store,
        notifications: notices,
      );
      final controller = AppController(
        source: source,
        demo: true,
        reminders: reminders,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      Future<void>? pending;
      try {
        await controller.initialize();
        await controller.login('fixture', 'fixture');
        final now = DateTime.now().toUtc();
        pending = reminders.synchronize(
          ExpirySnapshot(
            scope: const ReminderScope(
              localUser: 'fixture-device',
              environment: 'fixture',
              dataSource: 'fixture-v1',
              accountKey: 'fixture-account',
            ),
            items: [
              ExpirySummary(
                key: source.nodes.first.key,
                name: 'Fixture resource',
                expiresAtUtc: now.add(const Duration(hours: 12)),
              ),
            ],
            complete: false,
            syncGeneration: 'before-invalidation',
          ),
          effectiveNowUtc: now,
          endOfRound: true,
        );
        await store.entered.future;
        source.revoked = true;
        await controller.refresh();
        expect(controller.customer, isNull);
        store.release.complete();
        await pending;
        expect(
          notices.submitted,
          0,
          reason: 'old-account notification must not be submitted after terminal invalidation',
        );
        expect(reminders.snapshot, isNull);
      } finally {
        if (!store.release.isCompleted) store.release.complete();
        if (pending != null) await pending;
        controller.dispose();
        reminders.dispose();
      }
    },
  );

  test('explicit disconnect with failed marker deletion must not resume on newer build', () async {
    final source = FixtureDataSource();
    await source.login(
      username: 'fixture',
      password: 'fixture',
      deviceId: 'fixture-device',
    );
    final store = UndeletableResumeStore()
      ..marker = {
        'version': 1,
        'phase': 'handed_off',
        'build': 1,
        'localUser': 'fixture-device',
        'mode': 'tun',
        'key': source.nodes.first.key.toJson(),
        'logicalSessionId': 'old-session',
      };
    AppController make(int build) => AppController(
      source: source,
      demo: true,
      updateResumeStore: store,
      installedBuild: build,
      getDeviceId: () async => 'fixture-device',
      readPreferences: () async =>
          const Preferences(mode: 'tun', autoConnect: false),
      savePreferences: (_) async {},
    );
    final first = make(1);
    final next = make(2);
    try {
      await first.initialize();
      expect(await first.disconnect(), isTrue);
      expect(store.marker?['phase'], 'cancelled');
      await next.initialize();
      expect(
        next.connected,
        isFalse,
        reason: 'explicit disconnect must cancel the durable update resume intent even if autoConnect is disabled',
      );
    } finally {
      first.dispose();
      next.dispose();
    }
  });
}
