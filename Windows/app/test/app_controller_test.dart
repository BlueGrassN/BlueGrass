import 'dart:async';

import 'package:bluegrass_client/app_controller.dart';
import 'package:bluegrass_client/dev/fixture_data_source.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter/material.dart' show ThemeMode;

class ControlledSource extends FixtureDataSource {
  Future<Page<NodeRecord>> Function(String?)? page;
  Future<NodeRecord> Function(String)? detail;
  final cursors = <String?>[];
  ApiFailure? logoutFailure;
  ApiFailure? restoreFailure;
  @override
  Future<Customer?> restore() async {
    if (restoreFailure != null) throw restoreFailure!;
    return super.restore();
  }

  @override
  Future<void> logout() async {
    if (logoutFailure != null) throw logoutFailure!;
    await super.logout();
  }

  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) async {
    cursors.add(cursor);
    return page == null ? super.listNodes(cursor: cursor) : page!(cursor);
  }

  @override
  Future<NodeRecord> getNode(String sourceId) =>
      detail == null ? super.getNode(sourceId) : detail!(sourceId);
}

AppController controller(ControlledSource source, {bool demo = true}) =>
    AppController(
      source: source,
      demo: demo,
      getDeviceId: () async => 'fixture-device',
      readPreferences: () async => const Preferences(),
      savePreferences: (_) async {},
    );

Future<void> signIn(AppController c) async {
  await c.initialize();
  await c.login('fixture-user', 'fixture-password');
}

class CountingRefreshController extends AppController {
  CountingRefreshController(ControlledSource source)
    : super(
        source: source,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );

  int activeRefreshes = 0;
  @override
  Future<void> refreshActiveResource() async => activeRefreshes++;
}

