import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

import 'platform/vpn_client.dart';

class TrafficSample {
  TrafficSample.fromMap(Object? raw) {
    if (raw is! Map || raw['version'] != 1) {
      throw const FormatException('traffic');
    }
    String text(String key) {
      final value = raw[key];
      if (value is! String || value.isEmpty || value.length > 2048) {
        throw const FormatException('traffic');
      }
      return value;
    }

    int number(String key) {
      final value = raw[key];
      if (value is! int || value < 0) throw const FormatException('traffic');
      return value;
    }

    bool flag(String key) {
      final value = raw[key];
      if (value is! bool) throw const FormatException('traffic');
      return value;
    }

    BigInt bytes(String key) {
      final value = text(key);
      if (value.length > 39 || !RegExp(r'^(0|[1-9][0-9]*)$').hasMatch(value)) {
        throw const FormatException('traffic');
      }
      final count = BigInt.parse(value);
      if (count > (BigInt.one << 128) - BigInt.one) {
        throw const FormatException('traffic');
      }
      return count;
    }

    runtimeId = text('runtimeId');
    requestId = text('requestId');
    accountKey = text('accountKey');
    resourceKey = text('resourceKey');
    counterEpoch = text('counterEpoch');
    sequence = number('sequence');
    generation = number('generation');
    monotonicMillis = number('monotonicMillis');
    uploadBytes = bytes('uploadBytes');
    downloadBytes = bytes('downloadBytes');
    available = flag('available');
    complete = flag('complete');
    active = flag('active');
  }
  late final String runtimeId, requestId, accountKey, resourceKey, counterEpoch;
  late final int generation, sequence, monotonicMillis;
  late final BigInt uploadBytes, downloadBytes;
  late final bool available, complete, active;
}

abstract interface class TrafficClient {
  Future<TrafficSample> sample();
}

class MethodChannelTrafficClient implements TrafficClient {
  const MethodChannelTrafficClient();
  @override
  Future<TrafficSample> sample() async => TrafficSample.fromMap(
    await const MethodChannel('bluegrass/vpn').invokeMethod<Object?>('traffic'),
  );
}

/// Native cumulative values are authoritative; the UI never integrates rates.
class TrafficModel extends ChangeNotifier {
  TrafficModel({this.client});
  final TrafficClient? client;
  VpnSnapshot? _binding;
  TrafficSample? _previous;
  TrafficSample? _accepted;
  final _retiredEpochs = <String>{};
  Timer? _timer;
  bool _visible = false, _busy = false, _disposed = false;
  bool _incomplete = false;
  int _revision = 0;
  BigInt? uploadBytes = BigInt.zero, downloadBytes = BigInt.zero;
  double? uploadRate = 0, downloadRate = 0;
  bool available = true, complete = true;
  BigInt? get totalBytes => uploadBytes == null || downloadBytes == null
      ? null
      : uploadBytes! + downloadBytes!;

  bool _same(VpnSnapshot? a, VpnSnapshot? b) =>
      a?.runtimeId == b?.runtimeId &&
      a?.requestId == b?.requestId &&
      a?.accountKey == b?.accountKey &&
      a?.resourceKey == b?.resourceKey;

  void bind(VpnSnapshot? snapshot, String? account) {
    final next =
        snapshot != null &&
            snapshot.state != VpnState.idle &&
            !const {
              'RESOURCE_EXPIRED',
              'RESOURCE_REVOKED',
              'SESSION_REVOKED',
              'CONFIG_CHANGED',
            }.contains(snapshot.errorCode) &&
            account != null &&
            snapshot.accountKey == account
        ? snapshot
        : null;
    final changed = !_same(_binding, next);
    final stateChanged = _binding?.state != next?.state;
    _binding = next;
    if (!changed && !stateChanged) return;
    _revision++;
    _timer?.cancel();
    _previous = null;
    if (changed) {
      _accepted = null;
      _retiredEpochs.clear();
      _incomplete = false;
      complete = true;
      uploadBytes = downloadBytes = next == null ? BigInt.zero : null;
    }
    available = next == null;
    uploadRate = downloadRate = next == null || next.state != VpnState.connected
        ? 0
        : null;
    if (!_disposed) notifyListeners();
    if (_visible) unawaited(refresh());
  }

