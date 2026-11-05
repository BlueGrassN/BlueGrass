import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'api_failure.dart';
import 'session_store.dart';

typedef Json = Map<String, Object?>;
typedef ApiCall =
    Future<Json> Function(
      String method,
      String path, {
      Json? body,
      String? accessToken,
    });

Json object(Object? value) {
  if (value is! Map<String, Object?>)
    throw const ApiFailure('invalid_response');
  return value;
}

String string(Json value, String key, {bool empty = false}) {
  final field = value[key];
  if (field is! String || (!empty && field.isEmpty)) {
    throw const ApiFailure('invalid_response');
  }
  return field;
}

DateTime timestamp(Object? value) {
  if (value is! String ||
      !RegExp(
        r'^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|\+00:00)$',
      ).hasMatch(value)) {
    throw const ApiFailure('invalid_response');
  }
  final parsed = DateTime.tryParse(value);
  if (parsed == null ||
      parsed.toUtc().toIso8601String().substring(0, 19) !=
          value.substring(0, 19)) {
    throw const ApiFailure('invalid_response');
  }
  return parsed.toUtc();
}

Json customerJson(Object? value) {
  final result = object(value);
  if (result['id'] is! int || (result['id'] as int) <= 0)
    throw const ApiFailure('invalid_response');
  string(result, 'username');
  string(result, 'display_name', empty: true);
  return Map.of(result);
}

Json _copy(Json value) => object(jsonDecode(jsonEncode(value)));
bool validUuid(String value) =>
    RegExp(
      r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$',
    ).hasMatch(value) &&
    value.replaceAll('-', '') != '0' * 32;
String _uuid() {
  final random = Random.secure();
  final bytes = List.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final h = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-${h.substring(16, 20)}-${h.substring(20)}';
}

/// One coordinator per encrypted record in one UI process. Durable writes are
/// serialized; generations isolate old login, restore and refresh responses.
class SessionCoordinator {
  SessionCoordinator({
    required SessionStore store,
    required String environment,
    required ApiCall call,
    DateTime Function()? clock,
    String Function()? requestId,
    Future<void> Function(Duration)? delay,
    void Function(Json)? onAuthenticatedResponse,
    void Function()? onSessionReset,
  }) : _store = store,
       _environment = environment,
       _call = call,
       _clock = clock ?? DateTime.now,
       _requestId = requestId ?? _uuid,
       _delay = delay ?? Future<void>.delayed,
       _onAuthenticatedResponse = onAuthenticatedResponse,
       _onSessionReset = onSessionReset;
  final SessionStore _store;
  final String _environment;
  final ApiCall _call;
  final DateTime Function() _clock;
  final String Function() _requestId;
  final Future<void> Function(Duration) _delay;
  final void Function(Json)? _onAuthenticatedResponse;
  final void Function()? _onSessionReset;
  Json? _snapshot;
  int _generation = 0;
  Future<void> _writes = Future.value();
  Future<String>? _refresh;
  Future<Json?>? _restore;
  Json? get customer =>
      _snapshot == null ? null : _copy(object(_snapshot!['customer']));

  void _check(int generation) {
    if (generation != _generation) throw const ApiFailure('session_changed');
  }

  Future<T> _serial<T>(int generation, Future<T> Function() work) {
    final next = _writes.then((_) async {
      _check(generation);
      try {
        return await work();
      } on ApiFailure {
        rethrow;
      } catch (_) {
        throw const ApiFailure('storage_error');
      }
    });
    _writes = next.then<void>((_) {}, onError: (Object _, StackTrace __) {});
    return next;
  }

  Future<void> _save(int generation, Json snapshot) =>
      _serial(generation, () async {
        await _store.replace(_copy(snapshot));
        _check(generation);
        _snapshot = snapshot;
      });
  Future<void> _clear(int generation) => _serial(generation, () async {
    await _store.clear();
    _check(generation);
    _snapshot = null;
  });
  Future<void> _terminate(int generation) {
    _check(generation);
    final next = ++_generation;
    _onSessionReset?.call();
    _snapshot = null;
    _refresh = null;
    _restore = null;
    return _clear(next);
  }

