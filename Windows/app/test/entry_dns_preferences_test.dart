import 'package:bluegrass_client/entry_dns_policy.dart';
import 'package:bluegrass_client/preferences.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'entry DNS rejects endpoint classes the native WFP policy cannot enforce',
    () {
      for (final address in [
        '127.0.0.1',
        '127.255.0.1',
        '0.0.0.0',
        '224.0.0.1',
        '239.255.255.250',
        '::',
        '::1',
        'ff02::1',
        'fe80::1',
        'febf::1',
        'fe80::1%12',
        '::ffff:192.0.2.1',
        '::ffff:c000:201',
      ]) {
        expect(
          () => EntryDnsCandidate(address: address),
          throwsFormatException,
          reason: address,
        );
      }
      for (final address in [
        '192.168.1.1',
        '10.0.0.1',
        '172.16.0.1',
        '9.9.9.9',
        '2606:4700:4700::1111',
        'fd00::53',
      ]) {
        expect(EntryDnsCandidate(address: address).port, 53, reason: address);
      }
    },
  );
  test('old preferences default to explicit automatic entry DNS', () {
    expect(Preferences.fromJson({}).entryDns.toJson(), {
      'mode': 'auto',
      'candidates': [],
    });
  });
  test(
    'ordered custom numeric endpoints survive unrelated preference changes',
    () {
      final dns = EntryDnsPolicy.custom([
        EntryDnsCandidate(address: '9.9.9.9'),
        EntryDnsCandidate(
          address: '2606:4700:4700::1111',
          port: 5353,
          transport: 'tcp',
        ),
      ]);
      final prefs = Preferences(
        entryDns: dns,
        autoConnect: true,
      ).copyWith(theme: ThemeMode.dark, allowLan: false);
      final restored = Preferences.fromJson(prefs.toJson());
      expect(restored.entryDns.toJson(), {
        'mode': 'custom',
        'candidates': [
          {'address': '9.9.9.9', 'port': 53, 'transport': 'udp'},
          {'address': '2606:4700:4700::1111', 'port': 5353, 'transport': 'tcp'},
        ],
      });
      expect(restored.autoConnect, isTrue);
      expect(restored.entryDns.candidates, hasLength(2));
      expect(
        () => restored.entryDns.candidates.clear(),
        throwsUnsupportedError,
      );
    },
  );
  test('invalid custom configuration never silently becomes automatic DNS', () {
    for (final candidate in [
      {'address': 'dns.example.com', 'port': 53, 'transport': 'udp'},
      {'address': 'https://1.1.1.1/dns-query', 'port': 53, 'transport': 'udp'},
      {'address': '1.1.1.1', 'port': 0, 'transport': 'udp'},
      {'address': '1.1.1.1', 'port': 65536, 'transport': 'udp'},
      {'address': '1.1.1.1', 'port': 53, 'transport': 'tls'},
    ]) {
      expect(
        () => Preferences.fromJson({
          'entryDns': {
            'mode': 'custom',
            'candidates': [candidate],
          },
        }),
        throwsFormatException,
      );
    }
    for (final value in [
      {'mode': 'custom', 'candidates': []},
      {'mode': 'unknown', 'candidates': []},
      {
        'mode': 'custom',
        'candidates': List.generate(
          9,
          (_) => {'address': '1.1.1.1', 'port': 53, 'transport': 'udp'},
        ),
      },
    ]) {
      expect(() => EntryDnsPolicy.fromJson(value), throwsFormatException);
    }
  });
  test('automatic DNS has no hidden custom candidates', () {
    expect(const EntryDnsPolicy.auto().toJson(), {
      'mode': 'auto',
      'candidates': [],
    });
    expect(
      () => EntryDnsPolicy.fromJson({
        'mode': 'auto',
        'candidates': [
          {'address': '1.1.1.1', 'port': 53, 'transport': 'udp'},
        ],
      }),
      throwsFormatException,
    );
  });
}
