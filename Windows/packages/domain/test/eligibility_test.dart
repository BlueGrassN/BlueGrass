import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  final data = fixture('eligibility/cases.json');
  final now = DateTime.parse(data['nowUtc'] as String);
  for (final c in data['cases'] as List) {
    test(c['caseId'] as String, () {
      final result = prepareProfile(
          NodeRecord(
            key: ResourceKey.fromJson(data['key'] as Map<String, dynamic>),
            name: 'Synthetic node',
            protocol: c['protocol'] as String? ?? 'socks5',
            host: '8.8.8.8',
            port: c['port'] as int? ?? 1080,
            expiresAtUtc: c['expiresAtUtc'] == null
                ? null
                : DateTime.parse(c['expiresAtUtc'] as String),
            expectedExitIpv4: c['expectedExitIpv4'] as String?,
            isForwarded: c['isForwarded'] as bool? ?? true,
          ),
          now);
      if (c['expectedError'] == null) {
        expect(result, isA<Ready>());
        final profile = (result as Ready).profile;
        expect(profile.expectedExitIpv4, c['expectedExitIpv4']);
        expect(
            profile.validUntilUtc,
            c['expiresAtUtc'] == null
                ? null
                : DateTime.parse(c['expiresAtUtc'] as String));
      } else {
        expect(result, isA<Rejected>());
        expect((result as Rejected).error.code, c['expectedError']);
      }
    });
  }
  for (final ip in data['invalidObservedIps'] as List) {
    test('expected metadata does not gate profile: $ip', () {
      final result = prepareProfile(node(expected: ip as String), now);
      expect(result, isA<Ready>());
      expect((result as Ready).profile.expectedExitIpv4, ip);
      expect(isPublicIpv4(ip), isFalse);
    });
  }
  test('missing expected metadata does not bypass resource identity', () {
    final invalid = ResourceKey(
        environment: '',
        dataSource: 'api-v1',
        accountKey: 'fixture',
        sourceId: '1');
    final result =
        prepareProfile(node(resourceKey: invalid, expected: null), now);
    expect(result, isA<Rejected>());
    expect((result as Rejected).error.code, 'SYNC-003');
  });
  test('credential values never appear in diagnostic strings', () {
    final record = NodeRecord(
        key: key(),
        name: 'Synthetic',
        protocol: 'socks5',
        host: 'invalid/entry',
        port: 1080,
        username: 'FAKE-USER',
        password: 'FAKE-SECRET');
    final result = prepareProfile(record, now) as Rejected;
    expect('$record $result ${result.error}', isNot(contains('FAKE-SECRET')));
    expect('$record $result ${result.error}', isNot(contains('FAKE-USER')));
  });
}