  Json _tokens(Json data, String deviceId) {
    if (data['token_type'] != 'Bearer' ||
        data['expires_in'] is! int ||
        (data['expires_in'] as int) <= 0)
      throw const ApiFailure('invalid_response');
    return {
      'version': 1,
      'environment': _environment,
      'device_id': deviceId,
      'access_token': string(data, 'access_token'),
      'refresh_token': string(data, 'refresh_token'),
      'session_id': string(data, 'session_id'),
      'access_expires_at': timestamp(
        data['access_expires_at'],
      ).toIso8601String(),
      'refresh_expires_at': timestamp(
        data['refresh_expires_at'],
      ).toIso8601String(),
      'customer': customerJson(data['customer']),
      'pending_refresh': null,
    };
  }

  Json _stored(Json data) {
    if (data['version'] != 1 ||
        data['environment'] != _environment ||
        !validUuid(string(data, 'device_id')))
      throw const ApiFailure('invalid_session');
    final snapshot = _tokens({
      ...data,
      'token_type': 'Bearer',
      'expires_in': 1,
    }, string(data, 'device_id'));
    final pending = data['pending_refresh'];
    if (pending != null) {
      final p = object(pending);
      if (p['session_id'] != data['session_id'] ||
          p['refresh_token'] != data['refresh_token'] ||
          !validUuid(string(p, 'request_id')))
        throw const ApiFailure('invalid_session');
      timestamp(p['started_at']);
      if (p['retry_not_before'] != null) timestamp(p['retry_not_before']);
      snapshot['pending_refresh'] = _copy(p);
    }
    return snapshot;
  }

  Future<Json> login({
    required String username,
    required String password,
    required String deviceId,
    String deviceName = 'BlueGrass Windows',
  }) async {
    if (!validUuid(deviceId)) throw const ApiFailure('validation_failed');
    final generation = ++_generation;
    _onSessionReset?.call();
    _snapshot = null;
    _refresh = null;
    _restore = null;
    await _clear(generation);
    final data = await _call(
      'POST',
      '/auth/login',
      body: {
        'username': username,
        'password': password,
        'device_id': deviceId,
        'device_name': deviceName,
      },
    );
    _check(generation);
    await _save(generation, _tokens(data, deviceId));
    _check(generation);
    _onAuthenticatedResponse?.call(data);
    return customer!;
  }

  Future<Json?> restore() {
    if (_restore != null) return _restore!;
    if (_snapshot != null) return Future.value(customer);
    final generation = _generation;
    late Future<Json?> future;
    future = _restoreSession(generation).whenComplete(() {
      if (identical(_restore, future)) _restore = null;
    });
    return _restore = future;
  }

  Future<Json?> _restoreSession(int generation) async {
    final stored = await _serial(generation, _store.read);
    _check(generation);
    if (stored == null) return null;
    if (stored['environment'] != _environment)
      throw const ApiFailure('environment_mismatch');
    _snapshot = _stored(stored);
    await validAccessToken();
    _check(generation);
    return customer;
  }

  Future<String> validAccessToken() async {
    final snapshot = _snapshot;
    if (snapshot == null) throw const ApiFailure('not_authenticated');
    if (snapshot['pending_refresh'] == null &&
        timestamp(snapshot['access_expires_at']).isAfter(_clock().toUtc())) {
      return string(snapshot, 'access_token');
    }
    return _startRefresh();
  }

  Future<String> _startRefresh() {
    if (_refresh != null) return _refresh!;
    final generation = _generation;
    late Future<String> future;
    future = _rotate(generation).whenComplete(() {
      if (identical(_refresh, future)) _refresh = null;
    });
    return _refresh = future;
  }

