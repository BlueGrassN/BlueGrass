import 'dart:convert';
import 'dart:math';

import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:flutter/services.dart';

const platform = MethodChannel('bluegrass/platform');

/// A complete session is replaced as one host-protected record: DPAPI on
/// Windows, Keychain on macOS. The historical name preserves API compatibility.
class WindowsSessionStore implements SessionStore {
  WindowsSessionStore(this.key);
  final String key;

  @override
  Future<Map<String, Object?>?> read() async {
    final value = await platform.invokeMethod<String>('readRecord', {
      'key': key,
    });
    if (value == null) return null;
    final decoded = jsonDecode(value);
    if (decoded is! Map<String, dynamic>) {
      throw const FormatException('Invalid session record');
    }
    return decoded;
  }

  @override
  Future<void> replace(Map<String, Object?> snapshot) => platform.invokeMethod(
    'writeRecord',
    {'key': key, 'value': jsonEncode(snapshot)},
  );

  @override
  Future<void> clear() => platform.invokeMethod('deleteRecord', {'key': key});
}

String newId() {
  final random = Random.secure();
  final bytes = List.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final hex = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-'
      '${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

Future<String> deviceId() async {
  const key = 'device_id';
  final previous = await platform.invokeMethod<String>('readRecord', {
    'key': key,
  });
  if (previous != null &&
      RegExp(
        r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
      ).hasMatch(previous)) {
    return previous;
  }
  final id = newId();
  await platform.invokeMethod<void>('writeRecord', {'key': key, 'value': id});
  return id;
}
