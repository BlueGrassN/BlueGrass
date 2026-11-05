import 'dart:convert';
import 'dart:io';
import 'package:test/test.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'session_coordinator_test.dart'
    show MemoryStore, tokens, now, device, uuid;

class FakeTransport implements HttpTransport {
  FakeTransport(this.handle);
  final Future<ApiResponse> Function(ApiRequest) handle;
  final requests = <ApiRequest>[];
  @override
  Future<ApiResponse> send(ApiRequest request) {
    requests.add(request);
    return handle(request);
  }
}

ApiResponse ok(Object? data) => ApiResponse(
  status: 200,
  body: utf8.encode(
    jsonEncode({
      'success': true,
      'data': data,
      'server_time': now.toIso8601String(),
    }),
  ),
);
ApiResponse error(int status, String code, {String? retryAfter}) => ApiResponse(
  status: status,
  headers: {if (retryAfter != null) 'Retry-After': retryAfter},
  body: utf8.encode(
    jsonEncode({
      'success': false,
      'error': {'code': code, 'message': 'FAKE_PASSWORD FAKE_ACCESS_old'},
      'server_time': now.toIso8601String(),
    }),
  ),
);
Map<String, Object?> node() => {
  'id': '00456',
  'name': 'Fake Hong Kong',
  'protocol': 'socks5',
  'host': 'relay.example.com',
  'port': 10001,
  'username': 'FAKE_NODE_USER',
  'password': 'FAKE_NODE_PASSWORD',
  'country_code': 'HK',
  'country_name': 'Hong Kong',
  'city': 'Hong Kong',
  'expires_at': null,
  'is_forwarded': true,
};
ApiV2DataSource source(FakeTransport transport, {MemoryStore? store}) =>
    ApiV2DataSource(
      baseUri: Uri.parse('https://fake.example/api/client/v1'),
      store: store ?? MemoryStore(),
      transport: transport,
      clock: () => now,
      requestId: () => uuid,
      delay: (_) async {},
    );
Future<void> login(ApiV2DataSource s) async {
  await s.login(
    username: 'fake-user',
    password: 'FAKE_PASSWORD',
    deviceId: device,
  );
}