void main() {
  testWidgets(
    'production relogin after an origin change retains periodic resource checks',
    (tester) async {
      final source = ControlledSource()
        ..restoreFailure = const ApiFailure('environment_mismatch');
      final c = CountingRefreshController(source);
      try {
        await c.initialize();
        expect(c.message, contains('服务地址已更新，请重新登录'));
        expect(c.restoreNeedsRetry, isFalse);
        expect(c.customer, isNull);
        await c.login('fixture-user', 'fixture-password');
        expect(c.customer, isNotNull);
        await tester.pump(const Duration(minutes: 1));
        expect(c.activeRefreshes, 1);
        await c.initialize();
        await tester.pump(const Duration(minutes: 1));
        expect(
          c.activeRefreshes,
          2,
          reason: 'reinitialization must not duplicate periodic checks',
        );
      } finally {
        c.dispose();
      }
    },
  );

  test(
    'first preselection skips invalid profiles and follows empty cursor pages',
    () async {
      final source = ControlledSource();
      final good = source.nodes.last;
      final invalid = NodeRecord(
        key: source.nodes.first.key,
        name: 'Incomplete',
        protocol: 'https',
        host: 'relay.invalid',
        port: 443,
        expectedExitIpv4: '1.1.1.1',
      );
      source.page = (cursor) async => cursor == null
          ? Page(items: <NodeRecord>[], hasMore: true, nextCursor: 'second')
          : Page(items: [invalid, good], hasMore: false);
      final c = controller(source);
      try {
        await signIn(c);
        expect(source.cursors, [null, 'second']);
        expect(c.selected?.key, good.key);
        expect(c.active, isFalse);
      } finally {
        c.dispose();
      }
    },
  );
  test(
    'verified unsupported version blocks new connection but allows logout',
    () async {
      final c = controller(ControlledSource());
      try {
        await signIn(c);
        c.setUpdateUnsupported(true);
        await c.connect();
        expect(c.connected, isFalse);
        expect(c.message, contains('更新'));
        await c.logout();
        expect(c.customer, isNull);
      } finally {
        c.dispose();
      }
    },
  );
  for (final failure in [
    const ApiFailure('session_revoked', status: 401),
    const ApiFailure('refresh_retry_expired', status: 409),
  ]) {
    test(
      'terminal auth ${failure.code} clears stale account resources',
      () async {
        final source = ControlledSource();
        final c = controller(source);
        addTearDown(c.dispose);
        await signIn(c);
        source.page = (_) async => throw failure;
        await c.refresh();
        expect(c.customer, isNull);
        expect(c.nodes, isEmpty);
        expect(c.selected, isNull);
        expect(c.loading, isFalse);
        expect(c.message, contains('重新登录'));
      },
    );
  }
  test('corrupt preferences do not disable healthy session storage', () async {
    final c = AppController(
      source: ControlledSource(),
      demo: true,
      getDeviceId: () async => 'fixture-device',
      readPreferences: () async => throw const FormatException('corrupt'),
      savePreferences: (_) async {},
    );
    addTearDown(c.dispose);
    await c.initialize();
    expect(c.storageReady, isTrue);
    await c.login('fixture', 'fixture');
    expect(c.customer, isNotNull);
  });
  test(
    'startup transient failure can restore original session on retry',
    () async {
      final source = ControlledSource();
      await source.login(
        username: 'existing-user',
        password: 'fixture',
        deviceId: 'fixture',
      );
      source.restoreFailure = const ApiFailure('timeout');
      final c = controller(source);
      addTearDown(c.dispose);
      await c.initialize();
      expect(c.customer, isNull);
      source.restoreFailure = null;
      await c.initialize();
      expect(c.customer?.username, 'existing-user');
    },
  );
  test(
    'two failed preference writes roll back to last durable value',
    () async {
      final writes = [Completer<void>(), Completer<void>()];
      var index = 0;
      final c = AppController(
        source: ControlledSource(),
        demo: true,
        getDeviceId: () async => 'fixture-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) => writes[index++].future,
      );
      addTearDown(c.dispose);
      await c.initialize();
      final first = c.updatePreferences(
        c.preferences.copyWith(theme: ThemeMode.dark),
      );
      final second = c.updatePreferences(
        c.preferences.copyWith(theme: ThemeMode.light),
      );
      await Future<void>.delayed(Duration.zero);
      writes[0].completeError(StateError('write failed'));
      await first;
      await Future<void>.delayed(Duration.zero);
      writes[1].completeError(StateError('write failed'));
      await second;
      expect(c.preferences.theme, ThemeMode.system);
    },
  );
  test(
    'failed local logout must not claim local session was cleared',
    () async {
      final source = ControlledSource()
        ..logoutFailure = const ApiFailure('storage_error');
      final c = controller(source);
      addTearDown(c.dispose);
      await signIn(c);
      await c.logout();
      expect(c.message, contains('未能清除'));
      expect(c.message, isNot(contains('已本地退出')));
      await c.login('other', 'other');
      expect(c.customer, isNull);
      source.logoutFailure = null;
      await c.logout();
      await c.login('other', 'other');
      expect(c.customer?.username, 'other');
    },
  );
  test('login preselects a resource without connecting', () async {
    final c = controller(ControlledSource());
    addTearDown(c.dispose);
    await signIn(c);
    expect(c.selected?.key.sourceId, 'hk-01');
    expect(c.connection, 'idle');
    expect(c.actualExitIp, isNull);
  });

  test('cancelled detail response cannot establish connection', () async {
    final source = ControlledSource();
    final response = Completer<NodeRecord>();
    final c = controller(source);
    addTearDown(c.dispose);
    await signIn(c);
    source.detail = (_) => response.future;
    final connecting = c.connect();
    await c.disconnect();
    response.complete(source.nodes.first);
    await connecting;
    expect(c.connection, 'idle');
    expect(c.actualExitIp, isNull);
  });

  test('valid config without expected IP connects without inventing an observed IP', () async {
    final source = ControlledSource();
    final c = controller(source);
    addTearDown(c.dispose);
    await signIn(c);
    final node = source.nodes.first;
    source.detail = (_) async => NodeRecord(
      key: node.key,
      name: node.name,
      protocol: node.protocol,
      host: '1.1.1.1',
      port: 1080,
    );
    await c.connect();
    expect(c.connected, isTrue);
    expect(c.message, isNull);
    expect(c.actualExitIp, isNull);
  });

  test('production client cannot simulate a native connection', () async {
    final c = controller(ControlledSource(), demo: false);
    addTearDown(c.dispose);
    await signIn(c);
    await c.connect();
    expect(c.connection, 'failed');
    expect(c.message, contains('PROT-001'));
    expect(c.actualExitIp, isNull);
  });

  test(
    'empty first page follows the opaque cursor to an eligible node',
    () async {
      final source = ControlledSource();
      source.page = (cursor) async => cursor == null
          ? Page(items: [], hasMore: true, nextCursor: 'opaque+a/b==')
          : Page(items: source.nodes, hasMore: false);
      final c = controller(source);
      addTearDown(c.dispose);
      await signIn(c);
      expect(c.nodes, hasLength(3));
      expect(c.hasMore, isFalse);
      await c.refresh(more: true);
      expect(source.cursors, [null, 'opaque+a/b==']);
      expect(c.selected, isNotNull);
      expect(c.connection, 'idle');
    },
  );

  test('logout discards late resource pages from old account', () async {
    final source = ControlledSource();
    final c = controller(source);
    addTearDown(c.dispose);
    await signIn(c);
    final response = Completer<Page<NodeRecord>>();
    source.page = (_) => response.future;
    final refresh = c.refresh();
    await c.logout();
    response.complete(
      Page(items: source.nodes, hasMore: true, nextCursor: 'old'),
    );
    await refresh;
    expect(c.customer, isNull);
    expect(c.nodes, isEmpty);
    expect(c.selected, isNull);
    expect(c.hasMore, isFalse);
  });

  test(
    'diagnostic output excludes account, node and credential data',
    () async {
      final c = controller(ControlledSource());
      addTearDown(c.dispose);
      await signIn(c);
      await c.diagnose();
      final output = c.diagnosticJson();
      for (final secret in [
        'fixture-user',
        'fixture-password',
        'relay.invalid',
        'access_token',
        'refresh_token',
      ]) {
        expect(output, isNot(contains(secret)));
      }
      expect(output, contains('UDP'));
    },
  );
}
