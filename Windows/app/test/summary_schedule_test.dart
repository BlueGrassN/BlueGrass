import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';

class SummarySource extends FixtureDataSource
    implements ResourceQueryDataSource {
  int rounds = 0;
  bool fail = false;
  @override
  Future<DataSourceCapabilities> refreshCapabilities() async {
    rounds++;
    if (fail) throw const ApiFailure('timeout');
    return const DataSourceCapabilities();
  }

  @override
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) => throw UnimplementedError();
  @override
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) => throw UnimplementedError();
  @override
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  }) => throw UnimplementedError();
}

void main() {
  testWidgets(
    'account summaries use 30 minute jitter separately from active detail polling',
    (tester) async {
      final source = SummarySource();
      final c = AppController(
        source: source,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      await c.initialize();
      await c.login('fixture', 'fixture');
      expect(source.rounds, 1);
      await tester.pump(const Duration(minutes: 24));
      expect(source.rounds, 1, reason: 'no account-wide scan every minute');
      await tester.pump(const Duration(minutes: 12));
      expect(source.rounds, 2, reason: 'next scan must occur by 35 minutes');
      c.dispose();
      await tester.pump();
    },
  );

  testWidgets(
    'summary transient retries back off and stop retry bursts after three attempts',
    (tester) async {
      final source = SummarySource()..fail = true;
      final c = AppController(
        source: source,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
      await c.initialize();
      await c.login('fixture', 'fixture');
      expect(source.rounds, 1);
      await tester.pump(const Duration(minutes: 1));
      expect(source.rounds, 2);
      await tester.pump(const Duration(minutes: 1));
      expect(source.rounds, 2);
      await tester.pump(const Duration(minutes: 1));
      expect(source.rounds, 3);
      await tester.pump(const Duration(minutes: 4));
      expect(source.rounds, 4);
      await tester.pump(const Duration(minutes: 24));
      expect(source.rounds, 4);
      c.dispose();
      await tester.pump();
    },
  );
}
