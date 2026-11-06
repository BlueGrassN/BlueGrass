import 'dart:convert';

import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';

import 'fixtures.dart';

Map<String, dynamic> config(String type, Map<String, Object?> options,
        {Map<String, Object?>? tls}) =>
    {
      'version': 1,
      'type': type,
      type == 'https' ? 'http' : type: options,
      if (tls != null) 'tls': tls
    };
const uuid = '7b7ea563-e7d4-4de9-a6d3-5e50b9d20f81';

void main() {
  final vectors = fixture('protocol/config-v1.json');
  for (final vector in vectors['valid'] as List) {
    test('golden accepts ${vector['id']}', () {
      final value = ProtocolConfig.parse(vector['json'] as String);
      expect(value.version, 1);
      final decoded = ProtocolConfig.fromJson(value.toJson());
      expect(decoded, value);
      expect(decoded.hashCode, value.hashCode);
    });
  }
  for (final vector in vectors['invalid'] as List) {
    test('golden rejects ${vector['id']}', () {
      expect(
          () => ProtocolConfig.parse(vector['json'] as String),
          throwsA(isA<FormatException>()
              .having((e) => e.source, 'source', isNull)));
    });
  }

  test('Xray eligibility preserves unsupported resources without fallback', () {
    final now = DateTime.utc(2026, 10, 19);
    for (final vector in vectors['valid'] as List) {
      final value = ProtocolConfig.parse(vector['json'] as String);
      final node = NodeRecord(
        key: key(),
        name: 'Fixture',
        protocol: value.type,
        host: 'entry.example',
        port: 443,
        expiresAtUtc: now.add(const Duration(days: 1)),
        protocolConfig: value,
      );
      final result = prepareProfile(node, now);
      if (value.type == 'tuic' || value.type == 'anytls') {
        expect(
            result,
            isA<Rejected>().having(
                (r) => r.error.code, 'explicit Xray capability', 'ELIG-008'));
        expect(node.protocolConfig, value);
      } else {
        expect(result, isA<Ready>(), reason: value.type);
      }
    }
  });

  test('config owns nested data and toJson is a detached credential payload',
      () {
    final raw = config('https', {
      'username': 'fixture',
      'password': 'fixture-secret'
    }, tls: {
      'serverName': 'entry.example',
      'alpn': ['http/1.1']
    });
    final value = ProtocolConfig.fromJson(raw);
    final original = value.toJson();
    (raw['http'] as Map)['password'] = 'changed';
    ((raw['tls'] as Map)['alpn'] as List).clear();
    final output = value.toJson();
    (output['http'] as Map)['password'] = 'changed-again';
    ((output['tls'] as Map)['alpn'] as List).clear();
    expect(value.toJson(), original);
    expect('$value', isNot(contains('fixture-secret')));
    expect('$value', isNot(contains('entry.example')));
  });

  test('equality ignores object ordering and omitted empty options', () {
    final a = ProtocolConfig.fromJson(config('http', {}));
    final b = ProtocolConfig.fromJson({
      'http': {'password': '', 'username': ''},
      'type': 'http',
      'version': 1
    });
    expect(a, b);
    expect(a.hashCode, b.hashCode);
    expect(
        ProtocolConfig.fromJson(config(
            'vless', {'uuid': uuid, 'transport': 'tcp'},
            tls: {'serverName': 'entry.example'})),
        ProtocolConfig.fromJson(config(
            'vless', {'flow': '', 'transport': 'tcp', 'uuid': uuid},
            tls: {'alpn': [], 'serverName': 'entry.example'})));
  });

  test(
      'resource refresh distinguishes protocol changes from expiry-only updates',
      () {
    final now = DateTime.utc(2026, 10, 12);
    final context = ControlContext(
        clientEpoch: 'client',
        runtimeEpoch: 'runtime',
        logicalSessionId: 'session',
        attemptId: 'attempt',
        resourceKey: key());
    ConnectionProfile profile(Map<String, dynamic> raw, int days) =>
        (prepareProfile(
                NodeRecord(
                    key: key(),
                    name: 'Fixture',
                    protocol: 'https',
                    host: 'entry.example',
                    port: 443,
                    expectedExitIpv4: '8.8.8.8',
                    expiresAtUtc: now.add(Duration(days: days)),
                    protocolConfig: ProtocolConfig.fromJson(raw)),
                now) as Ready)
            .profile;
    final original = config('https', {'username': 'fixture', 'password': 'one'},
        tls: {'serverName': 'entry.example'});
    final connected = ConnectionState(
        phase: ConnectionPhase.connected,
        protection: ProtectionState.forwarding,
        context: context,
        profile: profile(original, 1),
        verifiedExitIpv4: '8.8.8.8');
    final same = config('https', {'password': 'one', 'username': 'fixture'},
        tls: {'alpn': [], 'serverName': 'entry.example'});
    final expiryOnly = reduceConnection(
        connected,
        ResourceUpdateAccepted(context,
            revision: 1, profile: profile(same, 2)));
    expect(expiryOnly.phase, ConnectionPhase.connected);
    expect(expiryOnly.verifiedExitIpv4, '8.8.8.8');
    expect(expiryOnly.effects, [ConnectionEffect.applyResourceUpdate]);
    for (final changed in [
      config('https', {'username': 'fixture', 'password': 'two'},
          tls: {'serverName': 'entry.example'}),
      config('https', {'username': 'fixture', 'password': 'one'},
          tls: {'serverName': 'changed.example'}),
    ]) {
      final refreshed = reduceConnection(
          connected,
          ResourceUpdateAccepted(context,
              revision: 1, profile: profile(changed, 2)));
      expect(refreshed.phase, ConnectionPhase.pausing);
      expect(refreshed.verifiedExitIpv4, isNull);
      expect(refreshed.protection, ProtectionState.forwarding);
      expect(refreshed.effects, [ConnectionEffect.applyResourceUpdate]);
    }
  });

  test('credential TLS option and ALPN preference changes alter equality', () {
    final original = config('vmess', {
      'uuid': uuid,
      'security': 'aes-128-gcm',
      'transport': 'tcp'
    }, tls: {
      'serverName': 'entry.example',
      'alpn': ['h2', 'http/1.1']
    });
    final a = ProtocolConfig.fromJson(original);
    for (final mutate in <void Function(Map<String, dynamic>)>[
      (r) =>
          (r['vmess'] as Map)['uuid'] = '11111111-1111-1111-1111-111111111111',
      (r) => (r['vmess'] as Map)['security'] = 'chacha20-poly1305',
      (r) => (r['tls'] as Map)['serverName'] = 'other.example',
      (r) => (r['tls'] as Map)['alpn'] = ['http/1.1', 'h2'],
    ]) {
      final changed = jsonDecode(jsonEncode(original)) as Map<String, dynamic>;
      mutate(changed);
      expect(ProtocolConfig.fromJson(changed), isNot(a));
    }
    expect(
        ProtocolConfig.legacySocks(username: 'fixture', password: 'one'),
        isNot(
            ProtocolConfig.legacySocks(username: 'fixture', password: 'two')));
  });

  test('secret limits count UTF-8 bytes and reject delimiters', () {
    expect(ProtocolConfig.legacySocks(username: 'é' * 127, password: 'x'),
        isA<ProtocolConfig>());
    for (final secret in ['é' * 128, 'x' * 256, 'x\u0000', 'x\r', 'x\n']) {
      expect(() => ProtocolConfig.legacySocks(username: secret, password: 'x'),
          throwsFormatException);
    }
    for (final secret in ['x' * 1025, 'x\n']) {
      expect(
          () => ProtocolConfig.fromJson(config('anytls', {'password': secret},
              tls: {'serverName': 'entry.example'})),
          throwsFormatException);
    }
  });

  test('all frozen SS methods and TUIC combinations have explicit options', () {
    for (final method in [
      'aes-128-gcm',
      'aes-256-gcm',
      'chacha20-ietf-poly1305',
      '2022-blake3-aes-128-gcm',
      '2022-blake3-aes-256-gcm',
      '2022-blake3-chacha20-poly1305'
    ]) {
      final password = method.startsWith('2022-')
          ? base64.encode(List.filled(method.contains('aes-128') ? 16 : 32, 0))
          : 'fixture';
      expect(
          ProtocolConfig.fromJson(
              config('shadowsocks', {'method': method, 'password': password})),
          isA<ProtocolConfig>());
    }
    for (final congestion in ['cubic', 'new_reno', 'bbr']) {
      for (final mode in ['native', 'quic']) {
        expect(
            ProtocolConfig.fromJson(config('tuic', {
              'uuid': uuid,
              'password': 'fixture',
              'congestionControl': congestion,
              'udpRelayMode': mode
            }, tls: {
              'serverName': 'entry.example'
            })),
            isA<ProtocolConfig>());
      }
    }
  });

  test(
      'unknown nested fields missing required values and bad enums are rejected',
      () {
    for (final vector in vectors['valid'] as List) {
      final original =
          jsonDecode(vector['json'] as String) as Map<String, dynamic>;
      final type = original['type'] as String;
      final section = type == 'https' ? 'http' : type;
      (original[section] as Map)['unknown'] = true;
      expect(() => ProtocolConfig.fromJson(original), throwsFormatException);
    }
    for (final invalid in [
      config('vmess', {
        'uuid': '00000000-0000-0000-0000-000000000000',
        'security': 'aes-128-gcm',
        'transport': 'tcp'
      }),
      config('vmess', {'uuid': uuid, 'security': 'auto', 'transport': 'tcp'}),
      config('vless', {'uuid': uuid, 'transport': 'tcp', 'flow': 'future'},
          tls: {'serverName': 'entry.example'}),
      config('tuic', {
        'uuid': uuid,
        'password': 'fixture',
        'congestionControl': 'future',
        'udpRelayMode': 'native'
      }, tls: {
        'serverName': 'entry.example'
      }),
      config('tuic', {
        'uuid': uuid,
        'password': 'fixture',
        'congestionControl': 'bbr',
        'udpRelayMode': 'future'
      }, tls: {
        'serverName': 'entry.example'
      }),
      config('hysteria2', {'password': 'fixture', 'obfs': null},
          tls: {'serverName': 'entry.example'}),
      config('hysteria2', {
        'password': 'fixture',
        'obfs': {'type': 'gecko', 'password': 'fixture'}
      }, tls: {
        'serverName': 'entry.example'
      }),
      config('hysteria2', {
        'password': 'fixture'
      }, tls: {
        'serverName': 'entry.example',
        'alpn': ['http/1.1']
      }),
      config('anytls', {}, tls: {'serverName': 'entry.example'}),
    ]) {
      expect(() => ProtocolConfig.fromJson(invalid), throwsFormatException);
    }
  });

  test('strict decoder distinguishes nested objects arrays and escaped strings',
      () {
    const text =
        r'{"a":[{"k":"brackets {}[] : and quote \""},{"k":2}],"b":{"k":3}}';
    expect(decodeStrictJson(text), jsonDecode(text));
    for (final text in [
      r'{"a":{"k":1,"\u006b":2}}',
      r'{"a":[{"k":1,"k":2}]}',
      '{"x":"fixture-secret}',
      '{"x":NaN}'
    ]) {
      expect(
          () => decodeStrictJson(text),
          throwsA(isA<FormatException>()
              .having((e) => e.source, 'source', isNull)));
    }
  });

  test('TLS name and ALPN validation follows frozen native checks', () {
    for (final name in [
      '',
      '*.example',
      'https://entry.example',
      '-entry.example',
      'entry-.example',
      'entry..example',
      'entry.example.',
      '::1',
      '127.0.0.1',
      '169.254.1.2',
      '224.0.0.1',
      '255.255.255.255',
      '0.0.0.0',
      'a' * 64,
      'é.example'
    ]) {
      expect(
          () => ProtocolConfig.fromJson(
              config('https', {}, tls: {'serverName': name})),
          throwsFormatException);
    }
    for (final alpn in <Object?>[
      null,
      'h2',
      ['h2', 'h2'],
      [''],
      ['h 2'],
      ['é'],
      ['a' * 256],
      List.generate(9, (i) => 'p$i')
    ]) {
      expect(
          () => ProtocolConfig.fromJson(config('vmess',
              {'uuid': uuid, 'security': 'aes-128-gcm', 'transport': 'tcp'},
              tls: {'serverName': 'entry.example', 'alpn': alpn})),
          throwsFormatException);
    }
  });

  test('oversized malformed JSON and diagnostic errors never retain input', () {
    for (final raw in [
      '{"fixture-secret":',
      '${' ' * 16384}{"version":1,"type":"http","http":{}}'
    ]) {
      try {
        ProtocolConfig.parse(raw);
        fail('accepted invalid payload');
      } on FormatException catch (e) {
        expect(e.source, isNull);
        expect('$e', isNot(contains('fixture-secret')));
      }
    }
  });

  test(
      'prepareProfile only permits matching configs with unambiguous credentials',
      () {
    NodeRecord make(String type,
            {ProtocolConfig? explicit,
            String username = '',
            String password = ''}) =>
        NodeRecord(
            key: key(),
            name: 'Fixture',
            protocol: type,
            host: 'entry.example',
            port: 443,
            expectedExitIpv4: '8.8.8.8',
            protocolConfig: explicit,
            username: username,
            password: password);
    final tls = ProtocolConfig.fromJson(
        config('https', {}, tls: {'serverName': 'entry.example'}));
    final now = DateTime.utc(2026, 10, 12);
    final result = prepareProfile(make('https', explicit: tls), now) as Ready;
    expect(result.profile.protocolConfig, tls);
    expect(prepareProfile(make('socks5'), now), isA<Ready>());
    for (final bad in [
      make('https'),
      make('https', explicit: tls, password: 'legacy'),
      make('http', explicit: tls),
      make('socks5', username: 'one-sided'),
      make('socks5',
          explicit: ProtocolConfig.legacySocks(),
          username: 'conflict',
          password: 'fixture')
    ]) {
      expect((prepareProfile(bad, now) as Rejected).error.code, 'ELIG-007');
    }
    final auth =
        ProtocolConfig.legacySocks(username: 'fixture', password: 'secret');
    expect(
        prepareProfile(
            make('socks5',
                explicit: auth, username: 'fixture', password: 'secret'),
            now),
        isA<Ready>());
  });
}
