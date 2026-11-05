import 'package:bluegrass_client/resource_presentation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('expiry shows seconds and explicit UTC+8 across a year boundary without modifying UTC', () {
    final expiry = DateTime.parse('2026-12-31T20:30:45Z');
    expect(formatExpiry(expiry), '2027-01-01 04:30:45（北京时间 UTC+8）');
    expect(
      formatExpiry(DateTime.parse('2027-01-01T04:30:45+08:00')),
      formatExpiry(expiry),
    );
    expect(expiry.toIso8601String(), '2026-12-31T20:30:45.000Z');
    expect(formatExpiry(null), '未指定到期');
  });
  test('public links reject credentials, tokens, scripts and ambiguous authorities', () {
    for (final input in [
      '',
      'http://example.com/',
      'javascript:alert(1)',
      'https://user:password@example.com/',
      'https://example.com/?token=value',
      'https://example.com/#token',
      'https://example.com/\n',
      'https://example.com\\@other.example/',
    ]) {
      expect(publicWebsiteUri(input), isNull, reason: input);
    }
    expect(
      publicWebsiteUri('https://example.com/account/renew')?.path,
      '/account/renew',
    );
  });
}
