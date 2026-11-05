import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/expiry_reminder_coordinator.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/successful_resource_history.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter_test/flutter_test.dart';

class HistoryStore extends ReminderStore {
  final records = <String, String>{};
  @override
  Future<String?> read(ReminderScope scope) async => records[scope.stableId];
  @override
  Future<void> write(ReminderScope scope, String value) async {
    records[scope.stableId] = value;
  }

  @override
  Future<void> delete(ReminderScope scope) async {
    records.remove(scope.stableId);
  }
}

void main() {
  test('persisted successful order survives refresh without changing selection or cursor', () async {
    final source = FixtureDataSource();
    final history = SuccessfulResourceHistory(store: HistoryStore());
    final c = AppController(
      source: source,
      demo: true,
      history: history,
      getDeviceId: () async => 'fixture-device',
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
    );
    try {
      await c.initialize();
      await c.login('fixture', 'fixture');
      final first = c.nodes.first, second = c.nodes[1];
      await c.connect();
      await c.disconnect();
      await c.select(second);
      await c.connect();
      await c.disconnect();
      await c.refresh();
      expect(c.nodes.take(2).map((n) => n.key).toList(), [
        second.key,
        first.key,
      ]);
      expect(c.selected?.key, second.key);
    } finally {
      c.dispose();
    }
  });
  for (final hasHistory in [false, true]) {
    test(
      'automatic connection requires recorded success: $hasHistory',
      () async {
        final source = FixtureDataSource();
        await source.login(
          username: 'fixture',
          password: 'fixture',
          deviceId: 'fixture-device',
        );
        final node = (await source.listNodes()).items.first;
        final history = SuccessfulResourceHistory(store: HistoryStore());
        if (hasHistory) {
          await history.recordSuccess(
            ReminderScope(
              localUser: 'fixture-device',
              environment: node.key.environment,
              dataSource: node.key.dataSource,
              accountKey: node.key.accountKey,
            ),
            node.key,
            successId: 'verified-before-restart',
          );
        }
        final c = AppController(
          source: source,
          demo: true,
          history: history,
          getDeviceId: () async => 'fixture-device',
          readPreferences: () async => const Preferences(autoConnect: true),
          savePreferences: (_) async {},
        );
        try {
          await c.initialize();
          expect(c.connected, hasHistory);
          await c.disconnect();
          await c.refresh();
          expect(
            c.connected,
            isFalse,
            reason:
                'refresh after manual disconnect is not an automatic reconnect',
          );
        } finally {
          c.dispose();
        }
      },
    );
  }
}
