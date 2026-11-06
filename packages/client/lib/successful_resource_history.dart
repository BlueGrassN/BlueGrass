import 'dart:convert';

import 'package:bluegrass_domain/bluegrass_domain.dart';

import 'expiry_reminder_coordinator.dart';
import 'platform_store.dart';

/// A separate DPAPI record per local user, environment, source and account.
/// Embedded scope is also checked, so a filename collision fails closed.
class WindowsSuccessfulHistoryStore extends ReminderStore {
  String _key(ReminderScope scope) {
    var hash = BigInt.parse('cbf29ce484222325', radix: 16);
    final mask = (BigInt.one << 64) - BigInt.one;
    for (final byte in utf8.encode(scope.stableId)) {
      hash = ((hash ^ BigInt.from(byte)) * BigInt.from(1099511628211)) & mask;
    }
    return 'successful_resources_${hash.toRadixString(16).padLeft(16, '0')}';
  }

  @override
  Future<String?> read(ReminderScope scope) =>
      platform.invokeMethod<String>('readRecord', {'key': _key(scope)});
  @override
  Future<void> write(ReminderScope scope, String value) => platform
      .invokeMethod<void>('writeRecord', {'key': _key(scope), 'value': value});
  @override
  Future<void> delete(ReminderScope scope) =>
      platform.invokeMethod<void>('deleteRecord', {'key': _key(scope)});
}

/// Call recordSuccess only after accepting a verified native success event for
/// the current account/attempt. Selection, connection attempts and failed exit
/// checks must never call it. successId identifies the accepted event across
/// polling/restart; an old replay cannot displace a more recent success.
///
/// Ordering is persisted, independent of wall-clock changes. Storage failures
/// propagate so the owner can report them without pretending history was saved.
class SuccessfulResourceHistory {
  SuccessfulResourceHistory({ReminderStore? store})
    : store = store ?? WindowsSuccessfulHistoryStore();
  final ReminderStore store;
  Future<void> _queue = Future.value();

  Future<T> _ordered<T>(Future<T> Function() operation) {
    final result = _queue.then((_) => operation());
    _queue = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<void> recordSuccess(
    ReminderScope scope,
    ResourceKey key, {
    required String successId,
  }) => _ordered(() async {
    if (!scope.contains(key) ||
        successId.trim().isEmpty ||
        successId.length > 512) {
      throw ArgumentError('Invalid successful resource identity');
    }
    // Also reject malformed keys constructed without JSON validation.
    ResourceKey.fromJson(key.toJson());
    final state = await _read(scope);
    final previous = state.events[successId];
    if (previous != null) {
      if (previous != key.stableId) {
        throw StateError('Conflicting successful resource event');
      }
      return;
    }
    state.events[successId] = key.stableId;
    state.recent.remove(key);
    state.recent.insert(0, key);
    final value = jsonEncode({
      'version': 1,
      'scope': scope.stableId,
      'recent': state.recent.map((key) => key.toJson()).toList(),
      'events': state.events,
    });
    // Preserve existing durable history if capacity is reached; never silently
    // drop deduplication identities and reinterpret an old replay as new.
    if (utf8.encode(value).length > 1024 * 1024) {
      throw StateError('Successful resource history capacity exceeded');
    }
    await store.write(scope, value);
  });

  Future<ResourceKey?> getLast(ReminderScope scope) => _ordered(() async {
    final recent = (await _read(scope)).recent;
    return recent.isEmpty ? null : recent.first;
  });

  Future<List<ResourceKey>> getRecent(ReminderScope scope) =>
      _ordered(() async => List.unmodifiable((await _read(scope)).recent));

  /// Logout clears only this account's history. OS-user preferences are stored
  /// separately and are deliberately outside this helper's access.
  Future<void> clearAccount(ReminderScope scope) => _ordered(() async {
    // Verify embedded scope before deleting even if a filename collides or
    // storage accidentally returns another account's record.
    await _read(scope);
    await store.delete(scope);
  });

  Future<_History> _read(ReminderScope scope) async {
    final raw = await store.read(scope);
    if (raw == null) return _History([], {});
    try {
      final value = jsonDecode(raw) as Map<String, dynamic>;
      if (value['version'] != 1 || value['scope'] != scope.stableId) {
        throw const FormatException();
      }
      final recent = (value['recent'] as List)
          .map((item) => ResourceKey.fromJson(item as Map<String, dynamic>))
          .toList();
      final events = Map<String, String>.from(value['events'] as Map);
      final identities = recent.map((key) => key.stableId).toSet();
      final successfulIdentities = events.values.toSet();
      if (!recent.every(scope.contains) ||
          identities.length != recent.length ||
          !identities.every(successfulIdentities.contains) ||
          events.entries.any(
            (entry) =>
                entry.key.trim().isEmpty ||
                entry.key.length > 512 ||
                !identities.contains(entry.value),
          )) {
        throw const FormatException();
      }
      return _History(recent, events);
    } catch (_) {
      // Never include an account identifier or stored record in diagnostics.
      throw const FormatException('Invalid successful resource history');
    }
  }
}

class _History {
  _History(this.recent, this.events);
  final List<ResourceKey> recent;
  final Map<String, String> events;
}
