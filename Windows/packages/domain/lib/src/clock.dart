/// A trusted UTC anchor advances only by monotonic time in the same runtime.
final class EffectiveClock {
  EffectiveClock(
      {required this.anchorUtc,
      required this.monotonicNanos,
      required this.runtimeEpoch});
  final DateTime anchorUtc;
  final BigInt monotonicNanos;
  final String runtimeEpoch;
  DateTime? read(
      {required BigInt monotonicNanos, required String runtimeEpoch}) {
    if (runtimeEpoch != this.runtimeEpoch ||
        monotonicNanos < this.monotonicNanos) return null;
    final micros = (monotonicNanos - this.monotonicNanos) ~/ BigInt.from(1000);
    // Dart DateTime's supported interval is bounded; never truncate a BigInt.
    if (micros > BigInt.from(8640000000000000000)) return null;
    try {
      return anchorUtc.toUtc().add(Duration(microseconds: micros.toInt()));
    } on ArgumentError {
      return null;
    }
  }
}
