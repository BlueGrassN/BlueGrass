import 'dart:async';
import 'dart:convert';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:test/test.dart';
import 'api_v2_data_source_test.dart' show FakeTransport, login, node;
import 'session_coordinator_test.dart' show MemoryStore, tokens, now, uuid;

ApiResponse timed(Object? data, Object serverTime, {int status = 200}) =>
    ApiResponse(
      status: status,
      body: utf8.encode(
        jsonEncode({
          'success': status < 400,
          if (status < 400) 'data': data,
          if (status >= 400) 'error': {'code': data, 'message': ''},
          'server_time': serverTime is DateTime
              ? serverTime.toIso8601String()
              : serverTime,
        }),
      ),
    );

ApiV2DataSource timedSource(
  FakeTransport transport, {
  MemoryStore? store,
  DateTime Function()? clock,
  bool http = false,
}) => ApiV2DataSource(
  baseUri: Uri.parse(
    http
        ? 'http://127.0.0.1/api/client/v1'
        : 'https://fake.example/api/client/v1',
  ),
  store: store ?? MemoryStore(),
  transport: transport,
  clock: clock ?? () => now,
  requestId: () => uuid,
  delay: (_) async {},
  allowLoopbackHttp: http,
);

DateTime? effective(ApiV2DataSource source) => source.effectiveNowUtc;

class GatedStore extends MemoryStore {
  final replacing = Completer<void>();
  final release = Completer<void>();
  @override
  Future<void> replace(Map<String, Object?> snapshot) async {
    replacing.complete();
    await release.future;
    await super.replace(snapshot);
  }
}

