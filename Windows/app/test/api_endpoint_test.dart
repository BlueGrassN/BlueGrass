import 'dart:convert';

import 'package:bluegrass_client/api_endpoint.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';

class ConfigStore implements SessionStore {
  Map<String, Object?>? value;
  bool failWrites = false;
  @override
  Future<Map<String, Object?>?> read() async => value;
  @override
  Future<void> replace(Map<String, Object?> snapshot) async {
    if (failWrites) throw StateError('storage unavailable');
    value = snapshot;
  }

  @override
  Future<void> clear() async => value = null;
}

class ConfigTransport implements HttpTransport {
  ConfigTransport(this.response);
  final ApiResponse response;
  final requests = <ApiRequest>[];
  @override
  Future<ApiResponse> send(ApiRequest request) async {
    requests.add(request);
    return response;
  }
}

Map<String, Object?> config(int revision, [String? url]) => {
  'version': 1,
  'revision': revision,
  'api_base_url': url ?? defaultApiUrl,
};
ApiResponse jsonResponse(Object value, {int status = 200}) =>
    ApiResponse(status: status, body: utf8.encode(jsonEncode(value)));

void main() {
  const next = 'https://api-next.sunip.vip/api/client/v1';
  test(
    'shared anonymous bootstrap selects and caches a new API origin',
    () async {
      final store = ConfigStore();
      final transport = ConfigTransport(jsonResponse(config(2, next)));
      expect(
        await loadApiEndpoint(store: store, transport: transport),
        Uri.parse(next),
      );
      expect(store.value, config(2, next));
      final request = transport.requests.single;
      expect(
        request.uri.toString(),
        'https://client-api.sunip.vip/client-config/config.json',
      );
      expect(request.method, 'GET');
      expect(request.headers, {'Accept': 'application/json'});
      expect(request.body, isNull);
    },
  );
  test(
    'missing, redirect, invalid and oversized config retain the cache',
    () async {
      for (final response in [
        jsonResponse({}, status: 404),
        jsonResponse(config(3), status: 302),
        const ApiResponse(status: 200, body: [123]),
        ApiResponse(status: 200, body: List.filled(4097, 32)),
        jsonResponse({...config(3), 'unexpected': true}),
        jsonResponse(config(3, 'http://api-next.sunip.vip/api/client/v1')),
      ]) {
        final store = ConfigStore()..value = config(2, next);
        expect(
          await loadApiEndpoint(
            store: store,
            transport: ConfigTransport(response),
          ),
          Uri.parse(next),
        );
        expect(store.value, config(2, next));
      }
    },
  );
  test('revision cannot roll back or replace an equal revision', () async {
    final store = ConfigStore()..value = config(2, next);
    for (final revision in [1, 2]) {
      expect(
        await loadApiEndpoint(
          store: store,
          transport: ConfigTransport(jsonResponse(config(revision))),
        ),
        Uri.parse(next),
      );
    }
    expect(
      await loadApiEndpoint(
        store: store,
        transport: ConfigTransport(jsonResponse(config(3))),
      ),
      Uri.parse(defaultApiUrl),
    );
  });
  test(
    'protected startup uses cache without any bootstrap network request',
    () async {
      final store = ConfigStore()..value = config(2, next);
      final transport = ConfigTransport(jsonResponse(config(3)));
      expect(
        await loadApiEndpoint(
          store: store,
          transport: transport,
          allowNetwork: false,
        ),
        Uri.parse(next),
      );
      expect(transport.requests, isEmpty);
    },
  );
  test(
    'fresh or damaged cache falls back to production while config is absent',
    () async {
      for (final cached in [
        null,
        <String, Object?>{'version': 'bad'},
      ]) {
        final store = ConfigStore()..value = cached;
        expect(
          await loadApiEndpoint(
            store: store,
            transport: ConfigTransport(jsonResponse({}, status: 404)),
          ),
          Uri.parse(defaultApiUrl),
        );
      }
    },
  );
  test(
    'failed cache write still uses authenticated config for this launch',
    () async {
      final store = ConfigStore()..failWrites = true;
      expect(
        await loadApiEndpoint(
          store: store,
          transport: ConfigTransport(jsonResponse(config(2, next))),
        ),
        Uri.parse(next),
      );
    },
  );
  test('only canonical HTTPS API DNS endpoints are accepted', () {
    for (final url in [
      'https://127.0.0.1/api/client/v1',
      'https://localhost/api/client/v1',
      'https://api.local/api/client/v1',
      'https://api.sunip.vip:444/api/client/v1',
      'https://api.sunip.vip:443/api/client/v1',
      'https://u@api.sunip.vip/api/client/v1',
      'https://api.sunip.vip/api/client/v1?x=1',
      'https://api.sunip.vip/api/client/v1#x',
      'https://api.sunip.vip/other',
      'https://api.sunip.vip/api/client/v1/',
      'https://api..sunip.vip/api/client/v1',
    ]) {
      expect(
        () => ApiEndpointConfig.parse(config(1, url)),
        throwsFormatException,
      );
    }
  });
}
