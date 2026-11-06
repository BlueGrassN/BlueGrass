import 'platform_store.dart';

/// Only user cancellation intent; package trust/cache remains UpdateClient-owned.
abstract class UpdatePauseStore {
  Future<String?> read();
  Future<void> write(String? candidate);
}

class MemoryUpdatePauseStore implements UpdatePauseStore {
  String? value;
  @override
  Future<String?> read() async => value;
  @override
  Future<void> write(String? candidate) async {
    value = candidate;
  }
}

class WindowsUpdatePauseStore implements UpdatePauseStore {
  static const _key = 'update_user_pause_v1';
  @override
  Future<String?> read() async {
    final value = await platform.invokeMethod<String>('readRecord', {
      'key': _key,
    });
    if (value != null && !RegExp(r'^[a-f0-9]{64}$').hasMatch(value)) {
      throw const FormatException('Invalid update user state');
    }
    return value;
  }

  @override
  Future<void> write(String? candidate) => candidate == null
      ? platform.invokeMethod<void>('deleteRecord', {'key': _key})
      : platform.invokeMethod<void>('writeRecord', {
          'key': _key,
          'value': candidate,
        });
}
