import 'dart:async';

import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class Store extends ReminderStore {
  final records = <String, String>{};
  bool failAcceptedWrite = false;
  @override
  Future<String?> read(ReminderScope scope) async => records[scope.stableId];
  @override
  Future<void> write(ReminderScope scope, String value) async {
    if (failAcceptedWrite && value.contains('"accepted"')) {
      throw StateError('disk unavailable after platform acceptance');
    }
    records[scope.stableId] = value;
  }

  @override
  Future<void> delete(ReminderScope scope) async {
    records.remove(scope.stableId);
  }
}

class Sink extends ReminderNotifications {
  final notices = <ReminderNotice>[];
  final cancelled = <String>[];
  bool accept = true;
  Completer<bool>? pending;
  @override
  Future<bool> submit(ReminderNotice notice) async {
    notices.add(notice);
    return pending == null ? accept : pending!.future;
  }

  @override
  Future<void> cancel(String id) async {
    cancelled.add(id);
  }
}

const scope = ReminderScope(
  localUser: 'test-user',
  environment: 'test',
  dataSource: 'api',
  accountKey: 'a',
);
final now = DateTime.utc(2026, 12, 30, 12);
ExpirySummary item(
  String id,
  int hours, {
  bool owned = true,
  ReminderScope owner = scope,
}) => ExpirySummary(
  key: ResourceKey(
    environment: owner.environment,
    dataSource: owner.dataSource,
    accountKey: owner.accountKey,
    sourceId: id,
  ),
  name: 'Resource $id',
  expiresAtUtc: now.add(Duration(hours: hours)),
  owned: owned,
);
ExpirySnapshot snapshot(
  List<ExpirySummary> items, {
  bool complete = true,
  String generation = 'round-1',
  ReminderScope owner = scope,
}) => ExpirySnapshot(
  scope: owner,
  items: items,
  complete: complete,
  syncGeneration: generation,
  nextCursor: complete ? null : 'next',
);

