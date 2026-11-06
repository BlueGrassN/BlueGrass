import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_client/windows_reminder_notifications.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const scope = ReminderScope(
    localUser: 'local',
    environment: 'test',
    dataSource: 'api',
    accountKey: 'a',
  );
  final notice = ReminderNotice(
    batch: ReminderBatch(
      id: 'stable-summary-id',
      members: [
        ReminderKey(
          scope: scope,
          resourceKey: const ResourceKey(
            environment: 'test',
            dataSource: 'api',
            accountKey: 'a',
            sourceId: 'test-resource',
          ),
          expiresAtUtc: DateTime.utc(2026, 1, 1),
          stage: ReminderStage.expired,
        ),
      ],
    ),
    snapshot: ExpirySnapshot(
      scope: scope,
      items: const [],
      complete: false,
      syncGeneration: 'test',
    ),
  );
  final calls = <MethodCall>[];
  final binding =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  setUp(() {
    calls.clear();
    binding.setMockMethodCallHandler(platform, (call) async {
      calls.add(call);
      return true;
    });
  });
  tearDown(() => binding.setMockMethodCallHandler(platform, null));
  test(
    'backend sends stable identity and generic text only on replacement',
    () async {
      final sink = WindowsReminderNotifications();
      expect(await sink.submit(notice), isTrue);
      expect(await sink.submit(notice), isTrue);
      expect(
        calls.map((c) => c.method),
        everyElement('submitExpiryNotification'),
      );
      expect(calls.first.arguments, {
        'id': notice.id,
        'title': notice.title,
        'body': notice.body,
      });
      expect(calls.last.arguments, calls.first.arguments);
      expect(
        calls.first.arguments.toString(),
        isNot(contains('test-resource')),
      );
    },
  );
  test(
    'permission refusal, missing backend, and platform failure return false',
    () async {
      final sink = WindowsReminderNotifications();
      binding.setMockMethodCallHandler(platform, (_) async => false);
      expect(await sink.submit(notice), isFalse);
      binding.setMockMethodCallHandler(
        platform,
        (_) async => throw PlatformException(code: 'denied'),
      );
      expect(await sink.submit(notice), isFalse);
      binding.setMockMethodCallHandler(platform, null);
      expect(await sink.submit(notice), isFalse);
    },
  );
  test('cancellation forwards identical scoped ID and never submits', () async {
    binding.setMockMethodCallHandler(platform, (call) async {
      calls.add(call);
      return null;
    });
    final sink = WindowsReminderNotifications();
    await sink.cancel(notice.id);
    await sink.cancel(notice.id);
    expect(
      calls.map((c) => c.method),
      everyElement('cancelExpiryNotification'),
    );
    expect(calls.map((c) => c.arguments), everyElement({'id': notice.id}));
  });
}
