import 'connection.dart';

final class TrafficSnapshot {
  const TrafficSnapshot(
      {required this.context,
      required this.counterEpoch,
      required this.sequence,
      required this.monotonicNanos,
      required this.uploadBytes,
      required this.downloadBytes,
      required this.available,
      required this.complete});
  final ControlContext context;
  final String counterEpoch;
  final int sequence;
  final BigInt monotonicNanos, uploadBytes, downloadBytes;
  final bool available, complete;
  factory TrafficSnapshot.fromJson(Map<String, dynamic> j) {
    BigInt counter(Object? value) {
      if (value is! String || !RegExp(r'^(0|[1-9][0-9]*)$').hasMatch(value)) {
        throw const FormatException(
            'Counter must be an unsigned decimal string');
      }
      final parsed = BigInt.parse(value);
      if (parsed > _uint64Max)
        throw const FormatException('Counter exceeds uint64');
      return parsed;
    }

    if (j['sequence'] is! int ||
        (j['sequence'] as int) < 0 ||
        j['monotonicNanos'] is! int ||
        (j['monotonicNanos'] as int) < 0 ||
        j['counterEpoch'] is! String ||
        (j['counterEpoch'] as String).isEmpty) {
      throw const FormatException('Invalid counter context');
    }
    return TrafficSnapshot(
        context: ControlContext.fromJson(j['context'] as Map<String, dynamic>),
        counterEpoch: j['counterEpoch'] as String,
        sequence: j['sequence'] as int,
        monotonicNanos: BigInt.parse(j['monotonicNanos'].toString()),
        uploadBytes: counter(j['uploadBytes']),
        downloadBytes: counter(j['downloadBytes']),
        available: j['available'] as bool,
        complete: j['complete'] as bool);
  }
}

final class TrafficView {
  const TrafficView(
      {this.uploadBytes,
      this.downloadBytes,
      this.uploadBytesPerSecond,
      this.downloadBytesPerSecond,
      required this.available,
      required this.complete});
  final BigInt? uploadBytes, downloadBytes;
  final double? uploadBytesPerSecond, downloadBytesPerSecond;
  final bool available, complete;
  BigInt? get totalBytes => uploadBytes == null || downloadBytes == null
      ? null
      : uploadBytes! + downloadBytes!;
}

final _uint64Max = (BigInt.one << 64) - BigInt.one;

TrafficView deriveTraffic(TrafficSnapshot? previous, TrafficSnapshot current) {
  bool valid(TrafficSnapshot s) =>
      s.uploadBytes >= BigInt.zero &&
      s.downloadBytes >= BigInt.zero &&
      s.uploadBytes <= _uint64Max &&
      s.downloadBytes <= _uint64Max &&
      s.monotonicNanos >= BigInt.zero;
  final sameSession = previous != null &&
      previous.context.resourceKey == current.context.resourceKey &&
      previous.context.clientEpoch == current.context.clientEpoch &&
      previous.context.logicalSessionId == current.context.logicalSessionId;
  final sameCounter = sameSession &&
      previous.counterEpoch == current.counterEpoch &&
      previous.context.runtimeEpoch == current.context.runtimeEpoch;
  if (!current.available || !valid(current)) {
    return TrafficView(
        available: false,
        complete: false,
        uploadBytes: sameSession && previous.available && valid(previous)
            ? previous.uploadBytes
            : null,
        downloadBytes: sameSession && previous.available && valid(previous)
            ? previous.downloadBytes
            : null);
  }
  if (sameCounter && current.sequence <= previous.sequence) {
    return TrafficView(
        uploadBytes: previous.uploadBytes,
        downloadBytes: previous.downloadBytes,
        available: previous.available,
        complete: false);
  }
  final continuous = sameCounter &&
      previous.available &&
      valid(previous) &&
      current.sequence == previous.sequence + 1 &&
      current.monotonicNanos > previous.monotonicNanos &&
      current.uploadBytes >= previous.uploadBytes &&
      current.downloadBytes >= previous.downloadBytes;
  final elapsed = continuous
      ? (current.monotonicNanos - previous.monotonicNanos).toDouble()
      : null;
  return TrafficView(
      uploadBytes: current.uploadBytes,
      downloadBytes: current.downloadBytes,
      uploadBytesPerSecond: continuous
          ? (current.uploadBytes - previous.uploadBytes).toDouble() *
              1e9 /
              elapsed!
          : null,
      downloadBytesPerSecond: continuous
          ? (current.downloadBytes - previous.downloadBytes).toDouble() *
              1e9 /
              elapsed!
          : null,
      available: true,
      complete: current.complete &&
          (!sameSession || continuous && previous.complete));
}