  Future<String> _rotate(int generation) async {
    var snapshot = _snapshot!;
    if (!timestamp(snapshot['refresh_expires_at']).isAfter(_clock().toUtc())) {
      await _terminate(generation);
      throw const ApiFailure('session_expired', status: 401);
    }
    Json pending;
    if (snapshot['pending_refresh'] == null) {
      final id = _requestId();
      if (!validUuid(id)) throw const ApiFailure('invalid_request_id');
      pending = {
        'session_id': snapshot['session_id'],
        'refresh_token': snapshot['refresh_token'],
        'request_id': id,
        'started_at': _clock().toUtc().toIso8601String(),
        'retry_not_before': null,
      };
      snapshot = {...snapshot, 'pending_refresh': pending};
      await _save(generation, snapshot);
    } else {
      pending = object(snapshot['pending_refresh']);
    }
    for (var attempt = 0; attempt < 3; attempt++) {
      _check(generation);
      final notBefore = pending['retry_not_before'];
      if (notBefore != null) {
        final remaining = timestamp(notBefore).difference(_clock().toUtc());
        if (remaining > Duration.zero) {
          if (timestamp(
                notBefore,
              ).difference(timestamp(pending['started_at'])) >=
              const Duration(seconds: 120)) {
            throw ApiFailure('retry_later', retryAfter: remaining);
          }
          await _delay(remaining);
          _check(generation);
        }
      }
      try {
        final data = await _call(
          'POST',
          '/auth/refresh',
          body: {
            'refresh_token': pending['refresh_token'],
            'request_id': pending['request_id'],
          },
        );
        _check(generation);
        final next = _tokens(data, string(snapshot, 'device_id'));
        if (next['session_id'] != snapshot['session_id'] ||
            object(next['customer'])['id'] !=
                object(snapshot['customer'])['id']) {
          throw const ApiFailure('invalid_response');
        }
        await _save(generation, next);
        _check(generation);
        _onAuthenticatedResponse?.call(data);
        return string(next, 'access_token');
      } on ApiFailure catch (failure) {
        _check(generation);
        if (failure.terminal) {
          await _terminate(generation);
          rethrow;
        }
        if (failure.status == 409 &&
            const {
              'stale_refresh',
              'refresh_retry_expired',
            }.contains(failure.code)) {
          final saved = await _serial(generation, _store.read);
          _check(generation);
          if (saved != null) {
            final recovered = _stored(saved);
            if (recovered['session_id'] == snapshot['session_id'] &&
                object(recovered['customer'])['id'] ==
                    object(snapshot['customer'])['id'] &&
                recovered['refresh_token'] != snapshot['refresh_token']) {
              _snapshot = recovered;
              return string(recovered, 'access_token');
            }
          }
          await _terminate(generation);
          rethrow;
        }
        if (!failure.transient) rethrow;
        final backoff = Duration(
          milliseconds: (1 << attempt) * 250 + Random.secure().nextInt(250),
        );
        final wait = failure.retryAfter != null && failure.retryAfter! > backoff
            ? failure.retryAfter!
            : backoff;
        pending = {
          ...pending,
          'retry_not_before': _clock().toUtc().add(wait).toIso8601String(),
        };
        snapshot = {...snapshot, 'pending_refresh': pending};
        await _save(generation, snapshot);
        if (attempt == 2) rethrow;
      }
    }
    throw const ApiFailure('retry_later');
  }

  Future<Json> authorized(String method, String path) async {
    final generation = _generation;
    var token = await validAccessToken();
    _check(generation);
    for (var attempt = 0; attempt < 2; attempt++) {
      try {
        final response = await _call(method, path, accessToken: token);
        _check(generation);
        _onAuthenticatedResponse?.call(response);
        return response;
      } on ApiFailure catch (failure) {
        _check(generation);
        if (failure.terminal) {
          await _terminate(generation);
          rethrow;
        }
        if (attempt != 0 ||
            failure.status != 401 ||
            !const {
              'invalid_token',
              'access_token_expired',
            }.contains(failure.code))
          rethrow;
        token = _snapshot?['access_token'] != token
            ? await validAccessToken()
            : await _startRefresh();
        _check(generation);
      }
    }
    throw const ApiFailure('invalid_token', status: 401);
  }

  Future<void> logout() async {
    final old = _snapshot;
    final generation = ++_generation;
    _onSessionReset?.call();
    _snapshot = null;
    _refresh = null;
    _restore = null;
    await _clear(generation);
    if (old != null) {
      try {
        await _call(
          'POST',
          '/auth/logout',
          accessToken: string(old, 'access_token'),
        );
      } catch (_) {
        throw const ApiFailure('logout_remote_unconfirmed');
      }
    }
  }
}
