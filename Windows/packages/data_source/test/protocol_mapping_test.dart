import 'dart:convert';
import 'dart:io';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';

import 'api_v2_data_source_test.dart'
    show FakeTransport, login, node, ok, source;
import 'session_coordinator_test.dart' show tokens, now;

void main() {
  final vectors =
      jsonDecode(
            File(
              '../../contracts/fixtures/protocol/config-v1.json',
            ).readAsStringSync(),
          )
          as Map;
  for (final fixture in vectors['valid'] as List) {
    for (final list in [false, true]) {
      test(
        'maps ${fixture['id']} through ${list ? 'list' : 'detail'} without inventing credentials',
        () async {
          final config = ProtocolConfig.parse(fixture['json'] as String);
          final dto =
              {
                  ...node(),
                  'protocol': config.type,
                  'protocol_config': config.toJson(),
                }
                ..remove('username')
                ..remove('password');
          final transport = FakeTransport(
            (r) async => ok(
              r.uri.path.endsWith('/login')
                  ? tokens('old')
                  : list
                  ? {
                      'items': [dto],
                      'pagination': {'per_page': 50, 'has_more': false},
                    }
                  : dto,
            ),
          );
          final s = source(transport);
          await login(s);
          final actual = list
              ? (await s.listNodes()).items.single
              : await s.getNode('00456');
          expect(actual.protocolConfig, config);
          expect(actual.protocolConfig!.toJson(), config.toJson());
          expect(actual.username, '');
          expect(actual.password, '');
          expect(actual.expectedExitIpv4, isNull);
          expect(s.capabilities.authoritativeExpectedExit, isFalse);
          final profile = prepareProfile(actual, now);
          if (config.type == 'tuic' || config.type == 'anytls') {
            // Preserve valid API metadata while respecting the pinned Xray
            // runtime's protocol support (ADR 0049).
            expect(
              profile,
              isA<Rejected>().having(
                (value) => value.error.code,
                'code',
                'ELIG-008',
              ),
            );
          } else {
            expect(profile, isA<Ready>());
          }
        },
      );
    }
  }
  test(
    'old SOCKS response preserves absence and original credentials',
    () async {
      final transport = FakeTransport(
        (r) async => ok(r.uri.path.endsWith('/login') ? tokens('old') : node()),
      );
      final s = source(transport);
      await login(s);
      final actual = await s.getNode('00456');
      expect(actual.protocolConfig, isNull);
      expect(actual.username, 'FAKE_NODE_USER');
      expect(actual.password, 'FAKE_NODE_PASSWORD');
    },
  );
  test(
    'non-SOCKS response never infers a protocol config from legacy fields',
    () async {
      final transport = FakeTransport(
        (r) async => ok(
          r.uri.path.endsWith('/login')
              ? tokens('old')
              : {...node(), 'protocol': 'vless'},
        ),
      );
      final s = source(transport);
      await login(s);
      await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
    },
  );
  for (final fixture in vectors['invalid'] as List) {
    test('wire rejection stays a safe API error ${fixture['id']}', () async {
      final body = jsonEncode({
        'success': true,
        'data': {...node(), 'protocol_config': 'PLACEHOLDER'},
        'server_time': now.toIso8601String(),
      }).replaceFirst('"PLACEHOLDER"', fixture['json'] as String);
      final transport = FakeTransport(
        (r) async => r.uri.path.endsWith('/login')
            ? ok(tokens('old'))
            : ApiResponse(status: 200, body: utf8.encode(body)),
      );
      final s = source(transport);
      await login(s);
      try {
        await s.getNode('00456');
        fail('invalid config accepted');
      } on ApiFailure catch (failure) {
        expect(failure.code, 'invalid_response');
        expect('$failure', isNot(contains('fixture-password')));
        expect('$failure', isNot(contains('FAKE_')));
        expect(s.customer, isNotNull);
      }
    });
  }
  for (final config in <Object?>[
    null,
    {'version': 1, 'type': 'socks5', 'socks5': {}, 'route': {}},
    {'version': 1, 'type': 'http', 'http': {}},
    {
      'version': 1,
      'type': 'socks5',
      'socks5': {'username': 'different', 'password': 'fixture'},
    },
  ]) {
    test(
      'explicit malformed or conflicting protocol config is rejected ${config == null ? 'null' : (config as Map).keys.join(',')}',
      () async {
        final transport = FakeTransport(
          (r) async => ok(
            r.uri.path.endsWith('/login')
                ? tokens('old')
                : {...node(), 'protocol_config': config},
          ),
        );
        final s = source(transport);
        await login(s);
        await expectLater(
          s.getNode('00456'),
          throwsA(
            isA<ApiFailure>().having((e) => e.code, 'code', 'invalid_response'),
          ),
        );
        expect(s.customer, isNotNull);
      },
    );
  }

  test(
    'duplicate protocol keys are rejected before jsonDecode discards them',
    () async {
      final body =
          jsonEncode({
            'success': true,
            'data': {...node(), 'protocol_config': 'PLACEHOLDER'},
            'server_time': now.toIso8601String(),
          }).replaceFirst(
            '"PLACEHOLDER"',
            '{"version":1,"type":"http","type":"socks5","socks5":{}}',
          );
      final transport = FakeTransport(
        (r) async => r.uri.path.endsWith('/login')
            ? ok(tokens('old'))
            : ApiResponse(status: 200, body: utf8.encode(body)),
      );
      final s = source(transport);
      await login(s);
      await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
    },
  );
}