void main() {
  test('Retry-After HTTP-date is relative to injected clock', () async {
    final t = FakeTransport(
      (r) async => r.uri.path.endsWith('/login')
          ? ok(tokens('old'))
          : error(
              503,
              'service_unavailable',
              retryAfter: HttpDate.format(now.add(const Duration(seconds: 15))),
            ),
    );
    final s = source(t);
    await login(s);
    await expectLater(
      s.listNodes(),
      throwsA(
        isA<ApiFailure>().having(
          (e) => e.retryAfter,
          'retryAfter',
          const Duration(seconds: 15),
        ),
      ),
    );
  });
  test('invalid_credentials login never refreshes', () async {
    final t = FakeTransport((r) async => error(401, 'invalid_credentials'));
    await expectLater(
      login(source(t)),
      throwsA(
        isA<ApiFailure>().having((e) => e.code, 'code', 'invalid_credentials'),
      ),
    );
    expect(t.requests, hasLength(1));
  });
  test(
    'detail rejects a mismatched resource id and malformed UTC timestamp',
    () async {
      var data = {...node(), 'id': '999'};
      final t = FakeTransport(
        (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : data),
      );
      final s = source(t);
      await login(s);
      await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
      data = {...node(), 'expires_at': '2026-02-31T00:00:00Z'};
      await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
    },
  );
  test('password login DTO and only current capabilities', () async {
    final t = FakeTransport((_) async => ok(tokens('old')));
    final s = source(t);
    await login(s);
    expect(s.customer!.id, '123');
    expect(s.customer!.displayName, 'Fake');
    expect(t.requests.single.uri.path, '/api/client/v1/auth/login');
    expect(t.requests.single.body, {
      'username': 'fake-user',
      'password': 'FAKE_PASSWORD',
      'device_id': device,
      'device_name': 'BlueGrass Windows',
    });
    expect(t.requests.single.headers['Authorization'], isNull);
    expect(s.capabilities.connectableNodes, isTrue);
    expect(s.capabilities.allOwnedResources, isFalse);
    expect(s.capabilities.expirySummaries, isFalse);
    expect(s.capabilities.smsLogin, isFalse);
  });
  test('empty_page_keeps_next_cursor with opaque query encoding', () async {
    const cursor = 'opaque+/=?&';
    final t = FakeTransport(
      (r) async => r.uri.path.endsWith('/login')
          ? ok(tokens('old'))
          : ok({
              'items': [],
              'pagination': {
                'per_page': 50,
                'has_more': true,
                'next_cursor': cursor,
              },
            }),
    );
    final s = source(t);
    await login(s);
    final page = await s.listNodes();
    expect(page.items, isEmpty);
    expect(page.hasMore, isTrue);
    expect(page.nextCursor, cursor);
    await s.listNodes(cursor: page.nextCursor);
    expect(t.requests.last.uri.queryParameters, {
      'per_page': '50',
      'cursor': cursor,
    });
    expect(t.requests.last.headers['Authorization'], 'Bearer FAKE_ACCESS_old');
  });
  test(
    'node null expiry and missing exit preserved with namespaced subscription id',
    () async {
      final t = FakeTransport(
        (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : node()),
      );
      final s = source(t);
      await login(s);
      final result = await s.getNode('00456');
      expect(result.key.sourceId, '00456');
      expect(result.key.kind, 'subscription');
      expect(result.key.accountKey, '123');
      expect(result.key.environment, 'https://fake.example/api/client/v1');
      expect(result.key.dataSource, 'api_v2');
      expect(result.expiresAtUtc, isNull);
      expect(result.expectedExitIpv4, isNull);
      expect(result.host, 'relay.example.com');
      expect(result.password, 'FAKE_NODE_PASSWORD');
      expect(prepareProfile(result, now), isA<Ready>());
    },
  );
  for (final exit in [
    null,
    '8.8.8.8',
    '::1',
    'bad',
    '999.1.1.1',
    '08.8.8.8',
    '127.0.0.1',
    '10.0.0.1',
    '203.0.113.1',
  ]) {
    test('future expected_exit_ip $exit never guessed', () async {
      final t = FakeTransport(
        (r) async => ok(
          r.uri.path.endsWith('/login')
              ? tokens('old')
              : {...node(), 'expected_exit_ip': exit},
        ),
      );
      final s = source(t);
      await login(s);
      final result = await s.getNode('00456');
      expect(result.expectedExitIpv4, exit == '8.8.8.8' ? exit : null);
      expect(prepareProfile(result, now), isA<Ready>());
    });
  }
  for (final field in node().keys) {
    test('missing node field $field is contract failure', () async {
      final broken = node()..remove(field);
      final t = FakeTransport(
        (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : broken),
      );
      final s = source(t);
      await login(s);
      await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
    });
  }
  test('missing pagination is not an empty successful page', () async {
    final t = FakeTransport(
      (r) async =>
          ok(r.uri.path.endsWith('/login') ? tokens('old') : {'items': []}),
    );
    final s = source(t);
    await login(s);
    await expectLater(s.listNodes(), throwsA(isA<ApiFailure>()));
  });
  for (final code in [
    'not_found',
    'node_unavailable',
    'https_required',
    'rate_limited',
    'service_busy',
  ]) {
    test(
      'resource or transient failure $code keeps session and hides server message',
      () async {
        final status = {
          'not_found': 404,
          'node_unavailable': 409,
          'https_required': 403,
          'rate_limited': 429,
          'service_busy': 503,
        }[code]!;
        final t = FakeTransport(
          (r) async => r.uri.path.endsWith('/login')
              ? ok(tokens('old'))
              : error(status, code, retryAfter: '5'),
        );
        final s = source(t);
        await login(s);
        try {
          await s.getNode('00456');
          fail('must fail');
        } on ApiFailure catch (e) {
          expect(e.code, code);
          expect(e.status, status);
          expect(e.retryAfter, const Duration(seconds: 5));
          expect(e.toString(), isNot(contains('FAKE_')));
        }
        expect(s.customer, isNotNull);
      },
    );
  }
  test(
    'malformed response and transport exceptions cannot leak credentials',
    () async {
      final t = FakeTransport((_) async => throw StateError('FAKE_PASSWORD'));
      await expectLater(
        login(source(t)),
        throwsA(
          isA<ApiFailure>().having(
            (e) => e.toString(),
            'safe',
            isNot(contains('FAKE_PASSWORD')),
          ),
        ),
      );
      final malformed = FakeTransport(
        (_) async =>
            ApiResponse(status: 200, body: utf8.encode('FAKE_ACCESS_old')),
      );
      await expectLater(login(source(malformed)), throwsA(isA<ApiFailure>()));
    },
  );
  test(
    'public HTTP base and credential/query URLs rejected before network',
    () {
      for (final uri in [
        'http://fake.example/api/client/v1',
        'https://user:password@fake.example/',
        'https://fake.example/?token=FAKE',
      ]) {
        expect(
          () => ApiV2DataSource(baseUri: Uri.parse(uri), store: MemoryStore()),
          throwsA(isA<ApiFailure>()),
        );
      }
    },
  );
}
