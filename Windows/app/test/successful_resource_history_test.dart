import 'dart:convert';

import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/successful_resource_history.dart';
import 'package:bluegrass_client/platform_store.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class HistoryStore extends ReminderStore {
  final values = <String, String>{};
  bool failWrite = false;
  @override
  Future<String?> read(ReminderScope scope) async => values[scope.stableId];
  @override
  Future<void> write(ReminderScope scope, String value) async {
    if (failWrite) throw StateError('storage unavailable');
    values[scope.stableId] = value;
  }

  @override
  Future<void> delete(ReminderScope scope) async =>
      values.remove(scope.stableId);
}

const scope = ReminderScope(
  localUser: 'local',
  environment: 'test',
  dataSource: 'api',
  accountKey: 'a',
);
const other = ReminderScope(
  localUser: 'local',
  environment: 'test',
  dataSource: 'api',
  accountKey: 'b',
);
ResourceKey key(String id, [ReminderScope owner = scope]) => ResourceKey(
  environment: owner.environment,
  dataSource: owner.dataSource,
  accountKey: owner.accountKey,
  sourceId: id,
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('recent entries require persisted success evidence', () async {
    final store = HistoryStore();
    final history = SuccessfulResourceHistory(store: store);
    await history.recordSuccess(scope, key('one'), successId: 'event-1');
    final value =
        jsonDecode(store.values[scope.stableId]!) as Map<String, dynamic>;
    value['events'] = <String, String>{};
    store.values[scope.stableId] = jsonEncode(value);
    await expectLater(history.getLast(scope), throwsFormatException);
    await expectLater(history.getRecent(scope), throwsFormatException);
  });
  test('default Windows store uses isolated account records and preserves preferences', () async {
    final records = <String, String>{'preferences_v1': '{"autoConnect":true}'};
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(platform, (call) async {
      final args = call.arguments as Map;
      final name = args['key'] as String;
      expect(name, startsWith('successful_resources_'));
      switch (call.method) {
        case 'readRecord':
          return records[name];
        case 'writeRecord':
          records[name] = args['value'] as String;
        case 'deleteRecord':
          records.remove(name);
        default:
          fail('Unexpected platform method');
      }
      return null;
    });
    try {
      final history = SuccessfulResourceHistory();
      await history.recordSuccess(scope, key('one'), successId: 'success');
      await history.recordSuccess(
        other,
        key('other', other),
        successId: 'success',
      );
      final restarted = SuccessfulResourceHistory();
      expect(await restarted.getLast(scope), key('one'));
      await restarted.clearAccount(scope);
      expect(await restarted.getLast(scope), isNull);
      expect(await restarted.getLast(other), key('other', other));
      expect(records['preferences_v1'], '{"autoConnect":true}');
      expect(records.length, 2);
    } finally {
      messenger.setMockMethodCallHandler(platform, null);
    }
  });
  test(
    'selection reads never invent or persist a successful resource',
    () async {
      final store = HistoryStore();
      final history = SuccessfulResourceHistory(store: store);
      expect(await history.getLast(scope), isNull);
      expect(await history.getRecent(scope), isEmpty);
      expect(store.values, isEmpty);
    },
  );
  test(
    'successful order survives restart and replay cannot move an old success',
    () async {
      final store = HistoryStore();
      var history = SuccessfulResourceHistory(store: store);
      await history.recordSuccess(scope, key('one'), successId: 'event-1');
      await history.recordSuccess(scope, key('two'), successId: 'event-2');
      history = SuccessfulResourceHistory(store: store);
      await history.recordSuccess(scope, key('one'), successId: 'event-1');
      expect(await history.getLast(scope), key('two'));
      expect(await history.getRecent(scope), [key('two'), key('one')]);
      await history.recordSuccess(scope, key('one'), successId: 'event-3');
      expect(await history.getRecent(scope), [key('one'), key('two')]);
    },
  );
  test(
    'foreign account and conflicting replay fail without modifying history',
    () async {
      final store = HistoryStore();
      final history = SuccessfulResourceHistory(store: store);
      await history.recordSuccess(scope, key('one'), successId: 'event-1');
      final before = Map.of(store.values);
      await expectLater(
        history.recordSuccess(
          scope,
          key('foreign', other),
          successId: 'event-2',
        ),
        throwsArgumentError,
      );
      await expectLater(
        history.recordSuccess(scope, key('two'), successId: 'event-1'),
        throwsStateError,
      );
      expect(store.values, before);
      expect(await history.getLast(other), isNull);
    },
  );
  test(
    'concurrent writes serialize; logout deletes only named account',
    () async {
      final store = HistoryStore()
        ..values['preferences_v1'] = '{"autoConnect":true}';
      final history = SuccessfulResourceHistory(store: store);
      await Future.wait([
        history.recordSuccess(scope, key('one'), successId: 'event-1'),
        history.recordSuccess(scope, key('two'), successId: 'event-2'),
        history.recordSuccess(other, key('other', other), successId: 'event-1'),
      ]);
      expect(await history.getLast(scope), key('two'));
      await history.clearAccount(scope);
      await history.clearAccount(scope);
      expect(await history.getLast(scope), isNull);
      expect(await history.getLast(other), key('other', other));
      expect(store.values['preferences_v1'], '{"autoConnect":true}');
    },
  );
  test(
    'failed persistence does not become a success or poison later writes',
    () async {
      final store = HistoryStore()..failWrite = true;
      final history = SuccessfulResourceHistory(store: store);
      await expectLater(
        history.recordSuccess(scope, key('one'), successId: 'event-1'),
        throwsStateError,
      );
      expect(await history.getLast(scope), isNull);
      store.failWrite = false;
      await history.recordSuccess(scope, key('two'), successId: 'event-2');
      expect(await history.getLast(scope), key('two'));
    },
  );
  test(
    'corrupt or incorrectly scoped persistence never selects a resource',
    () async {
      final store = HistoryStore();
      final history = SuccessfulResourceHistory(store: store);
      await history.recordSuccess(scope, key('one'), successId: 'event-1');
      final json =
          jsonDecode(store.values[scope.stableId]!) as Map<String, dynamic>;
      json['scope'] = other.stableId;
      store.values[scope.stableId] = jsonEncode(json);
      await expectLater(history.getLast(scope), throwsFormatException);
      await expectLater(history.clearAccount(scope), throwsFormatException);
      expect(store.values[scope.stableId], jsonEncode(json));
    },
  );
}
