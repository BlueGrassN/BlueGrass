import 'dart:async';
import 'dart:io';

import 'package:bluegrass_client/control_http_transport.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('bluegrass/platform');
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final calls = <MethodCall>[];
  final request = ApiRequest(
    method: 'GET',
    uri: Uri.parse('https://client-api.sunip.vip/api/client/v1/health'),
  );
  Map<String, Object> response({int status = 200}) => {
    'status': status,
    'headers': <String, String>{'content-type': 'application/json'},
    'body': Uint8List.fromList([123, 125]),
  };
  Matcher failure(String code) =>
      isA<ApiFailure>().having((e) => e.code, 'code', code);
  ControlHttpTransport transport({
    Future<List<InternetAddress>?> Function(String)? resolve,
    Duration timeout = const Duration(seconds: 20),
  }) => ControlHttpTransport(
    resolveHost: resolve ?? (_) async => [InternetAddress('1.1.1.1')],
    timeout: timeout,
  );
  setUp(() {
    calls.clear();
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return call.method == 'controlHttpSend' ? response() : null;
    });
  });
  tearDown(() => messenger.setMockMethodCallHandler(channel, null));

  test(
    'startup configuration pins a new origin before sending requests',
    () async {
      final t = transport();
      final endpoint = Uri.parse('https://api-next.sunip.vip/api/client/v1');
      await t.configure(endpoint);
      expect(calls.single.method, 'controlHttpConfigure');
      expect(calls.single.arguments, {'host': 'api-next.sunip.vip'});
      await expectLater(t.send(request), throwsA(failure('insecure_endpoint')));
      await t.send(
        ApiRequest(
          method: 'GET',
          uri: endpoint.resolve('${endpoint.path}/health'),
        ),
      );
      expect(calls.last.method, 'controlHttpSend');
      expect((calls.last.arguments as Map).containsKey('host'), isFalse);
      t.dispose();
    },
  );

  test('retired API origin is rejected in both network modes', () async {
    for (final active in [false, true]) {
      final fallback = _Fallback();
      fallback.pending.complete(const ApiResponse(status: 200, body: []));
      final t = ControlHttpTransport(
        resolveHost: (_) => throw StateError('retired host must not resolve'),
        protectionActive: () => active,
        unprotectedTransport: fallback,
      );
      await expectLater(
        t.send(
          ApiRequest(
            method: 'GET',
            uri: Uri.parse('https://test-api.dos.im/api/client/v1/me'),
            headers: {'Authorization': 'Bearer synthetic-old-session'},
          ),
        ),
        throwsA(failure('insecure_endpoint')),
      );
      expect(fallback.calls, 0);
      expect(calls, isEmpty);
      t.dispose();
    }
  });

  test(
    'unprotected entry delegates once; mode captured before async work',
    () async {
      var active = false;
      var modeReads = 0;
      final fallback = _Fallback();
      final t = ControlHttpTransport(
        resolveHost: (_) => throw StateError('protected resolver must not run'),
        protectionActive: () {
          modeReads++;
          return active;
        },
        unprotectedTransport: fallback,
      );
      final pending = t.send(request);
      active = true;
      fallback.pending.complete(const ApiResponse(status: 204, body: []));
      expect((await pending).status, 204);
      expect(modeReads, 1);
      expect(fallback.calls, 1);
      expect(calls, isEmpty);
      t.dispose();
    },
  );
  test('active null resolution never reaches unprotected fallback', () async {
    final fallback = _Fallback();
    final t = ControlHttpTransport(
      resolveHost: (_) async => null,
      protectionActive: () => true,
      unprotectedTransport: fallback,
    );
    await expectLater(t.send(request), throwsA(failure('control_dns_failed')));
    expect(fallback.calls, 0);
    expect(calls, isEmpty);
    t.dispose();
  });
  test('route failure cannot select fallback or retry another IP', () async {
    final fallback = _Fallback();
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      throw PlatformException(code: 'physical_route_unavailable');
    });
    final t = ControlHttpTransport(
      resolveHost: (_) async => [
        InternetAddress('1.1.1.1'),
        InternetAddress('8.8.8.8'),
      ],
      unprotectedTransport: fallback,
    );
    await expectLater(
      t.send(request),
      throwsA(failure('physical_route_unavailable')),
    );
    expect(calls, hasLength(1));
    expect(fallback.calls, 0);
    t.dispose();
  });
  test('unprotected late result is discarded on owner disposal', () async {
    final fallback = _Fallback();
    final t = ControlHttpTransport(
      resolveHost: (_) async => null,
      protectionActive: () => false,
      unprotectedTransport: fallback,
    );
    final pending = t.send(request);
    t.dispose();
    await expectLater(pending, throwsA(failure('cancelled')));
    fallback.pending.complete(const ApiResponse(status: 200, body: []));
    expect(calls, isEmpty);
  });

  test(
    'fixed TLS origin and service numeric destination reach native separately',
    () async {
      final t = transport(
        resolve: (host) async {
          expect(host, 'client-api.sunip.vip');
          return [InternetAddress('1.1.1.1')];
        },
      );
      final result = await t.send(
        ApiRequest(
          method: 'POST',
          uri: request.uri,
          headers: {'Authorization': 'Bearer synthetic-test-value'},
          body: {'hello': 'world'},
        ),
      );
      expect(result.status, 200);
      expect(result.body, [123, 125]);
      final payload = calls.single.arguments as Map;
      expect(payload['ip'], '1.1.1.1');
      expect(payload['target'], '/api/client/v1/health');
      expect(
        payload['body'],
        Uint8List.fromList('{"hello":"world"}'.codeUnits),
      );
      expect(
        payload.containsKey('host'),
        isFalse,
      ); // Host is compiled into native code.
      t.dispose();
    },
  );
  test(
    'foreign origin, redirect targets and header injection cannot leave GUI',
    () async {
      final t = transport();
      for (final uri in [
        'http://client-api.sunip.vip/api/client/v1/health',
        'https://other.invalid/api/client/v1/health',
        'https://client-api.sunip.vip:444/api/client/v1/health',
        'https://u@client-api.sunip.vip/api/client/v1/health',
        'https://client-api.sunip.vip/other',
        'https://client-api.sunip.vip/api/client/v1/health#fragment',
      ]) {
        await expectLater(
          t.send(ApiRequest(method: 'GET', uri: Uri.parse(uri))),
          throwsA(failure('insecure_endpoint')),
        );
      }
      for (final headers in [
        {'Host': 'other.invalid'},
        {'Authorization': 'x\r\nInjected: yes'},
        {'Proxy-Authorization': 'x'},
        {'X-Real-IP': '203.0.113.1'},
        {'X-Forwarded-For': '203.0.113.1'},
        {'Forwarded': 'for=203.0.113.1'},
      ]) {
        await expectLater(
          t.send(ApiRequest(method: 'GET', uri: request.uri, headers: headers)),
          throwsA(failure('invalid_request')),
        );
      }
      expect(calls, isEmpty);
      t.dispose();
    },
  );
  test(
    'null empty oversized or nonphysical resolution fails with no fallback',
    () async {
      for (final addresses in <List<InternetAddress>?>[
        null,
        [],
        List.filled(9, InternetAddress('1.1.1.1')),
        [InternetAddress.loopbackIPv4],
        [InternetAddress('198.18.0.1')],
      ]) {
        final t = transport(resolve: (_) async => addresses);
        await expectLater(
          t.send(request),
          throwsA(failure('control_dns_failed')),
        );
        t.dispose();
      }
      expect(calls.where((c) => c.method == 'controlHttpSend'), isEmpty);
    },
  );
  test(
    'UTF8 body size bound checked before resolver or native request',
    () async {
      final t = transport(
        resolve: (_) => throw StateError('resolver must not run'),
      );
      await expectLater(
        t.send(
          ApiRequest(method: 'POST', uri: request.uri, body: {'x': '界' * 1400}),
        ),
        throwsA(failure('payload_too_large')),
      );
      expect(calls, isEmpty);
      t.dispose();
    },
  );
  test(
    'redirect is returned once without another destination or request',
    () async {
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        return response(status: 302);
      });
      final t = transport();
      expect((await t.send(request)).status, 302);
      expect(calls, hasLength(1));
      t.dispose();
    },
  );
  test('native failure details never become public exceptions', () async {
    messenger.setMockMethodCallHandler(
      channel,
      (_) => throw PlatformException(
        code: 'arbitrary-secret',
        message: 'synthetic-token',
        details: request.uri.toString(),
      ),
    );
    final t = transport();
    await expectLater(t.send(request), throwsA(failure('network_error')));
    t.dispose();
  });
  test('malformed and oversized native responses are rejected', () async {
    final t = transport();
    for (final value in [
      null,
      {'status': 999, 'body': Uint8List(0), 'headers': {}},
      {'status': 200, 'body': Uint8List(2097153), 'headers': {}},
    ]) {
      messenger.setMockMethodCallHandler(channel, (_) async => value);
      await expectLater(t.send(request), throwsA(isA<ApiFailure>()));
    }
    t.dispose();
  });
  test('dispose while DNS is pending prevents late native request', () async {
    final dns = Completer<List<InternetAddress>?>();
    final t = transport(resolve: (_) => dns.future);
    final pending = t.send(request);
    t.dispose();
    await expectLater(pending, throwsA(failure('cancelled')));
    dns.complete([InternetAddress('1.1.1.1')]);
    await Future<void>.delayed(Duration.zero);
    expect(calls.where((c) => c.method == 'controlHttpSend'), isEmpty);
    await expectLater(t.send(request), throwsA(failure('cancelled')));
  });
  test(
    'dispose cancels only this instance, ignores late native completion',
    () async {
      final native = Completer<Object?>();
      messenger.setMockMethodCallHandler(channel, (call) async {
        calls.add(call);
        return call.method == 'controlHttpSend' ? native.future : null;
      });
      final a = transport(), b = transport();
      final pa = a.send(request), pb = b.send(request);
      await Future<void>.delayed(Duration.zero);
      a.dispose();
      await expectLater(pa, throwsA(failure('cancelled')));
      final sends = calls.where((c) => c.method == 'controlHttpSend').toList();
      expect(sends, hasLength(2));
      final cancels = calls
          .where((c) => c.method == 'controlHttpCancel')
          .toList();
      expect(cancels, hasLength(1));
      expect(
        (cancels.single.arguments as Map)['id'],
        (sends.first.arguments as Map)['id'],
      );
      native.complete(response());
      expect((await pb).status, 200);
      b.dispose();
    },
  );
  test('deadline includes DNS and prevents a late dial', () async {
    final dns = Completer<List<InternetAddress>?>();
    final t = transport(
      resolve: (_) => dns.future,
      timeout: const Duration(milliseconds: 25),
    );
    await expectLater(t.send(request), throwsA(failure('timeout')));
    dns.complete([InternetAddress('1.1.1.1')]);
    await Future<void>.delayed(Duration.zero);
    expect(calls.where((c) => c.method == 'controlHttpSend'), isEmpty);
    t.dispose();
  });
}

final class _Fallback implements HttpTransport {
  final pending = Completer<ApiResponse>();
  int calls = 0;
  @override
  Future<ApiResponse> send(ApiRequest request) {
    calls++;
    return pending.future;
  }
}
