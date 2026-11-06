import 'dart:async';

import 'package:bluegrass_client/app_controller.dart' as desktop;
import 'package:bluegrass_client/preferences.dart';
import 'package:bluegrass_client/src/android/app_controller.dart' as android;
import 'package:bluegrass_client/src/android/platform/android_store.dart';
import 'package:bluegrass_client/src/application/account_resources.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/material.dart' show ThemeMode;
import 'package:flutter_test/flutter_test.dart';

NodeRecord node(String id, {String account = 'account'}) => NodeRecord(
  key: ResourceKey(
    environment: 'test',
    dataSource: 'test',
    accountKey: account,
    sourceId: id,
  ),
  name: id,
  protocol: 'socks5',
  host: 'relay.example',
  port: 1080,
);

class Source implements CustomerDataSource {
  @override
  Customer? customer;
  Future<Page<NodeRecord>> Function(String?) page = (_) async =>
      Page(items: [node('first')], hasMore: false);
  @override
  DataSourceCapabilities get capabilities => const DataSourceCapabilities();
  @override
  Future<Customer?> restore() async => customer;
  @override
  Future<Customer> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = '',
  }) async => customer = Customer(
    id: 'account',
    username: username,
    displayName: username,
  );
  @override
  Future<void> logout() async {
    customer = null;
  }

  @override
  Future<Page<NodeRecord>> listNodes({String? cursor, int perPage = 50}) =>
      page(cursor);
  @override
  Future<NodeRecord> getNode(String sourceId) async => node(sourceId);
}