void main() {
  test(
    'login persistence delay advances from response receipt, not acceptance',
    () async {
      final store = GatedStore();
      final source = timedSource(
        FakeTransport((_) async => timed(tokens('old'), now)),
        store: store,
      );
      final pending = login(source);
      await store.replacing.future;
      expect(effective(source), isNull);
      await Future<void>.delayed(const Duration(milliseconds: 40));
      store.release.complete();
      await pending;
      expect(
        effective(source)!.difference(now),
        greaterThanOrEqualTo(const Duration(milliseconds: 30)),
      );
    },
  );

  test(
    'refresh for the wrong account cannot move the current time anchor',
    () async {
      final source = timedSource(
        FakeTransport(
          (r) async => r.uri.path.endsWith('/login')
              ? timed(tokens('old', expired: true), now)
              : timed(
                  tokens('wrong', account: 456),
                  now.add(const Duration(days: 20)),
                ),
        ),
      );
      await login(source);
      await expectLater(source.getNode('00456'), throwsA(isA<ApiFailure>()));
      expect(source.customer!.id, '123');
      expect(
        effective(source)!.difference(now),
        lessThan(const Duration(minutes: 1)),
      );
    },
  );

  test('late refresh cannot anchor a replacement login generation', () async {
    final pending = Completer<ApiResponse>();
    final entered = Completer<void>();
    var logins = 0;
    final source = timedSource(
      FakeTransport((r) async {
        if (r.uri.path.endsWith('/login')) {
          return timed(
            ++logins == 1
                ? tokens('old', expired: true)
                : tokens('new', account: 456),
            now,
          );
        }
        expect(r.uri.path, endsWith('/auth/refresh'));
        entered.complete();
        return pending.future;
      }),
    );
    await login(source);
    final rejected = expectLater(
      source.getNode('00456'),
      throwsA(isA<ApiFailure>()),
    );
    await entered.future;
    await login(source);
    pending.complete(timed(tokens('stale'), now.add(const Duration(days: 20))));
    await rejected;
    expect(source.customer!.id, '456');
    expect(
      effective(source)!.difference(now),
      lessThan(const Duration(minutes: 1)),
    );
  });

  test(
    'HTTPS login time advances monotonically through wall clock jumps',
    () async {
      var wall = now;
      final server = now.add(const Duration(hours: 2));
      final source = timedSource(
        FakeTransport((_) async => timed(tokens('old'), server)),
        clock: () => wall,
      );
      expect(effective(source), isNull);
      await login(source);
      final first = effective(source)!;
      expect(first.isUtc, isTrue);
      expect(first.isBefore(server), isFalse);
      wall = now.subtract(const Duration(days: 3650));
      await Future<void>.delayed(const Duration(milliseconds: 40));
      final second = effective(source)!;
      expect(
        second.difference(first),
        greaterThanOrEqualTo(const Duration(milliseconds: 30)),
      );
      wall = now.add(const Duration(days: 3650));
      final third = effective(source)!;
      expect(third.isBefore(second), isFalse);
      expect(third.difference(server), lessThan(const Duration(minutes: 1)));
    },
  );

  test(
    'backward server anchor cannot revive a known expired resource',
    () async {
      final expiry = now.add(const Duration(milliseconds: 20));
      final source = timedSource(
        FakeTransport(
          (r) async => r.uri.path.endsWith('/login')
              ? timed(tokens('old'), now)
              : timed(node(), now.subtract(const Duration(days: 1))),
        ),
      );
      await login(source);
      await Future<void>.delayed(const Duration(milliseconds: 40));
      final before = effective(source)!;
      expect(before.isBefore(expiry), isFalse);
      await source.getNode('00456');
      final after = effective(source)!;
      expect(after.isBefore(before), isFalse);
      expect(after.isBefore(expiry), isFalse);
      await Future<void>.delayed(const Duration(milliseconds: 30));
      expect(
        effective(source)!.difference(after),
        greaterThanOrEqualTo(const Duration(milliseconds: 20)),
      );
    },
  );

  test('newer authenticated server anchor advances effective time', () async {
    final later = now.add(const Duration(hours: 1));
    final source = timedSource(
      FakeTransport(
        (r) async => r.uri.path.endsWith('/login')
            ? timed(tokens('old'), now)
            : timed(node(), later),
      ),
    );
    await login(source);
    await source.getNode('00456');
    expect(effective(source)!.isBefore(later), isFalse);
  });

  test('errors and malformed server times cannot update the anchor', () async {
    var response = timed(
      'rate_limited',
      now.add(const Duration(days: 10)),
      status: 429,
    );
    final source = timedSource(
      FakeTransport(
        (r) async => r.uri.path.endsWith('/login')
            ? timed(tokens('old'), now)
            : response,
      ),
    );
    await login(source);
    for (final bad in [
      response,
      timed(node(), '2026-02-31T00:00:00Z'),
      timed(null, now.add(const Duration(days: 10))),
    ]) {
      response = bad;
      await expectLater(source.getNode('00456'), throwsA(isA<ApiFailure>()));
      expect(
        effective(source)!.difference(now),
        lessThan(const Duration(minutes: 1)),
      );
    }
  });

  test('failed or malformed login cannot create a trusted anchor', () async {
    for (final response in [
      timed('invalid_credentials', now, status: 401),
      timed({}, now),
    ]) {
      final source = timedSource(FakeTransport((_) async => response));
      await expectLater(login(source), throwsA(isA<ApiFailure>()));
      expect(effective(source), isNull);
    }
    final store = MemoryStore()..failNextReplace = true;
    final source = timedSource(
      FakeTransport((_) async => timed(tokens('old'), now)),
      store: store,
    );
    await expectLater(login(source), throwsA(isA<ApiFailure>()));
    expect(effective(source), isNull);
  });

  test('loopback HTTP never supplies trusted server time', () async {
    final source = timedSource(
      FakeTransport(
        (r) async =>
            timed(r.uri.path.endsWith('/login') ? tokens('old') : node(), now),
      ),
      http: true,
    );
    await login(source);
    await source.getNode('00456');
    expect(effective(source), isNull);
  });

  test(
    'restoring cached credentials does not restore an old time anchor',
    () async {
      final store = MemoryStore();
      final source = timedSource(
        FakeTransport((_) async => timed(tokens('old'), now)),
        store: store,
      );
      await login(source);
      expect(store.value!.keys, isNot(contains('server_time')));
      var requests = 0;
      final restored = timedSource(
        FakeTransport((_) async {
          requests++;
          return timed(node(), now.add(const Duration(minutes: 2)));
        }),
        store: store,
      );
      expect(await restored.restore(), isNotNull);
      expect(requests, 0);
      expect(effective(restored), isNull);
      await restored.getNode('00456');
      final anchored = effective(restored)!;
      await restored.restore();
      expect(effective(restored)!.isBefore(anchored), isFalse);
    },
  );

  test(
    'refresh during restore anchors only the accepted current account',
    () async {
      final store = MemoryStore();
      final source = timedSource(
        FakeTransport((_) async => timed(tokens('old', expired: true), now)),
        store: store,
      );
      await login(source);
      final server = now.add(const Duration(minutes: 3));
      final restored = timedSource(
        FakeTransport((r) async {
          expect(r.uri.path, endsWith('/auth/refresh'));
          return timed(tokens('new'), server);
        }),
        store: store,
      );
      await restored.restore();
      expect(effective(restored)!.isBefore(server), isFalse);
    },
  );

  test(
    'new login discards previous account floor and ignores its delayed read',
    () async {
      final pending = Completer<ApiResponse>();
      final entered = Completer<void>();
      final newLoginEntered = Completer<void>();
      final newLogin = Completer<ApiResponse>();
      var logins = 0;
      final source = timedSource(
        FakeTransport((r) async {
          if (r.uri.path.endsWith('/login')) {
            if (++logins == 1)
              return timed(tokens('old'), now.add(const Duration(days: 10)));
            newLoginEntered.complete();
            return newLogin.future;
          }
          entered.complete();
          return pending.future;
        }),
      );
      await login(source);
      final rejected = expectLater(
        source.getNode('00456'),
        throwsA(isA<ApiFailure>()),
      );
      await entered.future;
      final replacement = login(source);
      await newLoginEntered.future;
      expect(effective(source), isNull);
      newLogin.complete(timed(tokens('new', account: 456), now));
      await replacement;
      pending.complete(timed(node(), now.add(const Duration(days: 20))));
      await rejected;
      expect(source.customer!.id, '456');
      expect(
        effective(source)!.difference(now),
        lessThan(const Duration(minutes: 1)),
      );
    },
  );

  test(
    'late login and logout reply cannot resurrect a cleared time anchor',
    () async {
      final pending = Completer<ApiResponse>();
      final entered = Completer<void>();
      final source = timedSource(
        FakeTransport((_) async {
          entered.complete();
          return pending.future;
        }),
      );
      final rejected = expectLater(login(source), throwsA(isA<ApiFailure>()));
      await entered.future;
      await source.logout();
      pending.complete(timed(tokens('old'), now));
      await rejected;
      expect(effective(source), isNull);

      final loggedIn = timedSource(
        FakeTransport(
          (r) async =>
              timed(r.uri.path.endsWith('/login') ? tokens('old') : {}, now),
        ),
      );
      await login(loggedIn);
      await loggedIn.logout();
      expect(effective(loggedIn), isNull);
    },
  );

  test(
    'terminal authentication failure clears the anchor immediately',
    () async {
      final source = timedSource(
        FakeTransport(
          (r) async => r.uri.path.endsWith('/login')
              ? timed(tokens('old'), now)
              : timed('session_revoked', now, status: 401),
        ),
      );
      await login(source);
      await expectLater(source.getNode('00456'), throwsA(isA<ApiFailure>()));
      expect(effective(source), isNull);
    },
  );
}
