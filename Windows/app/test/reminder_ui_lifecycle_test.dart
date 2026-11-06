import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

class MemoryStore extends ReminderStore {
  String? value;
  @override
  Future<String?> read(ReminderScope scope) async => value;
  @override
  Future<void> write(ReminderScope scope, String text) async {
    value = text;
  }

  @override
  Future<void> delete(ReminderScope scope) async {
    value = null;
  }
}

class Notifications extends ReminderNotifications {
  String? id;
  @override
  Future<bool> submit(ReminderNotice notice) async {
    id = notice.id;
    return true;
  }

  @override
  Future<void> cancel(String id) async {}
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(() => messenger.setMockMethodCallHandler(platform, null));

  testWidgets('notification signal during an in-flight empty read is drained', (
    tester,
  ) async {
    final sink = Notifications();
    final reminders = ExpiryReminderCoordinator(
      store: MemoryStore(),
      notifications: sink,
    );
    final now = DateTime.utc(2026, 10, 12);
    await reminders.synchronize(
      ExpirySnapshot(
        scope: const ReminderScope(
          localUser: 'local',
          environment: 'fixture',
          dataSource: 'fixture-v1',
          accountKey: 'fixture-account',
        ),
        items: [
          ExpirySummary(
            key: const ResourceKey(
              environment: 'fixture',
              dataSource: 'fixture-v1',
              accountKey: 'fixture-account',
              sourceId: 'expiry',
            ),
            name: 'Current reminder',
            expiresAtUtc: now,
          ),
        ],
        complete: false,
        syncGeneration: 'round',
      ),
      effectiveNowUtc: now,
      endOfRound: true,
    );
    final firstRead = Completer<String?>();
    var reads = 0;
    messenger.setMockMethodCallHandler(platform, (call) async {
      if (call.method != 'takeExpiryNotificationActivation') return null;
      reads++;
      if (reads == 1) return firstRead.future;
      if (reads == 2) return sink.id;
      return null;
    });
    final c = fixtureController();
    try {
      await tester.pumpWidget(MyApp(controller: c, reminders: reminders));
      await tester.pumpAndSettle();
      await c.login('fixture', 'fixture');
      await tester.pumpAndSettle();
      Future<void> signal() => messenger.handlePlatformMessage(
        platform.name,
        const StandardMethodCodec().encodeMethodCall(
          const MethodCall('expiryNotificationAvailable'),
        ),
        (_) {},
      );
      final firstSignal = signal();
      await tester.pump();
      expect(reads, 1);
      // The notification arrives after Windows captured an empty first read,
      // but before the first response returns to Flutter.
      await signal();
      firstRead.complete(null);
      await firstSignal;
      await tester.pumpAndSettle();
      expect(find.text('全部资源 · 到期清单'), findsOneWidget);
      expect(find.text('Current reminder'), findsOneWidget);
      expect(reads, 3);
    } finally {
      if (!firstRead.isCompleted) firstRead.complete(null);
      await tester.pumpWidget(const SizedBox());
      c.dispose();
      reminders.dispose();
    }
  });

  testWidgets(
    'external controller without reminders never creates Windows storage or notification backend',
    (tester) async {
      final methods = <String>[];
      messenger.setMockMethodCallHandler(platform, (call) async {
        methods.add(call.method);
        return null;
      });
      final c = fixtureController();
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      await c.login('fixture', 'fixture');
      await tester.pumpAndSettle();
      expect(methods, isNot(contains('readRecord')));
      expect(methods, isNot(contains('takeExpiryNotificationActivation')));
      expect(methods, isNot(contains('submitExpiryNotification')));
      await tester.pumpWidget(const SizedBox());
      c.dispose();
    },
  );

  testWidgets(
    'controller reminders are displayed and cold click waits for loaded current scope',
    (tester) async {
      messenger.setMockMethodCallHandler(platform, (_) async => null);
      final store = MemoryStore();
      final sink = Notifications();
      final coordinator = ExpiryReminderCoordinator(
        store: store,
        notifications: sink,
      );
      const scope = ReminderScope(
        localUser: 'local',
        environment: 'fixture',
        dataSource: 'fixture-v1',
        accountKey: 'fixture-account',
      );
      final now = DateTime.utc(2026, 10, 12);
      final snapshot = ExpirySnapshot(
        scope: scope,
        items: [
          ExpirySummary(
            key: const ResourceKey(
              environment: 'fixture',
              dataSource: 'fixture-v1',
              accountKey: 'fixture-account',
              sourceId: 'expired',
            ),
            name: 'Only current owned resource',
            expiresAtUtc: now.subtract(const Duration(hours: 1)),
          ),
        ],
        complete: false,
        syncGeneration: 'round',
      );
      // Persist a prior launch's submitted identity, then recreate the coordinator.
      await coordinator.synchronize(
        snapshot,
        effectiveNowUtc: now,
        endOfRound: true,
      );
      final id = sink.id!;
      coordinator.dispose();
      final reminders = ExpiryReminderCoordinator(
        store: store,
        notifications: sink,
      );
      final c = AppController(
        source: FixtureDataSource(),
        demo: true,
        reminders: reminders,
        getDeviceId: () async => 'local',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      await tester.pumpWidget(MyApp(controller: c));
      await tester.pumpAndSettle();
      await messenger.handlePlatformMessage(
        platform.name,
        const StandardMethodCodec().encodeMethodCall(
          MethodCall('expiryNotificationActivated', id),
        ),
        (_) {},
      );
      await tester.pumpAndSettle();
      expect(find.text('全部资源 · 到期清单'), findsNothing);
      await c.login('fixture', 'fixture');
      await reminders.synchronize(
        snapshot,
        effectiveNowUtc: now,
        endOfRound: true,
      );
      await tester.pumpAndSettle();
      expect(find.text('全部资源 · 到期清单'), findsOneWidget);
      expect(find.text('Only current owned resource'), findsOneWidget);
      await tester.tap(find.text('关闭').last);
      await tester.pumpAndSettle();
      expect(find.textContaining('1 项资源临近到期或已到期'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
      c.dispose();
      reminders.dispose();
    },
  );
}