void main() {
  test('restart reuses pending platform identity if acknowledgement persistence failed', () async {
    final store = Store()..failAcceptedWrite = true;
    final sink = Sink();
    final c = ExpiryReminderCoordinator(store: store, notifications: sink);
    await c.synchronize(snapshot([item('a', 12)]), effectiveNowUtc: now);
    expect(c.notificationError, isNotNull);
    final id = sink.notices.single.id;
    store.failAcceptedWrite = false;
    final restarted = ExpiryReminderCoordinator(
      store: store,
      notifications: sink,
    );
    await restarted.synchronize(
      snapshot([item('a', 12)], generation: 'after-restart'),
      effectiveNowUtc: now,
    );
    expect(sink.notices.map((notice) => notice.id), [id, id]);
    await restarted.synchronize(
      snapshot([item('a', 12)], generation: 'next-round'),
      effectiveNowUtc: now,
    );
    expect(sink.notices, hasLength(2));
  });
  test(
    'renewing one member replaces the old grouped summary without stale dates',
    () async {
      final sink = Sink();
      final c = ExpiryReminderCoordinator(store: Store(), notifications: sink);
      await c.synchronize(
        snapshot([item('a', 12), item('b', 12)]),
        effectiveNowUtc: now,
      );
      final id = sink.notices.single.id;
      await c.synchronize(
        snapshot([item('a', 240), item('b', 12)], generation: 'round-2'),
        effectiveNowUtc: now,
      );
      expect(sink.notices, hasLength(2));
      expect(sink.notices.last.id, id);
      expect(sink.notices.last.members.map((key) => key.resourceKey.sourceId), [
        'b',
      ]);
      expect(c.items.map((value) => value.key.sourceId), ['b']);
    },
  );
  test('pages merge into one mixed-stage summary and accepted keys survive restart', () async {
    final store = Store(), sink = Sink();
    final c = ExpiryReminderCoordinator(store: store, notifications: sink);
    await c.synchronize(
      snapshot([item('a', 12)], complete: false),
      effectiveNowUtc: now,
    );
    expect(sink.notices, isEmpty);
    expect(c.coverageComplete, isFalse);
    await c.synchronize(snapshot([item('b', -1)]), effectiveNowUtc: now);
    expect(sink.notices, hasLength(1));
    expect(sink.notices.single.members, hasLength(2));
    expect(sink.notices.single.body, contains('已到期 1'));
    expect(sink.notices.single.body, contains('24 小时内 1'));
    expect(c.items, hasLength(2));
    expect(c.coverageComplete, isTrue);
    final restarted = ExpiryReminderCoordinator(
      store: store,
      notifications: sink,
    );
    await restarted.synchronize(
      snapshot([item('a', 12), item('b', -1)], generation: 'round-2'),
      effectiveNowUtc: now,
    );
    expect(sink.notices, hasLength(1));
    expect(restarted.items, hasLength(2));
  });

  test(
    'permission failure stays visible and does not consume member keys',
    () async {
      final sink = Sink()..accept = false;
      final c = ExpiryReminderCoordinator(store: Store(), notifications: sink);
      final batch = snapshot([item('a', 12)]);
      await c.synchronize(batch, effectiveNowUtc: now);
      expect(c.notificationError, isNotNull);
      expect(c.items, hasLength(1));
      sink.accept = true;
      await c.synchronize(batch, effectiveNowUtc: now);
      expect(sink.notices, hasLength(2));
      expect(sink.notices[0].id, sink.notices[1].id);
      expect(c.notificationError, isNull);
      await c.synchronize(batch, effectiveNowUtc: now);
      expect(sink.notices, hasLength(2));
    },
  );

  test(
    'account switch invalidates an in-flight submission and old navigation',
    () async {
      final sink = Sink()..pending = Completer<bool>();
      final c = ExpiryReminderCoordinator(store: Store(), notifications: sink);
      final submit = c.synchronize(
        snapshot([item('a', 12)]),
        effectiveNowUtc: now,
      );
      while (sink.notices.isEmpty) {
        await Future<void>.delayed(Duration.zero);
      }
      final id = sink.notices.single.id;
      final logout = c.clearAccount();
      expect(c.items, isEmpty);
      sink.pending!.complete(true);
      await Future.wait([submit, logout]);
      expect(sink.cancelled, contains(id));
      expect(c.itemsForNotification(id), isEmpty);
      const other = ReminderScope(
        localUser: 'test-user',
        environment: 'test',
        dataSource: 'api',
        accountKey: 'b',
      );
      sink.pending = null;
      await c.synchronize(
        snapshot([item('a', 12, owner: other)], owner: other),
        effectiveNowUtc: now,
      );
      expect(sink.notices, hasLength(2));
      expect(sink.notices.last.id, isNot(id));
    },
  );

  for (final complete in [false, true]) {
    test(
      '72-hour boundary stays visible; display tick hides and later sync cancels known notification (complete=$complete)',
      () async {
        final sink = Sink();
        final c = ExpiryReminderCoordinator(
          store: Store(),
          notifications: sink,
        );
        addTearDown(c.dispose);
        final expired = item('boundary', -72);
        await c.synchronize(snapshot([expired]), effectiveNowUtc: now);
        final notificationId = sink.notices.single.id;
        expect(c.items, [expired]);
        expect(c.itemsForNotification(notificationId), [expired]);

        var displayChanges = 0;
        c.addListener(() => displayChanges++);
        final afterCutoff = now.add(const Duration(microseconds: 1));
        c.updateDisplayTime(afterCutoff);
        expect(displayChanges, 1);
        expect(c.items, isEmpty);
        expect(c.itemsForNotification(notificationId), isEmpty);
        expect(c.snapshot!.items, [expired]);
        expect(sink.cancelled, isEmpty);
        expect(sink.notices, hasLength(1));

        // Even an incomplete page can cancel a known resource's old notice;
        // hiding it never requires treating a missing resource as reclaimed.
        await c.synchronize(
          snapshot([expired], complete: complete, generation: 'after-cutoff'),
          effectiveNowUtc: afterCutoff,
        );
        expect(sink.cancelled, [notificationId]);
        expect(sink.notices, hasLength(1));
        expect(c.items, isEmpty);
        expect(c.itemsForNotification(notificationId), isEmpty);
        expect(c.snapshot!.items, [expired]);
        expect(c.coverageComplete, complete);
        expect(c.notificationError, isNull);
      },
    );
  }

  test(
    'renewal cancels old notification and null expiry never schedules',
    () async {
      final sink = Sink();
      final c = ExpiryReminderCoordinator(store: Store(), notifications: sink);
      await c.synchronize(snapshot([item('a', 12)]), effectiveNowUtc: now);
      final old = sink.notices.single.id;
      await c.synchronize(
        snapshot([item('a', 240)], generation: 'round-2'),
        effectiveNowUtc: now,
      );
      expect(c.items, isEmpty);
      expect(sink.cancelled, contains(old));
      await c.synchronize(
        snapshot([
          ExpirySummary(
            key: item('b', 1).key,
            name: 'No expiry',
            expiresAtUtc: null,
          ),
        ], generation: 'round-3'),
        effectiveNowUtc: now,
      );
      expect(sink.notices, hasLength(1));
    },
  );
}
