import 'dart:async';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:client/app_controller.dart';
import 'package:client/resource_presentation.dart';
import 'package:flutter/material.dart' show ThemeMode;
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

void main() {
  late FakeSource source;
  late MemoryStore store;
  late AppController controller;
  setUp(() {
    source = FakeSource();
    store = MemoryStore();
    controller = AppController(
      source: source,
      store: store,
      clock: () => testNow,
    );
  });
  tearDown(() => controller.dispose());

  test(
    'login preselects eligible resource and identifies Android device',
    () async {
      source.onNodes = (_) async =>
          Page(items: [node('1', expired: true), node('2')], hasMore: false);
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      expect(controller.customer?.id, '1');
      expect(controller.selected?.key.sourceId, '2');
      expect(source.lastDeviceName, 'BlueGrass Android');
    },
  );
  test('logout discards late page and selection', () async {
    await controller.initialize();
    await controller.login('demo', 'fixture-password');
    final pending = Completer<Page<NodeRecord>>();
    source.onNodes = (_) => pending.future;
    final refresh = controller.refresh();
    await controller.logout();
    pending.complete(Page(items: [node('99')], hasMore: false));
    await refresh;
    expect(controller.customer, isNull);
    expect(controller.resources, isEmpty);
    expect(controller.selected, isNull);
  });
  test(
    'empty intermediate page preserves opaque cursor and deduplicates',
    () async {
      source.onNodes = (cursor) async => switch (cursor) {
        null => Page(
          items: [node('1')],
          hasMore: true,
          nextCursor: 'opaque+/=',
        ),
        'opaque+/=' => Page(items: [], hasMore: true, nextCursor: 'last'),
        _ => Page(items: [node('1'), node('2')], hasMore: false),
      };
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      await controller.loadMore();
      expect(controller.hasMore, isTrue);
      await controller.loadMore();
      expect(controller.resources.map((r) => r.key.sourceId), ['1', '2']);
      expect(source.cursors, [null, 'opaque+/=', 'last']);
      expect(controller.hasMore, isFalse);
    },
  );
  test('repeated cursor becomes recoverable paging error', () async {
    source.onNodes = (_) async =>
        Page(items: [], hasMore: true, nextCursor: 'same');
    await controller.initialize();
    await controller.login('demo', 'fixture-password');
    await controller.loadMore();
    expect(controller.resourceError, isNotNull);
    source.onNodes = (_) async => Page(items: [node('2')], hasMore: false);
    await controller.refresh();
    expect(controller.resourceError, isNull);
    expect(controller.resources.single.key.sourceId, '2');
  });
  test('owned asset IDs do not alias subscription selection', () async {
    await controller.initialize();
    await controller.login('demo', 'fixture-password');
    await controller.changeView(ResourceView.assets);
    expect(controller.resources.single.key.kind, 'asset');
    controller.select(controller.resources.single);
    expect(controller.selected?.key.kind, 'subscription');
    await controller.changeView(ResourceView.subscriptions);
    expect(controller.resources.single.key.kind, 'subscription');
    expect(controller.resources.single.eligible, isFalse);
  });
  test('older view response cannot replace newer view', () async {
    await controller.initialize();
    await controller.login('demo', 'fixture-password');
    final pending = Completer<ResourceQueryPage>();
    source.onResources = (_, _) => pending.future;
    final change = controller.changeView(ResourceView.assets);
    await controller.changeView(ResourceView.connectable);
    pending.complete(
      ResourceQueryPage(
        items: [],
        hasMore: false,
        kind: 'asset',
        ownershipCoverage: 'conservative',
        consistency: 'weak',
      ),
    );
    await change;
    expect(controller.resources.first.key.kind, 'subscription');
    expect(controller.view, ResourceView.connectable);
  });
  test(
    'failed logout storage blocks success until retry clears record',
    () async {
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      source.onLogout = () async => throw const ApiFailure('storage_error');
      await controller.logout();
      expect(controller.requiresSessionClear, isTrue);
      expect(controller.authError, isNotNull);
      source.onLogout = null;
      await controller.logout();
      expect(controller.requiresSessionClear, isFalse);
      expect(controller.customer, isNull);
    },
  );
  test('theme persistence failure retains effective theme', () async {
    store.theme = ThemeMode.dark;
    await controller.initialize();
    store.failTheme = true;
    await controller.setTheme(ThemeMode.light);
    expect(controller.themeMode, ThemeMode.dark);
    expect(controller.settingsError, isNotNull);
  });
  test('expiry displays UTC+8 without inventing a null deadline', () {
    expect(
      formatExpiry(DateTime.utc(2026, 10, 13, 20, 5)),
      '2026-10-14 04:05（北京时间）',
    );
    expect(formatExpiry(null), '未指定到期时间');
  });

  test(
    'revocation from obsolete query still clears the current account UI',
    () async {
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      final page = Completer<ResourceQueryPage>();
      source.onResources = (_, _) => page.future;
      final obsolete = controller.changeView(ResourceView.assets);
      await controller.changeView(ResourceView.connectable);
      source.customer = null;
      page.completeError(const ApiFailure('session_revoked', status: 401));
      await obsolete;
      expect(controller.customer, isNull);
      expect(controller.resources, isEmpty);
    },
  );
  test(
    'invalidated session with failed durable clear exposes cleanup retry',
    () async {
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      source.onNodes = (_) async {
        source.customer = null;
        throw const ApiFailure('storage_error');
      };
      source.onLogout = () async => throw const ApiFailure('storage_error');
      await controller.refresh();
      expect(controller.customer, isNull);
      expect(controller.requiresSessionClear, isTrue);
      source.onLogout = null;
      await controller.logout();
      expect(controller.requiresSessionClear, isFalse);
    },
  );
  test(
    'manual selection survives refresh and waits for its later page',
    () async {
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      controller.select(controller.resources.last);
      await controller.changeView(ResourceView.assets);
      source.onNodes = (cursor) async => cursor == null
          ? Page(items: [node('1')], hasMore: true, nextCursor: 'next')
          : Page(items: [node('2')], hasMore: false);
      await controller.changeView(ResourceView.connectable);
      expect(controller.selected, isNull);
      await controller.loadMore();
      expect(controller.selected?.key.sourceId, '2');
      source.onNodes = (_) async => Page(items: [node('1')], hasMore: false);
      await controller.refresh();
      expect(controller.selected, isNull);
    },
  );
  test(
    'refresh retries transient capabilities and enables owned views',
    () async {
      source.onCapabilities = () async =>
          throw const ApiFailure('network_error');
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      expect(controller.resourceQueries, isFalse);
      source.onCapabilities = null;
      await controller.refresh();
      expect(controller.resourceQueries, isTrue);
      expect(controller.notice, isNull);
    },
  );
  test(
    'delayed initialization cannot overwrite a newer theme choice',
    () async {
      final gate = Completer<String>();
      final started = Completer<void>();
      source.customer = null;
      store.onDeviceId = () {
        started.complete();
        return gate.future;
      };
      final initializing = controller.initialize();
      await started.future;
      await controller.setTheme(ThemeMode.dark);
      gate.complete('00000000-0000-4000-8000-000000000001');
      await initializing;
      expect(store.theme, ThemeMode.dark);
      expect(controller.themeMode, ThemeMode.dark);
    },
  );
  test(
    'terminal page with a continuation cursor reports incomplete sync',
    () async {
      source.onNodes = (_) async =>
          Page(items: [node('1')], hasMore: false, nextCursor: 'contradiction');
      await controller.initialize();
      await controller.login('demo', 'fixture-password');
      expect(controller.resourceError, isNotNull);
      expect(controller.resources, isEmpty);
    },
  );
}
