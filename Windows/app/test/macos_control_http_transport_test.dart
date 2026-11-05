import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_client/macos_control_http_transport.dart';
import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter_test/flutter_test.dart';

const origin = 'https://api.example.com/api/client/v1';
ApiRequest request([String path = '/resources']) => ApiRequest(
  method: 'GET',
  uri: Uri.parse('$origin$path'),
  headers: const {'authorization': 'Bearer TEST_ONLY'},
);

class PlainTransport implements HttpTransport {
  int calls = 0;
  @override
  Future<ApiResponse> send(ApiRequest request) async {
    calls++;
    return const ApiResponse(status: 204, body: []);
  }
}

void main() {
  late HttpServer server;
  late SecurityContext trusted;
  final received = <HttpRequest>[];
  late Future<void> Function(HttpRequest) respond;
  setUp(() async {
    final serverContext = SecurityContext()
      ..useCertificateChain('test/fixtures/control-tls/server.pem')
      ..usePrivateKey('test/fixtures/control-tls/server-key.pem');
    trusted = SecurityContext(withTrustedRoots: false)
      ..setTrustedCertificates('test/fixtures/control-tls/ca.pem');
    server = await HttpServer.bindSecure(
      InternetAddress.loopbackIPv4,
      0,
      serverContext,
    );
    received.clear();
    respond = (r) async {
      r.response.headers.contentType = ContentType.json;
      r.response.write('{"ok":true}');
      await r.response.close();
    };
    server.listen((r) {
      received.add(r);
      unawaited(respond(r));
    }, onError: (Object _) {});
  });
  tearDown(() async {
    await server.close(force: true);
  });

  test(
    'loopback TLS fixture passes platform trust with its explicit test CA',
    () async {
      final raw = await Socket.connect(
        InternetAddress.loopbackIPv4,
        server.port,
      );
      final secure = await SecureSocket.secure(
        raw,
        host: 'api.example.com',
        context: trusted,
      ).timeout(const Duration(seconds: 5));
      secure.destroy();
    },
  );

  MacControlHttpTransport transport({
    SecurityContext? securityContext,
    bool Function()? active,
    PlainTransport? direct,
    Future<List<InternetAddress>?> Function(String)? resolve,
    Duration timeout = const Duration(seconds: 2),
    int maxResponseBytes = 2 * 1024 * 1024,
    void Function(InternetAddress, int)? onDial,
  }) => MacControlHttpTransport(
    resolveHost: resolve ?? (_) async => [InternetAddress('1.1.1.1')],
    protectionActive: active ?? () => true,
    unprotectedTransport: direct ?? PlainTransport(),
    securityContext: securityContext,
    timeout: timeout,
    maxResponseBytes: maxResponseBytes,
    connect: (ip, port) {
      onDial?.call(ip, port);
      return RawSocket.startConnect(InternetAddress.loopbackIPv4, server.port);
    },
  );

  test(
    'pinned numeric connection preserves original TLS host and HTTP authority',
    () async {
      final dials = <String>[];
      final t = transport(
        securityContext: trusted,
        onDial: (ip, port) => dials.add('${ip.address}:$port'),
      );
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      final response = await t.send(request());
      expect(response.status, 200);
      expect(jsonDecode(utf8.decode(response.body)), {'ok': true});
      expect(dials, ['1.1.1.1:443']);
      expect(received.single.headers.host, 'api.example.com');
      expect(
        received.single.headers.value('authorization'),
        'Bearer TEST_ONLY',
      );
    },
  );

  test('a trusted CA cannot authorize the wrong TLS hostname', () async {
    final t = transport(securityContext: trusted);
    addTearDown(t.dispose);
    await t.configure(Uri.parse('https://other.example.com/api/client/v1'));
    await expectLater(
      t.send(
        ApiRequest(
          method: 'GET',
          uri: Uri.parse('https://other.example.com/api/client/v1/resources'),
        ),
      ),
      throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'tls_failed')),
    );
    expect(received, isEmpty);
  });

  test(
    'production trust rejects the fixture CA before sending credentials',
    () async {
      final t = transport();
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      await expectLater(
        t.send(request()),
        throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'tls_failed')),
      );
      expect(received, isEmpty);
    },
  );

  test(
    'redirect is returned without forwarding credentials elsewhere',
    () async {
      respond = (r) async {
        r.response.statusCode = 302;
        r.response.headers.set('location', 'https://elsewhere.example.com/');
        await r.response.close();
      };
      final t = transport(securityContext: trusted);
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      expect((await t.send(request())).status, 302);
      expect(received, hasLength(1));
    },
  );

  test('resolver failure never falls back to ordinary transport', () async {
    final direct = PlainTransport();
    var active = true;
    final t = transport(
      direct: direct,
      active: () => active,
      resolve: (_) async {
        active = false;
        throw const SocketException('private resolver detail');
      },
    );
    addTearDown(t.dispose);
    await t.configure(Uri.parse(origin));
    await expectLater(t.send(request()), throwsA(isA<ApiFailure>()));
    expect(direct.calls, 0);
    expect(received, isEmpty);
  });

  test(
    'ordinary API transport is used only when invoked without TUN protection',
    () async {
      final direct = PlainTransport();
      final t = transport(
        direct: direct,
        active: () => false,
        resolve: (_) => throw StateError('must not resolve'),
      );
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      expect((await t.send(request())).status, 204);
      expect(direct.calls, 1);
    },
  );

  test(
    'timeout prevents late resolver results from creating sockets',
    () async {
      final resolved = Completer<List<InternetAddress>?>();
      var dials = 0;
      final t = transport(
        timeout: const Duration(milliseconds: 30),
        resolve: (_) => resolved.future,
        onDial: (_, _) => dials++,
      );
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      await expectLater(
        t.send(request()),
        throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'timeout')),
      );
      resolved.complete([InternetAddress('1.1.1.1')]);
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(dials, 0);
    },
  );

  test(
    'dispose cancels a pending resolver and prevents late sockets',
    () async {
      final resolved = Completer<List<InternetAddress>?>();
      var dials = 0;
      final t = transport(
        resolve: (_) => resolved.future,
        onDial: (_, _) => dials++,
      );
      await t.configure(Uri.parse(origin));
      final result = t.send(request());
      final assertion = expectLater(
        result,
        throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'cancelled')),
      );
      t.dispose();
      await assertion;
      resolved.complete([InternetAddress('1.1.1.1')]);
      await Future<void>.delayed(const Duration(milliseconds: 30));
      expect(dials, 0);
    },
  );

  test(
    'private resolver answers and cross-origin requests are rejected',
    () async {
      final t = transport(resolve: (_) async => [InternetAddress.loopbackIPv4]);
      addTearDown(t.dispose);
      await t.configure(Uri.parse(origin));
      await expectLater(
        t.send(request()),
        throwsA(
          isA<ApiFailure>().having((e) => e.code, 'code', 'control_dns_failed'),
        ),
      );
      await expectLater(
        t.send(
          ApiRequest(
            method: 'GET',
            uri: Uri.parse('https://elsewhere.example.com/api/client/v1'),
          ),
        ),
        throwsA(
          isA<ApiFailure>().having((e) => e.code, 'code', 'insecure_endpoint'),
        ),
      );
      expect(received, isEmpty);
    },
  );

  for (final dispose in [false, true]) {
    test(
      '${dispose ? 'dispose' : 'timeout'} closes a stalled TLS handshake socket',
      () async {
        final listener = await ServerSocket.bind(
          InternetAddress.loopbackIPv4,
          0,
        );
        final closed = Completer<void>();
        final accepted = Completer<Socket>();
        final clientHello = Completer<void>();
        final helloBytes = <int>[];
        listener.listen((socket) {
          accepted.complete(socket);
          socket.listen(
            (bytes) {
              helloBytes.addAll(bytes);
              // Wait for the whole first TLS record, not just TCP acceptance.
              if (!clientHello.isCompleted &&
                  helloBytes.length >= 5 &&
                  helloBytes.length >=
                      5 + (helloBytes[3] << 8) + helloBytes[4]) {
                clientHello.complete();
              }
            },
            onDone: () {
              if (!closed.isCompleted) closed.complete();
            },
            onError: (Object _) {
              if (!closed.isCompleted) closed.complete();
            },
          );
        });
        final direct = PlainTransport();
        var active = true;
        final t = MacControlHttpTransport(
          resolveHost: (_) async => [InternetAddress('1.1.1.1')],
          protectionActive: () => active,
          unprotectedTransport: direct,
          timeout: dispose
              ? const Duration(seconds: 20)
              : const Duration(milliseconds: 500),
          connect: (_, _) => RawSocket.startConnect(
            InternetAddress.loopbackIPv4,
            listener.port,
          ),
        );
        try {
          await t.configure(Uri.parse(origin));
          final assertion = expectLater(
            t.send(request()),
            throwsA(
              isA<ApiFailure>().having(
                (e) => e.code,
                'code',
                dispose ? 'cancelled' : 'timeout',
              ),
            ),
          );
          await clientHello.future.timeout(const Duration(seconds: 2));
          // TLS server_name extension: list length 18, name type 0, 15-byte name.
          final hex = helloBytes
              .map((b) => b.toRadixString(16).padLeft(2, '0'))
              .join();
          expect(
            hex,
            contains('00000014001200000f6170692e6578616d706c652e636f6d'),
          );
          active = false;
          if (dispose) t.dispose();
          await assertion.timeout(const Duration(seconds: 2));
          await closed.future.timeout(const Duration(seconds: 2));
          expect(direct.calls, 0);
        } finally {
          t.dispose();
          if (accepted.isCompleted) (await accepted.future).destroy();
          await listener.close();
        }
      },
    );
  }

  test(
    'dispose closes a TCP connection returned late by the connector',
    () async {
      final listener = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final accepted = Completer<Socket>();
      final connected = Completer<RawSocket>();
      final closed = Completer<void>();
      final release = Completer<void>();
      final receivedBytes = <int>[];
      listener.listen((socket) {
        accepted.complete(socket);
        socket.listen(
          receivedBytes.addAll,
          onDone: () {
            if (!closed.isCompleted) closed.complete();
          },
          onError: (Object _) {
            if (!closed.isCompleted) closed.complete();
          },
        );
      });
      final t = MacControlHttpTransport(
        resolveHost: (_) async => [InternetAddress('1.1.1.1')],
        protectionActive: () => true,
        unprotectedTransport: PlainTransport(),
        connect: (_, _) async {
          final task = await RawSocket.startConnect(
            InternetAddress.loopbackIPv4,
            listener.port,
          );
          connected.complete(await task.socket);
          await release.future;
          return task;
        },
      );
      try {
        await t.configure(Uri.parse(origin));
        final assertion = expectLater(
          t.send(request()),
          throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'cancelled')),
        );
        await connected.future.timeout(const Duration(seconds: 2));
        t.dispose();
        t.dispose();
        await assertion.timeout(const Duration(seconds: 2));
        release.complete();
        await closed.future.timeout(const Duration(seconds: 2));
        expect(receivedBytes, isEmpty);
        await expectLater(
          t.send(request()),
          throwsA(isA<ApiFailure>().having((e) => e.code, 'code', 'cancelled')),
        );
      } finally {
        t.dispose();
        if (!release.isCompleted) release.complete();
        if (connected.isCompleted) await (await connected.future).close();
        if (accepted.isCompleted) (await accepted.future).destroy();
        await listener.close();
      }
    },
  );

  for (final phase in ['headers', 'body', 'oversized']) {
    for (final dispose in phase == 'oversized' ? [false] : [false, true]) {
      test(
        '${dispose ? 'dispose' : 'limit'} closes HTTPS while reading $phase',
        () async {
          final ready = Completer<void>();
          final closed = Completer<void>();
          Socket? peer;
          respond = (r) async {
            final socket = peer = await r.response.detachSocket(
              writeHeaders: false,
            );
            socket.listen(
              (_) {},
              onDone: () {
                if (!closed.isCompleted) closed.complete();
              },
              onError: (Object _) {
                if (!closed.isCompleted) closed.complete();
              },
            );
            if (phase != 'headers') {
              final length = phase == 'oversized' ? 3 * 1024 * 1024 : 1024;
              socket.write(
                'HTTP/1.1 200 OK\r\nContent-Length: $length\r\n\r\n',
              );
              await socket.flush();
            }
            ready.complete();
          };
          final t = transport(
            securityContext: trusted,
            timeout: dispose || phase == 'oversized'
                ? const Duration(seconds: 20)
                : const Duration(milliseconds: 500),
          );
          try {
            await t.configure(Uri.parse(origin));
            final code = phase == 'oversized'
                ? 'response_too_large'
                : dispose
                ? 'cancelled'
                : 'timeout';
            final assertion = expectLater(
              t.send(request()),
              throwsA(isA<ApiFailure>().having((e) => e.code, 'code', code)),
            );
            await ready.future.timeout(const Duration(seconds: 2));
            if (dispose) t.dispose();
            await assertion.timeout(const Duration(seconds: 2));
            await closed.future.timeout(const Duration(seconds: 2));
          } finally {
            t.dispose();
            peer?.destroy();
          }
        },
      );
    }
  }

  test('large HTTPS request and fragmented response keep every byte', () async {
    final requestBody = {'value': 'x' * 4000};
    final responseBody = List<int>.generate(128 * 1024, (i) => i % 251);
    respond = (r) async {
      expect(jsonDecode(await utf8.decoder.bind(r).join()), requestBody);
      for (var offset = 0; offset < responseBody.length; offset += 4096) {
        r.response.add(responseBody.sublist(offset, offset + 4096));
        await r.response.flush();
      }
      await r.response.close();
    };
    final t = transport(securityContext: trusted);
    addTearDown(t.dispose);
    await t.configure(Uri.parse(origin));
    final response = await t.send(
      ApiRequest(
        method: 'POST',
        uri: Uri.parse('$origin/resources?q=${'q' * 4000}'),
        headers: {
          'authorization': 'Bearer ${'a' * 4000}',
          'accept': 'a' * 4000,
          'content-type': 'application/json; padding=${'p' * 4000}',
        },
        body: requestBody,
      ),
    );
    expect(response.status, 200);
    expect(response.body, responseBody);
  });

  test('streamed response and request limits are enforced', () async {
    respond = (r) async {
      r.response.write('x' * 256);
      await r.response.close();
    };
    final t = transport(securityContext: trusted, maxResponseBytes: 128);
    addTearDown(t.dispose);
    await t.configure(Uri.parse(origin));
    await expectLater(
      t.send(request()),
      throwsA(
        isA<ApiFailure>().having((e) => e.code, 'code', 'response_too_large'),
      ),
    );
    await expectLater(
      t.send(
        ApiRequest(
          method: 'POST',
          uri: Uri.parse('$origin/a'),
          body: {'tooMuch': 'x' * 4096},
        ),
      ),
      throwsA(
        isA<ApiFailure>().having((e) => e.code, 'code', 'payload_too_large'),
      ),
    );
    await expectLater(
      t.send(
        ApiRequest(
          method: 'GET',
          uri: Uri.parse('$origin/a'),
          headers: {'host': 'other.example.com'},
        ),
      ),
      throwsA(
        isA<ApiFailure>().having((e) => e.code, 'code', 'invalid_request'),
      ),
    );
  });
}
