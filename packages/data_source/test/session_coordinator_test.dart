import 'dart:async';
import 'dart:convert';
import 'package:test/test.dart';
import 'package:bluegrass_data_source/src/session_coordinator.dart';
import 'package:bluegrass_data_source/src/session_store.dart';
import 'package:bluegrass_data_source/src/api_failure.dart';

final now = DateTime.utc(2026, 10, 11, 8);
const device = 'cce6c5b7-4448-4351-9d80-3a73ef6369b4';
const uuid = 'd0d57b8b-0358-4fc5-a3ea-b8975eeb963b';
Json tokens(String suffix, {bool expired = false, int account = 123}) => {
  'token_type': 'Bearer',
  'access_token': 'FAKE_ACCESS_$suffix',
  'refresh_token': 'FAKE_REFRESH_$suffix',
  'session_id': 'FAKE_SESSION_$account',
  'expires_in': 1800,
  'access_expires_at': now
      .add(Duration(minutes: expired ? -1 : 30))
      .toIso8601String(),
  'refresh_expires_at': now.add(const Duration(days: 180)).toIso8601String(),
  'customer': {'id': account, 'username': 'fake-user', 'display_name': 'Fake'},
};

class MemoryStore implements SessionStore {
  Json? value;
  bool failNextReplace = false;
  final writes = <Json>[];
  @override
  Future<Json?> read() async => value == null ? null : clone(value!);
  @override
  Future<void> replace(Json snapshot) async {
    if (failNextReplace) {
      failNextReplace = false;
      throw StateError('fake failure');
    }
    value = clone(snapshot);
    writes.add(clone(snapshot));
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

Json clone(Json v) =>
    Map<String, Object?>.from(jsonDecode(jsonEncode(v)) as Map);
SessionCoordinator coordinator(MemoryStore store, ApiCall call) =>
    SessionCoordinator(
      store: store,
      environment: 'https://fake.example/api/client/v1',
      call: call,
      clock: () => now,
      requestId: () => uuid,
      delay: (_) async {},
    );
Future<void> login(SessionCoordinator s) async {
  await s.login(
    username: 'fake-user',
    password: 'FAKE_PASSWORD',
    deviceId: device,
  );
}

void main() {
  test('pending_uuid_is_durable_before_refresh_network', () async {
    final store = MemoryStore();
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      if (path == '/auth/login') return tokens('old', expired: true);
      expect(store.value!['pending_refresh'], isA<Map>());
      expect(
        (store.value!['pending_refresh'] as Map)['request_id'],
        body!['request_id'],
      );
      expect(
        (store.value!['pending_refresh'] as Map)['refresh_token'],
        body['refresh_token'],
      );
      return tokens('new');
    });
    await login(s);
    expect(await s.validAccessToken(), 'FAKE_ACCESS_new');
  });
  for (final status in [429, 503, 0]) {
    test(
      'refresh_retry_reuses_persisted_uuid on $status and honors Retry-After',
      () async {
        final store = MemoryStore();
        var time = now;
        final waits = <Duration>[];
        final bodies = <Json>[];
        final s = SessionCoordinator(
          store: store,
          environment: 'test',
          clock: () => time,
          requestId: () => uuid,
          delay: (duration) async {
            waits.add(duration);
            time = time.add(duration);
          },
          call: (method, path, {body, accessToken}) async {
            if (path == '/auth/login') return tokens('old', expired: true);
            bodies.add(clone(body!));
            if (bodies.length == 1)
              throw ApiFailure(
                status == 0 ? 'network_error' : 'service_busy',
                status: status == 0 ? null : status,
                retryAfter: status == 0 ? null : const Duration(seconds: 5),
              );
            expect(store.value, isNotNull);
            return tokens('new');
          },
        );
        await login(s);
        expect(await s.validAccessToken(), 'FAKE_ACCESS_new');
        expect(bodies.length, 2);
        expect(bodies[0], bodies[1]);
        expect(
          waits.single,
          status == 0
              ? greaterThanOrEqualTo(const Duration(milliseconds: 250))
              : const Duration(seconds: 5),
        );
      },
    );
  }
  test(
    'Retry-After beyond recovery window is persisted and never bypassed after restart',
    () async {
      final store = MemoryStore();
      var refreshes = 0;
      Future<Json> call(
        String method,
        String path, {
        Json? body,
        String? accessToken,
      }) async {
        if (path == '/auth/login') return tokens('old', expired: true);
        refreshes++;
        throw const ApiFailure(
          'rate_limited',
          status: 429,
          retryAfter: Duration(seconds: 180),
        );
      }

      final s = coordinator(store, call);
      await login(s);
      await expectLater(s.validAccessToken(), throwsA(isA<ApiFailure>()));
      expect(s.customer, isNotNull);
      expect(store.value!['pending_refresh'], isNotNull);
      await expectLater(
        coordinator(store, call).restore(),
        throwsA(isA<ApiFailure>()),
      );
      expect(refreshes, 1);
    },
  );
  test(
    'twenty delayed access failures share one rotation and original retries at most once',
    () async {
      final store = MemoryStore();
      var refreshes = 0;
      var reads = 0;
      final s = coordinator(store, (method, path, {body, accessToken}) async {
        if (path == '/auth/login') return tokens('old');
        if (path == '/auth/refresh') {
          refreshes++;
          return tokens('new');
        }
        reads++;
        if (accessToken == 'FAKE_ACCESS_old')
          throw const ApiFailure('access_token_expired', status: 401);
        return {'ok': true};
      });
      await login(s);
      final results = await Future.wait(
        List.generate(20, (_) => s.authorized('GET', '/nodes')),
      );
      expect(results, hasLength(20));
      expect(refreshes, 1);
      expect(reads, 40);
    },
  );
  test('second access failure is returned without another rotation', () async {
    final store = MemoryStore();
    var refreshes = 0;
    var reads = 0;
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      if (path == '/auth/login') return tokens('old');
      if (path == '/auth/refresh') {
        refreshes++;
        return tokens('new');
      }
      reads++;
      throw const ApiFailure('invalid_token', status: 401);
    });
    await login(s);
    await expectLater(
      s.authorized('GET', '/nodes'),
      throwsA(isA<ApiFailure>()),
    );
    expect(refreshes, 1);
    expect(reads, 2);
    expect(store.value, isNotNull);
  });
  for (final code in [
    'credentials_changed',
    'account_unavailable',
    'session_revoked',
    'session_expired',
    'invalid_refresh_token',
    'refresh_reuse_detected',
    'account_disabled',
  ]) {
    test('terminal $code requires reauthentication', () async {
      final store = MemoryStore();
      final s = coordinator(store, (method, path, {body, accessToken}) async {
        if (path == '/auth/login') return tokens('old', expired: true);
        throw ApiFailure(code, status: code == 'account_disabled' ? 403 : 401);
      });
      await login(s);
      await expectLater(
        s.validAccessToken(),
        throwsA(isA<ApiFailure>().having((e) => e.code, 'code', code)),
      );
      expect(store.value, isNull);
      expect(s.customer, isNull);
    });
  }
  test('expired refresh token clears auth without network request', () async {
    final store = MemoryStore();
    var requests = 0;
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      requests++;
      return {
        ...tokens('old', expired: true),
        'refresh_expires_at': now.toIso8601String(),
      };
    });
    await login(s);
    await expectLater(s.validAccessToken(), throwsA(isA<ApiFailure>()));
    expect(requests, 1);
    expect(store.value, isNull);
  });
  test(
    'failure to persist pending refresh prevents sending consumed token',
    () async {
      final store = MemoryStore();
      var requests = 0;
      final s = coordinator(store, (method, path, {body, accessToken}) async {
        requests++;
        return tokens('old', expired: true);
      });
      await login(s);
      store.failNextReplace = true;
      await expectLater(
        s.validAccessToken(),
        throwsA(
          isA<ApiFailure>().having((e) => e.code, 'code', 'storage_error'),
        ),
      );
      expect(requests, 1);
    },
  );
  for (final code in ['stale_refresh', 'refresh_retry_expired']) {
    for (final recovered in [false, true]) {
      test(
        '$code rereads committed replacement or requires login: $recovered',
        () async {
          final store = MemoryStore();
          final s = coordinator(store, (
            method,
            path, {
            body,
            accessToken,
          }) async {
            if (path == '/auth/login') return tokens('old', expired: true);
            if (recovered) {
              store.value = {
                ...store.value!,
                'access_token': 'FAKE_ACCESS_new',
                'refresh_token': 'FAKE_REFRESH_new',
                'access_expires_at': now
                    .add(const Duration(minutes: 30))
                    .toIso8601String(),
                'pending_refresh': null,
              };
            }
            throw ApiFailure(code, status: 409);
          });
          await login(s);
          if (recovered) {
            expect(await s.validAccessToken(), 'FAKE_ACCESS_new');
          } else {
            await expectLater(s.validAccessToken(), throwsA(isA<ApiFailure>()));
            expect(store.value, isNull);
          }
        },
      );
    }
  }
  test(
    'environment mismatch does not consume or erase another environment session',
    () async {
      final store = MemoryStore();
      await login(
        coordinator(
          store,
          (method, path, {body, accessToken}) async => tokens('old'),
        ),
      );
      final original = clone(store.value!);
      final other = SessionCoordinator(
        store: store,
        environment: 'different',
        call: (method, path, {body, accessToken}) async {
          fail('no network');
        },
      );
      await expectLater(
        other.restore(),
        throwsA(
          isA<ApiFailure>().having(
            (e) => e.code,
            'code',
            'environment_mismatch',
          ),
        ),
      );
      expect(store.value, original);
    },
  );
  test('logout remote failure explicitly reports local-only exit', () async {
    final store = MemoryStore();
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      if (path == '/auth/login') return tokens('old');
      throw const ApiFailure('network_error');
    });
    await login(s);
    await expectLater(
      s.logout(),
      throwsA(
        isA<ApiFailure>().having(
          (e) => e.code,
          'code',
          'logout_remote_unconfirmed',
        ),
      ),
    );
    expect(store.value, isNull);
    expect(s.customer, isNull);
  });
  test('terminal response isolates other in-flight successful reads', () async {
    final store = MemoryStore();
    final gate = Completer<Json>();
    final started = Completer<void>();
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      if (path == '/auth/login') return tokens('old');
      if (path == '/slow') {
        started.complete();
        return gate.future;
      }
      throw const ApiFailure('session_revoked', status: 401);
    });
    await login(s);
    final slow = s.authorized('GET', '/slow');
    final rejected = expectLater(slow, throwsA(isA<ApiFailure>()));
    await started.future;
    await expectLater(
      s.authorized('GET', '/terminal'),
      throwsA(isA<ApiFailure>()),
    );
    gate.complete({'old_account_secret': 'FAKE'});
    await rejected;
  });
  test(
    'concurrent_requests_share_one_refresh and persist before request',
    () async {
      final store = MemoryStore();
      var refreshes = 0;
      final gate = Completer<Json>();
      final started = Completer<void>();
      final s = coordinator(store, (method, path, {body, accessToken}) async {
        if (path == '/auth/login') return tokens('old', expired: true);
        refreshes++;
        expect((store.value!['pending_refresh'] as Map)['request_id'], uuid);
        expect(body, {'refresh_token': 'FAKE_REFRESH_old', 'request_id': uuid});
        if (!started.isCompleted) started.complete();
        return gate.future;
      });
      await login(s);
      final requests = List.generate(20, (_) => s.validAccessToken());
      await started.future;
      expect(refreshes, 1);
      gate.complete(tokens('new'));
      expect(await Future.wait(requests), everyElement('FAKE_ACCESS_new'));
      expect(store.value!['pending_refresh'], isNull);
      expect(jsonEncode(store.value), isNot(contains('FAKE_PASSWORD')));
    },
  );
  test('crash_before_token_save_recovers_same_request', () async {
    final store = MemoryStore();
    final bodies = <Json>[];
    var loseSave = true;
    Future<Json> call(
      String method,
      String path, {
      Json? body,
      String? accessToken,
    }) async {
      if (path == '/auth/login') return tokens('old', expired: true);
      bodies.add(clone(body!));
      if (loseSave) {
        store.failNextReplace = true;
        loseSave = false;
      }
      return tokens('new');
    }

    final s = coordinator(store, call);
    await login(s);
    await expectLater(s.validAccessToken(), throwsA(anything));
    expect(store.value!['pending_refresh'], isNotNull);
    final restarted = coordinator(store, call);
    await restarted.restore();
    expect(await restarted.validAccessToken(), 'FAKE_ACCESS_new');
    expect(bodies.length, 2);
    expect(bodies[1], bodies[0]);
  });
  test('late_refresh_cannot_overwrite_login', () async {
    final store = MemoryStore();
    final gate = Completer<Json>();
    final started = Completer<void>();
    var logins = 0;
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      if (path == '/auth/login')
        return ++logins == 1
            ? tokens('old', expired: true)
            : tokens('other', account: 456);
      started.complete();
      return gate.future;
    });
    await login(s);
    final refresh = s.validAccessToken();
    final rejected = expectLater(refresh, throwsA(anything));
    await started.future;
    await login(s);
    gate.complete(tokens('late'));
    await rejected;
    expect(await s.validAccessToken(), 'FAKE_ACCESS_other');
    expect(jsonEncode(store.value), isNot(contains('FAKE_ACCESS_late')));
  });
  test('late_login_cannot_resurrect_logout', () async {
    final store = MemoryStore();
    final gate = Completer<Json>();
    final started = Completer<void>();
    final s = coordinator(store, (method, path, {body, accessToken}) async {
      started.complete();
      return gate.future;
    });
    final pending = login(s);
    final rejected = expectLater(pending, throwsA(anything));
    await started.future;
    await s.logout();
    gate.complete(tokens('late'));
    await rejected;
    expect(store.value, isNull);
    expect(s.customer, isNull);
  });
  test(
    'restore callers await pending recovery instead of exposing a premature customer',
    () async {
      final store = MemoryStore();
      await login(
        coordinator(
          store,
          (method, path, {body, accessToken}) async =>
              tokens('old', expired: true),
        ),
      );
      final gate = Completer<Json>();
      final started = Completer<void>();
      final restarted = coordinator(store, (
        method,
        path, {
        body,
        accessToken,
      }) async {
        started.complete();
        return gate.future;
      });
      final first = restarted.restore();
      await started.future;
      var secondDone = false;
      final second = restarted.restore().then((v) {
        secondDone = true;
        return v;
      });
      await Future<void>.delayed(Duration.zero);
      expect(secondDone, isFalse);
      gate.complete(tokens('new'));
      expect((await first)!['id'], 123);
      expect((await second)!['id'], 123);
    },
  );
  test(
    'crash_after_token_save restores replacement without reusing old refresh',
    () async {
      final store = MemoryStore();
      final s = coordinator(
        store,
        (method, path, {body, accessToken}) async => tokens(
          path == '/auth/login' ? 'old' : 'new',
          expired: path == '/auth/login',
        ),
      );
      await login(s);
      await s.validAccessToken();
      final restarted = coordinator(store, (
        method,
        path, {
        body,
        accessToken,
      }) async {
        fail('no rotation needed');
      });
      await restarted.restore();
      expect(await restarted.validAccessToken(), 'FAKE_ACCESS_new');
    },
  );
}
