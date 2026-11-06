import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/main.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/successful_resource_history.dart';
import 'package:bluegrass_client/update_client.dart';
import 'package:bluegrass_client/update_presentation.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'successful_resource_history_test.dart' show HistoryStore;
import 'update_startup_policy_test.dart' show StartupClient;

Future<AppController> automaticController() async {
  final source = FixtureDataSource();
  await source.login(
    username: 'fixture',
    password: 'fixture',
    deviceId: 'local',
  );
  final key = source.nodes.first.key;
  final history = SuccessfulResourceHistory(store: HistoryStore());
  await history.recordSuccess(
    ReminderScope(
      localUser: 'local',
      environment: key.environment,
      dataSource: key.dataSource,
      accountKey: key.accountKey,
    ),
    key,
    successId: 'previous-success',
  );
  return AppController(
    source: source,
    demo: true,
    history: history,
    getDeviceId: () async => 'local',
    readPreferences: () async => const Preferences(autoConnect: true),
    savePreferences: (_) async {},
  );
}

void main() {
  testWidgets(
    'startup waits for version decision before restoring an automatic connection',
    (tester) async {
      final client = StartupClient()..checkGate = Completer<UpdateSnapshot>();
      final updates = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      final c = await automaticController();
      try {
        await tester.pumpWidget(MyApp(controller: c, updates: updates));
        await tester.pump();
        await tester.pump(const Duration(seconds: 1));
        expect(
          c.connected,
          isFalse,
          reason: 'restored success must not connect ahead of verified version decision',
        );
        expect(client.calls, contains('check'));
        client.checkGate!.complete(
          const UpdateSnapshot(
            state: UpdateState.upToDate,
            minimumVersion: '9.0.0',
            unsupported: true,
          ),
        );
        await tester.pumpAndSettle();
        expect(c.updateUnsupported, isTrue);
        expect(c.connected, isFalse);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
        updates.dispose();
        await client.dispose();
      }
    },
  );

  testWidgets(
    'unconfigured first start permits opted-in automatic connection promptly',
    (tester) async {
      final client = StartupClient()..policyFailure = 'UPDATE_UNCONFIGURED';
      final updates = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      final c = await automaticController();
      try {
        await tester.pumpWidget(MyApp(controller: c, updates: updates));
        await tester.pumpAndSettle();
        await tester.pump(const Duration(milliseconds: 300));
        await tester.pumpAndSettle();
        expect(c.connected, isTrue);
        expect(updates.verifiedUnsupported, isNull);
        expect(updates.statusText, contains('未配置'));
        expect(client.calls, ['startup_policy']);
      } finally {
        await tester.pumpWidget(const SizedBox());
        c.dispose();
        updates.dispose();
        await client.dispose();
      }
    },
  );

  testWidgets(
    'disposing the shell while the decision is pending never restores the account or connects',
    (tester) async {
      final client = StartupClient()..checkGate = Completer<UpdateSnapshot>();
      final updates = UpdatePresentation(
        client: client,
        pauseBeforeInstall: () async => true,
        handoffAfterLaunch: () async => true,
        exitAfterHandoff: () async {},
      );
      final c = await automaticController();
      try {
        await tester.pumpWidget(MyApp(controller: c, updates: updates));
        await tester.pump();
        await tester.pumpWidget(const SizedBox());
        client.checkGate!.complete(
          const UpdateSnapshot(state: UpdateState.upToDate),
        );
        await tester.pumpAndSettle();
        expect(c.customer, isNull);
        expect(c.connected, isFalse);
        expect(tester.takeException(), isNull);
      } finally {
        c.dispose();
        updates.dispose();
        await client.dispose();
      }
    },
  );
}
