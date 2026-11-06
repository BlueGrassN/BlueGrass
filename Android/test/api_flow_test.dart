import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:client/app_controller.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

class ContractTransport implements HttpTransport {
  Map<String, Object?> get nodeData => {
    'id': '1',
    'name': '真实适配器测试',
    'protocol': 'socks5',
    'host': 'entry.example.invalid',
    'port': 1080,
    'username': 'FAKE_USER',
    'password': 'FAKE_NODE_PASSWORD',
    'country_code': 'HK',
    'country_name': '中国香港',
    'city': '',
    'expires_at': null,
    'is_forwarded': true,
  };
  Completer<void>? holdLogin;
  final loginStarted = Completer<void>();
  final loginBodies = <Map<String, Object?>>[];
  ApiResponse ok(Object? data) => ApiResponse(
    status: 200,
    body: utf8.encode(
      jsonEncode({
        'success': true,
        'data': data,
        'server_time': testNow.toIso8601String(),
      }),
    ),
  );
  @override
  Future<ApiResponse> send(ApiRequest request) async {
    switch (request.uri.path.split('/').last) {
      case '1':
        expect(request.headers['Authorization'], 'Bearer FAKE_ACCESS');
        return ok(nodeData);
      case 'login':
        loginBodies.add(request.body!);
        if (!loginStarted.isCompleted) loginStarted.complete();
        await holdLogin?.future;
        return ok({
          'token_type': 'Bearer',
          'access_token': 'FAKE_ACCESS',
          'refresh_token': 'FAKE_REFRESH',
          'session_id': 'FAKE_SESSION',
          'expires_in': 1800,
          'access_expires_at': testNow
              .add(const Duration(minutes: 30))
              .toIso8601String(),
          'refresh_expires_at': testNow
              .add(const Duration(days: 180))
              .toIso8601String(),
          'customer': {'id': 1, 'username': 'demo', 'display_name': '测试用户'},
        });
      case 'capabilities':
        return ok({
          'connectable_nodes': true,
          'resource_queries': true,
          'all_owned_resources': false,
          'expiry_summaries': true,
          'sms_login': false,
          'ownership_coverage': 'conservative',
          'resource_kinds': ['subscription', 'asset'],
        });
      case 'nodes':
        expect(request.headers['Authorization'], 'Bearer FAKE_ACCESS');
        return ok({
          'items': [
            {
              'id': '1',
              'name': '真实适配器测试',
              'protocol': 'socks5',
              'host': 'entry.example.invalid',
              'port': 1080,
              'username': 'FAKE_USER',
              'password': 'FAKE_NODE_PASSWORD',
              'country_code': 'HK',
              'country_name': '中国香港',
              'city': '',
              'expires_at': null,
              'is_forwarded': true,
            },
          ],
          'pagination': {
            'has_more': false,
            'next_cursor': null,
            'per_page': 50,
          },
        });
      case 'logout':
        return ok(null);
      default:
        throw StateError('unexpected fixture route');
    }
  }
}

void main() {
  ApiV2DataSource source(MemoryStore store, ContractTransport transport) =>
      ApiV2DataSource(
        baseUri: Uri.parse('https://example.invalid/api/client/v1'),
        store: store,
        transport: transport,
        clock: () => testNow,
      );
  test('real API adapter persists session, restores in new controller, clears on logout', () async {
    final store = MemoryStore();
    final transport = ContractTransport();
    final first = AppController(
      source: source(store, transport),
      store: store,
      clock: () => testNow,
    );
    addTearDown(first.dispose);
    await first.initialize();
    await first.login(' demo ', 'FAKE_LOGIN_PASSWORD');
    expect(first.authError, isNull);
    expect(first.resourceError, isNull);
    expect(first.selected?.name, '真实适配器测试');
    expect(transport.loginBodies.single['device_name'], 'BlueGrass Android');
    expect(transport.loginBodies.single['username'], 'demo');
    final saved = jsonEncode(store.snapshot);
    expect(saved, contains('FAKE_REFRESH'));
    expect(saved, isNot(contains('FAKE_LOGIN_PASSWORD')));
    expect(saved, isNot(contains('FAKE_NODE_PASSWORD')));
    final next = AppController(
      source: source(store, transport),
      store: store,
      clock: () => testNow,
    );
    addTearDown(next.dispose);
    await next.initialize();
    expect(next.customer?.id, '1');
    expect(next.selected?.key.sourceId, '1');
    expect(transport.loginBodies, hasLength(1));
    await next.logout();
    expect(store.snapshot, isNull);
    expect(next.customer, isNull);
  });
  test(
    'logout during real pending login never persists late credentials',
    () async {
      final store = MemoryStore();
      final transport = ContractTransport()..holdLogin = Completer<void>();
      final controller = AppController(
        source: source(store, transport),
        store: store,
        clock: () => testNow,
      );
      addTearDown(controller.dispose);
      await controller.initialize();
      final login = controller.login('demo', 'FAKE_PASSWORD');
      await transport.loginStarted.future;
      await controller.logout();
      transport.holdLogin!.complete();
      await login;
      expect(controller.customer, isNull);
      expect(controller.resources, isEmpty);
      expect(store.snapshot, isNull);
      expect(controller.authBusy, isFalse);
    },
  );
}
