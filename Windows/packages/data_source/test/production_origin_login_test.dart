import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:test/test.dart';

import 'api_v2_data_source_test.dart'
    show FakeTransport, error, login, node, ok;
import 'session_coordinator_test.dart'
    show MemoryStore, device, now, tokens, uuid;

const _oldEnvironment = 'https://client-api.sunip.cc/api/client/v1';
const _productionEnvironment = 'https://client-api.sunip.vip/api/client/v1';

ApiV2DataSource _source(
  String environment,
  MemoryStore store,
  HttpTransport transport, {
  bool allowLoopbackHttp = false,
}) => ApiV2DataSource(
  baseUri: Uri.parse(environment),
  store: store,
  transport: transport,
  allowLoopbackHttp: allowLoopbackHttp,
  clock: () => now,
  requestId: () => uuid,
  delay: (_) async {},
);

Matcher _failure(String code) =>
    isA<ApiFailure>().having((failure) => failure.code, 'code', code);

void main() {
  test(
    'real login wire sends only login DTO, no claimed IP or forwarding headers',
    () async {
      // Ephemeral loopback server only; no production login, real account,
      // system proxy changes, or use of the user's existing proxy port.
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      final received =
          Completer<
            ({
              String method,
              String path,
              Object? body,
              Map<String, List<String>> headers,
            })
          >();
      var hits = 0;
      server.listen((request) async {
        hits++;
        final body = jsonDecode(await utf8.decoder.bind(request).join());
        final headers = <String, List<String>>{};
        request.headers.forEach(
          (name, values) => headers[name.toLowerCase()] = List.of(values),
        );
        if (!received.isCompleted) {
          received.complete((
            method: request.method,
            path: request.uri.path,
            body: body,
            headers: headers,
          ));
        }
        request.response.headers.contentType = ContentType.json;
        request.response.add(ok(tokens('wire-fixture')).body);
        await request.response.close();
      });
      final source = _source(
        'http://127.0.0.1:${server.port}/api/client/v1',
        MemoryStore(),
        IoHttpTransport(allowLoopbackHttp: true),
        allowLoopbackHttp: true,
      );
      await login(source);
      final wire = await received.future;
      expect(wire.method, 'POST');
      expect(wire.path, '/api/client/v1/auth/login');
      expect(wire.body, {
        'username': 'fake-user',
        'password': 'FAKE_PASSWORD',
        'device_id': device,
        'device_name': 'BlueGrass Windows',
      });
      expect((wire.body as Map).containsKey('login_ip'), isFalse);
      expect((wire.body as Map).containsKey('client_ip'), isFalse);
      expect(wire.headers['authorization'], isNull);
      expect(
        wire.headers.keys.where((name) => name.startsWith('x-forwarded-')),
        isEmpty,
      );
      for (final name in [
        'forwarded',
        'x-real-ip',
        'client-ip',
        'x-client-ip',
        'x-cluster-client-ip',
        'true-client-ip',
        'cf-connecting-ip',
      ]) {
        expect(wire.headers.containsKey(name), isFalse, reason: name);
      }
      expect(source.customer!.id, '123');
      // Server-side login IP/history is neither required in the response nor
      // queried through an invented follow-up endpoint.
      expect(hits, 1);
    },
  );

  for (final state in ['valid_access', 'expired_access', 'pending_refresh']) {
    test(
      'production never sends former environment credentials: $state',
      () async {
        final store = MemoryStore();
        final previousTransport = FakeTransport((request) async {
          if (request.uri.path.endsWith('/auth/login')) {
            return ok(
              tokens('old-environment', expired: state != 'valid_access'),
            );
          }
          expect(request.uri.path, '/api/client/v1/auth/refresh');
          return error(429, 'rate_limited', retryAfter: '180');
        });
        final previous = _source(_oldEnvironment, store, previousTransport);
        await login(previous);
        if (state == 'pending_refresh') {
          await expectLater(
            previous.getNode('00456'),
            throwsA(_failure('retry_later')),
          );
          expect(store.value!['pending_refresh'], isNotNull);
        }
        final original = await store.read();
        final productionTransport = FakeTransport((request) async {
          expect(request.uri.origin, 'https://client-api.sunip.vip');
          if (request.uri.path.endsWith('/auth/login')) {
            expect(request.headers, {
              'Accept': 'application/json',
              'Content-Type': 'application/json',
            });
            expect(request.body, {
              'username': 'fake-user',
              'password': 'FAKE_PASSWORD',
              'device_id': device,
              'device_name': 'BlueGrass Windows',
            });
            return ok(tokens('production', account: 456));
          }
          expect(request.uri.path, '/api/client/v1/nodes/00456');
          expect(
            request.headers['Authorization'],
            'Bearer FAKE_ACCESS_production',
          );
          return ok(node());
        });
        final production = _source(
          _productionEnvironment,
          store,
          productionTransport,
        );
        await expectLater(
          production.restore(),
          throwsA(_failure('environment_mismatch')),
        );
        await expectLater(
          production.getNode('00456'),
          throwsA(_failure('not_authenticated')),
        );
        expect(production.customer, isNull);
        expect(production.effectiveNowUtc, isNull);
        expect(productionTransport.requests, isEmpty);
        expect(
          store.value,
          original,
        ); // No implicit migration or destructive clearing.

        // Only an explicit new login creates a production session. It does not
        // refresh/revoke the former environment's token against the new origin.
        await login(production);
        expect(production.customer!.id, '456');
        expect(store.value!['environment'], _productionEnvironment);
        expect(store.value!['pending_refresh'], isNull);
        await production.getNode('00456');
        expect(productionTransport.requests, hasLength(2));
      },
    );
  }
}