class CapabilitySource extends Source implements ResourceQueryDataSource {
  Future<DataSourceCapabilities> Function() onCapabilities = () async =>
      const DataSourceCapabilities(resourceQueries: true);
  @override
  Future<DataSourceCapabilities> refreshCapabilities() => onCapabilities();
  @override
  Future<ResourceQueryPage> listResources({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) async => ResourceQueryPage(
    items: [],
    hasMore: false,
    kind: kind,
    ownershipCoverage: 'conservative',
    consistency: 'weak',
  );
  @override
  Future<ResourceQueryPage> listExpirySummaries({
    String kind = 'subscription',
    String? cursor,
    int perPage = 50,
  }) => listResources(kind: kind, cursor: cursor, perPage: perPage);
  @override
  Future<OwnedResourceRecord> getResource(
    String sourceId, {
    String kind = 'subscription',
  }) async => throw const ApiFailure('node_unavailable');
}

class Store implements AppStore {
  @override
  Future<String> deviceId() async => 'test-device';
  @override
  Future<ThemeMode> readTheme() async => ThemeMode.system;
  @override
  Future<void> writeTheme(ThemeMode value) async {}
  @override
  Future<String> readRoutingMode() async => 'bypass_cn';
  @override
  Future<void> writeRoutingMode(String value) async {}
  @override
  Future<Map<String, Object?>?> read() async => null;
  @override
  Future<void> replace(Map<String, Object?> value) async {}
  @override
  Future<void> clear() async {}
}

// Exercise the public production adapters with identical data and races.
class Harness {
  Harness(bool mobile, this.source) {
    if (mobile) {
      phone = android.AppController(source: source, store: Store());
    } else {
      pc = desktop.AppController(
        source: source,
        demo: true,
        getDeviceId: () async => 'test-device',
        readPreferences: () async => const Preferences(),
        savePreferences: (_) async {},
      );
    }
  }
  final Source source;
  android.AppController? phone;
  desktop.AppController? pc;
  Future<void> initialize() => phone?.initialize() ?? pc!.initialize();
  Future<void> login() =>
      phone?.login('user', 'password') ?? pc!.login('user', 'password');
  Future<void> logout() => phone?.logout() ?? pc!.logout();
  Future<void> refresh() => phone?.refresh() ?? pc!.refresh();
  Future<void> more() => phone?.loadMore() ?? pc!.refresh(more: true);
  Customer? get customer => phone != null ? phone!.customer : pc!.customer;
  Iterable<ResourceKey> get keys => phone != null
      ? phone!.resources.map((r) => r.key)
      : pc!.nodes.map((n) => n.key);
  String? get error => phone != null ? phone!.resourceError : pc!.message;
  bool get loading => phone?.loading ?? pc!.loading;
  void dispose() {
    phone?.dispose();
    pc?.dispose();
  }
}

void main() {
  group('shared account coordinator', () {
    late CapabilitySource source;
    late AccountResources<NodeRecord> account;
    setUp(() {
      source = CapabilitySource();
      account =
          AccountResources<NodeRecord>(
              source: () => source,
              keyOf: (node) => node.key,
              eligible: (node) =>
                  prepareProfile(node, DateTime.now().toUtc()) is Ready,
            )
            ..customer = const Customer(
              id: 'account',
              username: 'user',
              displayName: 'User',
            );
    });
    tearDown(() => account.dispose());

    test('capability failure enables retry and success clears it', () async {
      source.onCapabilities = () async =>
          throw const ApiFailure('network_error');
      await expectLater(
        account.refreshCapabilities(),
        throwsA(isA<ApiFailure>()),
      );
      expect(account.retryCapabilities, isTrue);
      expect(account.capabilities.resourceQueries, isFalse);
      source.onCapabilities = () async =>
          const DataSourceCapabilities(resourceQueries: true);
      await account.refreshCapabilities();
      expect(account.retryCapabilities, isFalse);
      expect(account.capabilities.resourceQueries, isTrue);
    });

    for (final failure in [false, true]) {
      test(
        'old capability ${failure ? 'failure' : 'result'} cannot affect a new account',
        () async {
          final pending = Completer<DataSourceCapabilities>();
          source.onCapabilities = () => pending.future;
          final old = account.refreshCapabilities();
          account.beginAccountChange();
          account.resetResources();
          if (failure) {
            pending.completeError(
              const ApiFailure('session_revoked', status: 401),
            );
          } else {
            pending.complete(
              const DataSourceCapabilities(resourceQueries: true),
            );
          }
          expect(await old, isNull);
          expect(account.capabilities.resourceQueries, isFalse);
          expect(account.retryCapabilities, isFalse);
        },
      );
    }

    test(
      'newer query owns loading while old query reports session failure',
      () async {
        final oldPage = Completer<Page<NodeRecord>>();
        final newPage = Completer<Page<NodeRecord>>();
        final errors = <(Object, bool)>[];
        final old = account.load(
          reset: true,
          fetch: (_) => oldPage.future,
          changed: () {},
          onError: (e, current) {
            errors.add((e, current));
          },
        );
        final fresh = account.load(
          reset: true,
          fetch: (_) => newPage.future,
          changed: () {},
          onError: (e, current) {
            errors.add((e, current));
          },
        );
        oldPage.completeError(const ApiFailure('session_revoked', status: 401));
        await old;
        expect(errors.single.$1, isA<ApiFailure>());
        expect(errors.single.$2, isFalse);
        expect(account.loading, isTrue);
        newPage.complete(Page(items: [node('fresh')], hasMore: false));
        await fresh;
        expect(account.items.single.key.sourceId, 'fresh');
        expect(account.loading, isFalse);
      },
    );

    test(
      'disposed owner ignores pending failure without notifications',
      () async {
        final pending = Completer<Page<NodeRecord>>();
        var changes = 0;
        final loading = account.load(
          reset: true,
          fetch: (_) => pending.future,
          changed: () => changes++,
          onError: (_, _) => fail('disposed account received failure'),
        );
        expect(changes, 1);
        account.dispose();
        pending.completeError(const ApiFailure('session_revoked', status: 401));
        await loading;
        expect(changes, 1);
        expect(account.items, isEmpty);
        expect(account.loading, isFalse);
      },
    );
  });

  for (final mobile in [false, true]) {
    group(
      mobile
          ? 'Android production controller'
          : 'desktop production controller',
      () {
        late Source source;
        late Harness c;
        setUp(() async {
          source = Source();
          c = Harness(mobile, source);
          await c.initialize();
          await c.login();
        });
        tearDown(() => c.dispose());

        if (!mobile) {
          test(
            'explicitly cleared desktop choice permits new preselection',
            () async {
              c.pc!.selected = null;
              source.page = (_) async =>
                  Page(items: [node('replacement')], hasMore: false);
              await c.refresh();
              expect(c.pc!.selected?.key.sourceId, 'replacement');
            },
          );
        }

        test(
          'rejects a foreign account page without exposing its resource',
          () async {
            source.page = (_) async => Page(
              items: [node('foreign', account: 'other')],
              hasMore: false,
            );
            await c.refresh();
            expect(c.keys.any((key) => key.accountKey == 'other'), isFalse);
            expect(c.error, isNotNull);
          },
        );

        test('rejects a cursor loop across separate load-more calls', () async {
          source.page = (cursor) async => Page(
            items: [node(cursor ?? 'first')],
            hasMore: true,
            nextCursor: cursor == 'a' ? 'b' : 'a',
          );
          await c.refresh();
          await c.more();
          await c.more();
          expect(c.error, isNotNull);
          expect(c.keys.map((k) => k.sourceId), isNot(contains('b')));
        });

        test(
          'failed append retains cursor for retry and deduplicates on success',
          () async {
            source.page = (_) async => Page(
              items: [node('first')],
              hasMore: true,
              nextCursor: 'opaque+/=',
            );
            await c.refresh();
            final cursors = <String?>[];
            source.page = (cursor) async {
              cursors.add(cursor);
              throw const ApiFailure('network_error');
            };
            await c.more();
            expect(c.keys.map((k) => k.sourceId), ['first']);
            source.page = (cursor) async {
              cursors.add(cursor);
              return Page(
                items: [node('first'), node('second')],
                hasMore: false,
              );
            };
            await c.more();
            expect(cursors, ['opaque+/=', 'opaque+/=']);
            expect(c.keys.map((k) => k.sourceId), ['first', 'second']);
            expect(c.error, isNull);
          },
        );

        for (final fail in [false, true]) {
          test(
            'ignores old session ${fail ? 'failure' : 'page'} after relogin',
            () async {
              final pending = Completer<Page<NodeRecord>>();
              source.page = (_) => pending.future;
              final old = c.refresh();
              await c.logout();
              source.page = (_) async =>
                  Page(items: [node('new-session')], hasMore: false);
              await c.login();
              if (fail) {
                pending.completeError(
                  const ApiFailure('session_revoked', status: 401),
                );
              } else {
                pending.complete(
                  Page(items: [node('old-session')], hasMore: false),
                );
              }
              await old;
              expect(c.customer, isNotNull);
              expect(c.keys.map((k) => k.sourceId), ['new-session']);
              expect(c.error, isNull);
              expect(c.loading, isFalse);
            },
          );
        }

        test(
          'current terminal session failure clears account resources',
          () async {
            source.page = (_) async =>
                throw const ApiFailure('session_revoked', status: 401);
            await c.refresh();
            expect(c.customer, isNull);
            expect(c.keys, isEmpty);
            expect(c.loading, isFalse);
          },
        );
      },
    );
  }
}