  void setVisible(bool value) {
    if (_disposed || _visible == value) return;
    _visible = value;
    _revision++;
    _timer?.cancel();
    _previous = null;
    if (_binding?.state == VpnState.connected) uploadRate = downloadRate = null;
    if (value) {
      notifyListeners();
      unawaited(refresh());
    }
  }

  void _unavailable() {
    _previous = null;
    available = false;
    uploadRate = downloadRate = null;
  }

  Future<void> refresh() async {
    if (_disposed || _busy || _binding == null || !_visible) return;
    _timer?.cancel();
    if (client == null) {
      _unavailable();
      notifyListeners();
      return;
    }
    final revision = _revision;
    _busy = true;
    try {
      final sample = await client!.sample().timeout(const Duration(seconds: 3));
      if (_disposed || revision != _revision || !_visible) return;
      final bound = _binding!;
      if (sample.runtimeId != bound.runtimeId ||
          sample.requestId != bound.requestId ||
          sample.accountKey != bound.accountKey ||
          sample.resourceKey != bound.resourceKey ||
          sample.generation < bound.generation) {
        _unavailable();
      } else if (!sample.available ||
          _retiredEpochs.contains(sample.counterEpoch)) {
        _unavailable();
      } else {
        final previous = _previous;
        final accepted = _accepted;
        final sameCounter = accepted?.counterEpoch == sample.counterEpoch;
        if (sameCounter &&
            (sample.sequence <= accepted!.sequence ||
                sample.uploadBytes < accepted.uploadBytes ||
                sample.downloadBytes < accepted.downloadBytes ||
                sample.monotonicMillis < accepted.monotonicMillis)) {
          _incomplete = true;
          complete = false;
          _unavailable();
        } else {
          if (accepted != null && !sameCounter) {
            _incomplete = true;
            _retiredEpochs.add(accepted.counterEpoch);
          }
          final elapsed = previous == null
              ? 0
              : sample.monotonicMillis - previous.monotonicMillis;
          final continuous =
              sameCounter &&
              previous != null &&
              sample.sequence == previous.sequence + 1 &&
              elapsed > 0 &&
              elapsed <= 5000 &&
              previous.active &&
              sample.active &&
              previous.complete &&
              sample.complete;
          uploadBytes = sample.uploadBytes;
          downloadBytes = sample.downloadBytes;
          available = true;
          complete = sample.complete && !_incomplete;
          if (!sample.active || bound.state != VpnState.connected) {
            uploadRate = downloadRate = 0;
          } else {
            uploadRate = continuous
                ? (sample.uploadBytes - previous.uploadBytes).toDouble() *
                      1000 /
                      elapsed
                : null;
            downloadRate = continuous
                ? (sample.downloadBytes - previous.downloadBytes).toDouble() *
                      1000 /
                      elapsed
                : null;
          }
          _previous = sample;
          _accepted = sample;
        }
      }
    } catch (_) {
      if (!_disposed && revision == _revision) _unavailable();
    } finally {
      _busy = false;
      if (!_disposed && revision == _revision) notifyListeners();
      if (!_disposed && _visible && _binding != null) {
        _timer = Timer(const Duration(seconds: 1), () => unawaited(refresh()));
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _revision++;
    _timer?.cancel();
    super.dispose();
  }
}

({String value, String unit}) formatTraffic(num? bytes, {bool rate = false}) {
  if (bytes == null || bytes < 0 || !bytes.isFinite) {
    return (value: '—', unit: rate ? 'B/s' : 'B');
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB'];
  var value = bytes.toDouble(), index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index++;
  }
  return (
    value: value.toStringAsFixed(index == 0 ? 0 : 1),
    unit: '${units[index]}${rate ? '/s' : ''}',
  );
}
