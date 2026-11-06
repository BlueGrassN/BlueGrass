import 'dart:async';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:test/test.dart';
import 'api_v2_data_source_test.dart'
    show FakeTransport, ok, error, source, login;
import 'session_coordinator_test.dart' show tokens;

Map<String, Object?> capabilities() => {
  'connectable_nodes': true,
  'resource_queries': true,
  'all_owned_resources': false,
  'expiry_summaries': true,
  'sms_login': false,
  'authoritative_expected_exit': false,
  'resource_kinds': ['subscription', 'asset'],
  'ownership_coverage': 'conservative',
};

Map<String, Object?> resource({String kind = 'subscription'}) => {
  'id': '11',
  'kind': kind,
  'name': 'Fixture',
  'owned': true,
  'connectable': false,
  'expires_at': '2026-10-10T04:00:00Z',
  'status': 'expired',
};

Map<String, Object?> page({String kind = 'subscription'}) => {
  'items': [resource(kind: kind)],
  'pagination': {'per_page': 50, 'has_more': false},
  'coverage': {
    'kind': kind,
    'consistency': 'per_page',
    'ownership': 'conservative',
    'snapshot_complete': false,
  },
};

void main() {
  for (final metadata in [false, true, null, 'unknown']) {
    test('expected-exit capability is informational: $metadata', () async {
      final payload = capabilities();
      if (metadata == null) {
        payload.remove('authoritative_expected_exit');
      } else {
        payload['authoritative_expected_exit'] = metadata;
      }
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
      );
      final s = source(t);
      await login(s);
      final result = await s.refreshCapabilities();
      expect(result.connectableNodes, isTrue);
      expect(result.resourceQueries, isTrue);
      expect(result.expirySummaries, isTrue);
      expect(result.allOwnedResources, isFalse);
      expect(result.authoritativeExpectedExit, metadata == true);
    });
  }
  test(
    'informational expected-exit flag never broadens ownership guarantees',
    () async {
      final payload = capabilities()..['authoritative_expected_exit'] = true;
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
      );
      final s = source(t);
      await login(s);
      for (final patch in [
        {'all_owned_resources': true},
        {'ownership_coverage': 'complete'},
        {'resource_queries': 'true'},
      ]) {
        payload
          ..clear()
          ..addAll(capabilities())
          ..['authoritative_expected_exit'] = true
          ..addAll(patch);
        await expectLater(s.refreshCapabilities(), throwsA(isA<ApiFailure>()));
      }
    },
  );
  test(
    'resource detail retains kind and rejects mismatched identities',
    () async {
      var payload = resource(kind: 'asset')..['expires_at'] = null;
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
      );
      final CustomerDataSource s = source(t);
      await s.login(
        username: 'fixture',
        password: 'fixture',
        deviceId: '00000000-0000-4000-8000-000000000001',
      );
      final result = await s.getResource('11', kind: 'asset');
      expect(result.summary.key.kind, 'asset');
      expect(result.summary.expiresAtUtc, isNull);
      expect(t.requests.last.uri.path, '/api/client/v1/resources/asset/11');
      payload = resource(kind: 'asset')..['id'] = '12';
      await expectLater(
        s.getResource('11', kind: 'asset'),
        throwsA(isA<ApiFailure>()),
      );
    },
  );

  test('invalid resource inputs do not issue network calls', () async {
    final t = FakeTransport((_) async => ok(tokens('old')));
    final s = source(t);
    await login(s);
    for (final invalid in <Future<Object> Function()>[
      () => s.listResources(kind: 'other'),
      () => s.listResources(perPage: 101),
      () => s.listResources(cursor: ''),
      () => s.getResource('../11'),
      () => s.getResource('0'),
      () => s.getResource('9223372036854775808'),
    ]) {
      await expectLater(invalid(), throwsA(isA<ApiFailure>()));
    }
    expect(t.requests, hasLength(1));
  });

  test(
    'pagination corruption never becomes successful complete coverage',
    () async {
      final payload = page();
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
      );
      final s = source(t);
      await login(s);
      for (final broken in [
        {'per_page': 50, 'has_more': true},
        {'per_page': 50, 'has_more': false, 'next_cursor': 'unexpected'},
        {'per_page': 100, 'has_more': false},
      ]) {
        payload['pagination'] = broken;
        await expectLater(s.listResources(), throwsA(isA<ApiFailure>()));
      }
      payload['pagination'] = {'per_page': 50, 'has_more': false};
      (payload['coverage'] as Map)['snapshot_complete'] = true;
      await expectLater(s.listResources(), throwsA(isA<ApiFailure>()));
    },
  );

  test('transient capability failure is an error with retry delay', () async {
    final t = FakeTransport(
      (r) async => r.uri.path.endsWith('/login')
          ? ok(tokens('old'))
          : error(503, 'service_unavailable', retryAfter: '4'),
    );
    final s = source(t);
    await login(s);
    await expectLater(
      s.refreshCapabilities(),
      throwsA(
        isA<ApiFailure>().having(
          (e) => e.retryAfter,
          'retry delay',
          const Duration(seconds: 4),
        ),
      ),
    );
    expect(s.customer, isNotNull);
    expect(s.capabilities.resourceQueries, isFalse);
  });

  test(
    'late capability result cannot restore capabilities after logout',
    () async {
      final pending = Completer<ApiResponse>();
      final entered = Completer<void>();
      final t = FakeTransport((r) async {
        if (r.uri.path.endsWith('/login')) return ok(tokens('old'));
        if (r.uri.path.endsWith('/logout')) return ok({});
        entered.complete();
        return pending.future;
      });
      final s = source(t);
      await login(s);
      final rejected = expectLater(
        s.refreshCapabilities(),
        throwsA(isA<ApiFailure>()),
      );
      await entered.future;
      await s.logout();
      pending.complete(ok(capabilities()));
      await rejected;
      expect(s.capabilities.resourceQueries, isFalse);
    },
  );

  test(
    'new resource API is negotiated without claiming full ownership or egress',
    () async {
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : capabilities()),
      );
      final s = source(t);
      await login(s);
      final result = await s.refreshCapabilities();
      expect(result.resourceQueries, isTrue);
      expect(result.allOwnedResources, isFalse);
      expect(result.expirySummaries, isTrue);
      expect(result.authoritativeExpectedExit, isFalse);
      expect(s.capabilities.expirySummaries, isTrue);
    },
  );

  test(
    'old server reports unsupported instead of successful empty resources',
    () async {
      final t = FakeTransport(
        (r) async => r.uri.path.endsWith('/login')
            ? ok(tokens('old'))
            : error(404, 'not_found'),
      );
      final s = source(t);
      await login(s);
      final result = await s.refreshCapabilities();
      expect(result.resourceQueries, isFalse);
      await expectLater(s.listResources(), throwsA(isA<ApiFailure>()));
      expect(s.customer, isNotNull);
    },
  );

  test(
    'summary maps customer expiry, typed account identity and incomplete coverage',
    () async {
      final t = FakeTransport(
        (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : page()),
      );
      final s = source(t);
      await login(s);
      final result = await s.listExpirySummaries();
      expect(result.items.single.summary.key.accountKey, '123');
      expect(result.items.single.summary.key.kind, 'subscription');
      expect(
        result.items.single.summary.expiresAtUtc,
        DateTime.utc(2026, 10, 10, 4),
      );
      expect(result.items.single.status, 'expired');
      expect(result.snapshotComplete, isFalse);
      expect(result.ownershipCoverage, 'conservative');
      expect(t.requests.last.uri.path, '/api/client/v1/resources/expiry');
    },
  );

  test('asset empty page preserves cursor and never proves removal', () async {
    final payload = page(kind: 'asset')..['items'] = [];
    payload['pagination'] = {
      'per_page': 1,
      'has_more': true,
      'next_cursor': 'YXNzZXQ6MTE',
    };
    final t = FakeTransport(
      (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
    );
    final s = source(t);
    await login(s);
    final result = await s.listResources(kind: 'asset', perPage: 1);
    expect(result.items, isEmpty);
    expect(result.hasMore, isTrue);
    expect(result.snapshotComplete, isFalse);
    payload['pagination'] = {'per_page': 1, 'has_more': false};
    await s.listResources(kind: 'asset', cursor: result.nextCursor, perPage: 1);
    expect(t.requests.last.uri.queryParameters, {
      'kind': 'asset',
      'per_page': '1',
      'cursor': 'YXNzZXQ6MTE',
    });
  });

  test(
    'malformed or cross-kind summaries cannot be accepted as resource data',
    () async {
      final payload = page();
      final t = FakeTransport(
        (r) async =>
            ok(r.uri.path.endsWith('/login') ? tokens('old') : payload),
      );
      final s = source(t);
      await login(s);
      for (final broken in [
        resource(kind: 'asset'),
        resource()..remove('expires_at'),
        resource()..['owned'] = false,
        resource()..['expires_at'] = '2026-02-31T00:00:00Z',
        resource()..['id'] = 'not-an-id',
        resource()..['connectable'] = true,
      ]) {
        payload['items'] = [broken];
        await expectLater(s.listResources(), throwsA(isA<ApiFailure>()));
      }
    },
  );

  test('logout discards capabilities and delayed resource replies', () async {
    final pending = Completer<ApiResponse>();
    final entered = Completer<void>();
    final t = FakeTransport((r) async {
      if (r.uri.path.endsWith('/login')) return ok(tokens('old'));
      if (r.uri.path.endsWith('/capabilities')) return ok(capabilities());
      if (r.uri.path.endsWith('/logout')) return ok({});
      entered.complete();
      return pending.future;
    });
    final s = source(t);
    await login(s);
    await s.refreshCapabilities();
    final response = s.listResources();
    final rejected = expectLater(response, throwsA(isA<ApiFailure>()));
    await entered.future;
    await s.logout();
    pending.complete(ok(page()));
    await rejected;
    expect(s.capabilities.expirySummaries, isFalse);
  });
}
