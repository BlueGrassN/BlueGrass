import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  test('resource identity compares every namespace field and preserves strings',
      () {
    final json =
        fixture('eligibility/cases.json')['key'] as Map<String, dynamic>;
    final a = ResourceKey.fromJson(json);
    final b = ResourceKey.fromJson(json);
    expect(a, b);
    expect({a, b}, hasLength(1));
    expect(a.sourceId, '00042');
    for (final field in json.keys) {
      expect(
          ResourceKey.fromJson(
              {...json, field: field == 'kind' ? 'asset' : 'other'}),
          isNot(a));
    }
  });
  test('resource JSON rejects malformed namespace and unknown fields', () {
    final json =
        fixture('eligibility/cases.json')['key'] as Map<String, dynamic>;
    for (final bad in [
      {...json, 'sourceId': ''},
      {...json, 'kind': 'unknown'},
      {...json, 'extra': true}
    ]) {
      expect(() => ResourceKey.fromJson(bad), throwsFormatException);
    }
  });
}
