import 'dart:io';
import 'package:test/test.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';

void main() {
  test(
    'explicit loopback proxy forwards only this transport without OS changes',
    () async {
      final proxy = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => proxy.close(force: true));
      var hits = 0;
      proxy.listen((request) async {
        hits++;
        expect(request.requestedUri.host, '127.0.0.1');
        request.response.write('through explicit proxy');
        await request.response.close();
      });
      final response = await IoHttpTransport(
        allowLoopbackHttp: true,
        explicitProxy: Uri.parse('http://127.0.0.1:${proxy.port}'),
      ).send(ApiRequest(method: 'GET', uri: Uri.parse('http://127.0.0.1:1/')));
      expect(response.status, 200);
      expect(hits, 1);
    },
  );
  test(
    'explicit proxy rejects remote hosts credentials and non HTTP schemes',
    () async {
      for (final value in [
        'http://example.com:8080',
        'http://user:secret@127.0.0.1:8080',
        'socks5://127.0.0.1:8080',
        'http://127.0.0.1:8080/path',
      ]) {
        await expectLater(
          IoHttpTransport(explicitProxy: Uri.parse(value)).send(
            ApiRequest(method: 'GET', uri: Uri.parse('https://example.com')),
          ),
          throwsA(
            isA<ApiFailure>().having(
              (e) => e.code,
              'code',
              'invalid_explicit_proxy',
            ),
          ),
        );
      }
    },
  );
  test('HTTP requires explicit loopback opt in even on localhost', () async {
    final transport = IoHttpTransport();
    await expectLater(
      transport.send(
        ApiRequest(method: 'GET', uri: Uri.parse('http://127.0.0.1:1/')),
      ),
      throwsA(isA<ApiFailure>()),
    );
    await expectLater(
      IoHttpTransport(
        allowLoopbackHttp: true,
      ).send(ApiRequest(method: 'GET', uri: Uri.parse('http://example.com/'))),
      throwsA(isA<ApiFailure>()),
    );
  });
  test(
    'real io response and redirect never follows or forwards credentials',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      var hits = 0;
      server.listen((r) async {
        hits++;
        expect(r.headers.value('authorization'), 'Bearer FAKE_TOKEN');
        r.response.statusCode = 302;
        r.response.headers.set(
          'location',
          'http://127.0.0.1:${server.port}/steal',
        );
        await r.response.close();
      });
      final response = await IoHttpTransport(allowLoopbackHttp: true).send(
        ApiRequest(
          method: 'GET',
          uri: Uri.parse('http://127.0.0.1:${server.port}/'),
          headers: {'Authorization': 'Bearer FAKE_TOKEN'},
        ),
      );
      expect(response.status, 302);
      expect(hits, 1);
    },
  );
  test(
    'bounded response includes streamed bodies without content length',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      server.listen((r) async {
        r.response.add(List.filled(1024, 65));
        await r.response.close();
      });
      await expectLater(
        IoHttpTransport(allowLoopbackHttp: true, maxResponseBytes: 64).send(
          ApiRequest(
            method: 'GET',
            uri: Uri.parse('http://127.0.0.1:${server.port}/'),
          ),
        ),
        throwsA(
          isA<ApiFailure>().having((e) => e.code, 'code', 'response_too_large'),
        ),
      );
    },
  );
  test('total request timeout aborts stalled server', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((_) {});
    await expectLater(
      IoHttpTransport(
        allowLoopbackHttp: true,
        timeout: const Duration(milliseconds: 50),
      ).send(
        ApiRequest(
          method: 'GET',
          uri: Uri.parse('http://127.0.0.1:${server.port}/'),
        ),
      ),
      throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'timeout')),
    );
  });
}
