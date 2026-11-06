import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  final f = fixture('time/clock.json');
  test(f['caseId'] as String, () {
    final clock = EffectiveClock(
        anchorUtc: DateTime.parse(f['anchorUtc'] as String),
        monotonicNanos: BigInt.parse(f['anchorNanos'] as String),
        runtimeEpoch: f['runtimeEpoch'] as String);
    expect(
        clock.read(
            monotonicNanos: BigInt.parse(f['sampleNanos'] as String),
            runtimeEpoch: 'runtime-1'),
        DateTime.parse(f['expectedUtc'] as String));
    expect(clock.read(monotonicNanos: BigInt.zero, runtimeEpoch: 'runtime-1'),
        isNull);
    expect(
        clock.read(
            monotonicNanos: BigInt.parse(f['sampleNanos'] as String),
            runtimeEpoch: 'runtime-2'),
        isNull);
  });
}
