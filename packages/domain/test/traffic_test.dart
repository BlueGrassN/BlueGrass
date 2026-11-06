import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  final f = fixture('traffic/counters.json');
  final base = <String, dynamic>{
    ...f['base'] as Map<String, dynamic>,
    'context': f['context']
  };
  final previous = TrafficSnapshot.fromJson(base);
  for (final c in f['cases'] as List) {
    test(c['caseId'] as String, () {
      final current = TrafficSnapshot.fromJson(
          {...base, ...c['patch'] as Map<String, dynamic>});
      final view = deriveTraffic(previous, current);
      expect(view.uploadBytesPerSecond, c['expectedUploadSpeed']);
      expect(view.downloadBytesPerSecond, c['expectedDownloadSpeed']);
      expect(view.available, c['expectedAvailable']);
      expect(view.complete, c['expectedComplete']);
      if (view.available) {
        expect(view.uploadBytes, current.uploadBytes);
        expect(view.totalBytes, current.uploadBytes + current.downloadBytes);
      }
    });
  }
  test('first sample establishes exact baseline without inventing rate', () {
    final view = deriveTraffic(null, previous);
    expect(view.uploadBytes.toString(), '9007199254740993');
    expect(view.uploadBytesPerSecond, isNull);
    expect(view.complete, isTrue);
  });
  test('old resource results never contaminate new resource totals', () {
    final contextB = fixture('connection/generations.json')['contextB'];
    final current = TrafficSnapshot.fromJson({
      ...base,
      'context': contextB,
      'uploadBytes': '10',
      'downloadBytes': '20'
    });
    final view = deriveTraffic(previous, current);
    expect(view.totalBytes, BigInt.from(30));
    expect(view.uploadBytesPerSecond, isNull);
    expect(view.complete, isTrue);
  });
  test(
      'counters require uint64 decimal strings without floating point conversion',
      () {
    for (final invalid in f['invalidCounters'] as List) {
      expect(() => TrafficSnapshot.fromJson({...base, 'uploadBytes': invalid}),
          throwsFormatException);
    }
    expect(
        TrafficSnapshot.fromJson(
            {...base, 'uploadBytes': '18446744073709551615'}).uploadBytes,
        BigInt.parse('18446744073709551615'));
  });
  test('out of order same-epoch sample preserves last reliable totals', () {
    final current =
        TrafficSnapshot.fromJson({...base, 'sequence': 0, 'uploadBytes': '5'});
    final view = deriveTraffic(previous, current);
    expect(view.uploadBytes, previous.uploadBytes);
    expect(view.uploadBytesPerSecond, isNull);
    expect(view.complete, isFalse);
  });
}
