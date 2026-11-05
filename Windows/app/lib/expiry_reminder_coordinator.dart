import 'dart:async';
import 'dart:convert';

import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:flutter/foundation.dart';

import 'platform_store.dart';
import 'resource_presentation.dart';

abstract class ReminderStore {
  Future<String?> read(ReminderScope scope);
  Future<void> write(ReminderScope scope, String value);
  Future<void> delete(ReminderScope scope);
}

String _recordId(String input) {
  var hash = BigInt.parse('cbf29ce484222325', radix: 16);
  final mask = (BigInt.one << 64) - BigInt.one;
  for (final byte in utf8.encode(input)) {
    hash = ((hash ^ BigInt.from(byte)) * BigInt.from(1099511628211)) & mask;
  }
  return hash.toRadixString(16).padLeft(16, '0');
}

class WindowsReminderStore extends ReminderStore {
  String _key(ReminderScope scope) => 'reminders_${_recordId(scope.stableId)}';
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

/// The implementation must update by stable [ReminderNotice.id] instead of
/// adding another notification. Accepted means platform acceptance, not read.
/// A denied permission must return false, and cancel must be idempotent.
abstract class ReminderNotifications {
  Future<bool> submit(ReminderNotice notice);
  Future<void> cancel(String id);
}

class ReminderNotice {
  ReminderNotice({required this.batch, required this.snapshot});
  final ReminderBatch batch;
  final ExpirySnapshot snapshot;
  String get id => batch.id;
  List<ReminderKey> get members => batch.members;
  String get title => 'BlueGrass · ${members.length} 项资源到期提醒';
  String get body {
    final counts = <ReminderStage, int>{};
    for (final member in members) {
      counts.update(member.stage, (n) => n + 1, ifAbsent: () => 1);
    }
    const labels = {
      ReminderStage.expired: '已到期',
      ReminderStage.oneDay: '24 小时内',
      ReminderStage.threeDays: '1 至 3 天内',
      ReminderStage.sevenDays: '3 至 7 天内',
    };
    final stages = [
      for (final stage in labels.keys)
        if (counts[stage] != null) '${labels[stage]} ${counts[stage]} 项',
    ];
    if (members.length == 1) {
      stages.add(formatExpiry(members.single.expiresAtUtc));
    }
    if (!snapshot.complete || snapshot.nextCursor != null) {
      stages.add('部分同步，仅含已知资源');
    }
    return stages.join('；');
  }
}

/// Only feed the dedicated owned-resource expiry projection, never /nodes.
/// Call in source generation order and pass the corrected UTC clock. Pages in
/// the same generation merge; complete or endOfRound submits one stable batch.
/// The owner must call clearAccount before logout/account changes and should
/// stop its polling/timers separately. This object never connects or renews.
class ExpiryReminderCoordinator extends ChangeNotifier {
  ExpiryReminderCoordinator({required this.store, required this.notifications});
  final ReminderStore store;
  final ReminderNotifications notifications;
  ExpirySnapshot? _snapshot;
  DateTime? _now;
  ReminderLedger _ledger = ReminderLedger();
  ReminderScope? _loadedScope;
  Future<void> _queue = Future.value();
  int _revision = 0;
  bool _disposed = false;
  String? notificationError;
  ExpirySnapshot? get snapshot => _snapshot;
  bool get scopeLoaded => _snapshot != null && _loadedScope == _snapshot!.scope;
  bool get coverageComplete =>
      _snapshot?.complete == true && _snapshot?.nextCursor == null;
  List<ExpirySummary> get items {
    if (_snapshot == null || _now == null) return const [];
    final keys = evaluateReminders(
      _snapshot!,
      ReminderLedger(),
      _now!,
    ).dueItems.map((key) => key.resourceKey).toSet();
    return List.unmodifiable(
      _snapshot!.items.where((item) => keys.contains(item.key)),
    );
  }

  void _changed() {
    if (!_disposed) notifyListeners();
  }

  // Use the controller's corrected clock between resource synchronizations.
  void updateDisplayTime(DateTime effectiveNowUtc) {
    if (_now == null || !effectiveNowUtc.isAfter(_now!)) return;
    _now = effectiveNowUtc.toUtc();
    _changed();
  }

  Future<void> synchronize(
    ExpirySnapshot next, {
    required DateTime effectiveNowUtc,
    bool endOfRound = false,
  }) {
    if (_disposed) return Future.value();
    final old = _snapshot;
    final sameRound =
        old?.scope == next.scope && old?.syncGeneration == next.syncGeneration;
    final merged = <ResourceKey, ExpirySummary>{
      if (sameRound)
        for (final item in old!.items) item.key: item,
      for (final item in next.items)
        if (next.scope.contains(item.key)) item.key: item,
    };
    final current = ExpirySnapshot(
      scope: next.scope,
      items: merged.values.toList(),
      complete: next.complete,
      nextCursor: next.nextCursor,
      syncGeneration: next.syncGeneration,
    );
    _snapshot = current;
    _now = effectiveNowUtc.toUtc();
    final revision = ++_revision;
    _changed();
    return _enqueue(() async {
      if (!_current(revision)) return;
      if (_loadedScope != current.scope) {
        for (final id in _ledger.batches.keys) {
          await notifications.cancel(id);
        }
        _ledger = _decode(await store.read(current.scope), current.scope);
        _loadedScope = current.scope;
      }
      if (!_current(revision)) return;
      // An incomplete page cannot prove a missing resource was reclaimed.
      final validKeys = evaluateReminders(
        current,
        ReminderLedger(),
        effectiveNowUtc,
      ).dueItems.toSet();
      final knownKeys = current.items.map((item) => item.key).toSet();
      final batches = Map<String, ReminderBatch>.of(_ledger.batches);
      for (final entry in _ledger.batches.entries) {
        final retained = entry.value.members
            .where(
              (key) =>
                  key.scope == current.scope &&
                  (validKeys.contains(key) ||
                      !coverageComplete &&
                          !knownKeys.contains(key.resourceKey)),
            )
            .toList();
        if (retained.isEmpty) {
          await notifications.cancel(entry.key);
          batches.remove(entry.key);
        } else if (retained.length != entry.value.members.length &&
            entry.value.status != SubmissionStatus.pending) {
          final replacement = ReminderBatch(
            id: entry.key,
            members: retained,
            status: entry.value.status,
          );
          final accepted = await notifications.submit(
            ReminderNotice(batch: replacement, snapshot: current),
          );
          if (!_current(revision)) {
            await notifications.cancel(entry.key);
            return;
          }
          if (accepted) {
            batches[entry.key] = replacement;
          } else {
            await notifications.cancel(entry.key);
            notificationError = 'NOTIFY-001 · 摘要更新未被系统接受；到期清单仍可查看。';
          }
        }
      }
      final known = {for (final item in current.items) item.key: item};
      final states = Map<ReminderKey, SubmissionStatus>.of(_ledger.states)
        ..removeWhere((key, _) {
          final item = known[key.resourceKey];
          if (item == null) return coverageComplete;
          return !item.owned ||
              item.expiresAtUtc == null ||
              !item.expiresAtUtc!.isAtSameMomentAs(key.expiresAtUtc);
        });
      _ledger = ReminderLedger(states: states, batches: batches);
      if (!_current(revision)) return;
      // A crash after platform acceptance but before its durable acknowledgement
      // leaves a pending identity. Replay that identity even in a new API round.
      if (coverageComplete || endOfRound) {
        for (final previous in _ledger.batches.values.toList()) {
          if (previous.status != SubmissionStatus.pending) continue;
          final members = previous.members.where(validKeys.contains).toList();
          if (members.isEmpty) continue;
          final replay = ReminderBatch(id: previous.id, members: members);
          final accepted = await notifications.submit(
            ReminderNotice(batch: replay, snapshot: current),
          );
          if (!_current(revision)) {
            await notifications.cancel(replay.id);
            return;
          }
          final status = accepted
              ? SubmissionStatus.accepted
              : SubmissionStatus.failed;
          _ledger = ReminderLedger(
            states: {
              ..._ledger.states,
              for (final member in members)
                if (_ledger.states[member] != SubmissionStatus.accepted)
                  member: status,
            },
            batches: {
              ..._ledger.batches,
              replay.id: ReminderBatch(
                id: replay.id,
                members: members,
                status: status,
              ),
            },
          );
          await store.write(current.scope, _encode(_ledger, current.scope));
          notificationError = accepted
              ? null
              : 'NOTIFY-001 · 系统未接受通知，请检查通知权限；到期清单仍可查看。';
          if (!accepted) return;
        }
      }
      final evaluation = evaluateReminders(current, _ledger, effectiveNowUtc);
      if ((!coverageComplete && !endOfRound) || evaluation.batchDraft == null) {
        await store.write(current.scope, _encode(_ledger, current.scope));
        return;
      }
      final draft = revalidateBatch(
        evaluation.batchDraft!,
        current,
        effectiveNowUtc,
      );
      if (draft.members.isEmpty) return;
      _ledger = ReminderLedger(
        states: _ledger.states,
        batches: {..._ledger.batches, draft.id: draft},
      );
      // Save the stable pending identity before crossing the platform boundary.
      await store.write(current.scope, _encode(_ledger, current.scope));
      if (!_current(revision)) return;
      final accepted = await notifications.submit(
        ReminderNotice(batch: draft, snapshot: current),
      );
      if (!_current(revision)) {
        await notifications.cancel(draft.id);
        return;
      }
      _ledger = _ledger.recordSubmission(
        draft,
        accepted: accepted,
        snapshot: current,
        effectiveNowUtc: effectiveNowUtc,
      );
      await store.write(current.scope, _encode(_ledger, current.scope));
      notificationError = accepted
          ? null
          : 'NOTIFY-001 · 系统未接受通知，请检查通知权限；到期清单仍可查看。';
    });
  }

  bool _current(int revision) => !_disposed && revision == _revision;

  Future<void> _enqueue(Future<void> Function() action) {
    final work = _queue
        .then((_) => action())
        .catchError((Object _) {
          notificationError = 'NOTIFY-002 · 通知投递或本地记录失败，请重试；提醒不会改变连接有效期。';
        })
        .whenComplete(_changed);
    _queue = work;
    return work;
  }

  /// Resolve only against the current owned snapshot. An old notification never
  /// reveals another account's cached resource or restores connection rights.
  List<ExpirySummary> itemsForNotification(String id) {
    final current = _snapshot;
    final batch = _ledger.batches[id];
    if (current == null || batch == null || _now == null) return const [];
    final keys = batch.members
        .where((m) => m.scope == current.scope)
        .map((m) => m.resourceKey)
        .toSet();
    return List.unmodifiable(
      current.items.where(
        (item) =>
            current.scope.contains(item.key) &&
            item.owned &&
            isResourceVisible(item.expiresAtUtc, _now!) &&
            keys.contains(item.key),
      ),
    );
  }

  Future<void> clearAccount({bool deleteLocalData = false}) {
    ++_revision;
    _snapshot = null;
    _now = null;
    _changed();
    return _enqueue(() async {
      final scope = _loadedScope;
      for (final id in _ledger.batches.keys) {
        await notifications.cancel(id);
      }
      if (deleteLocalData && scope != null) await store.delete(scope);
      _ledger = ReminderLedger();
      _loadedScope = null;
      notificationError = null;
    });
  }

  @override
  void dispose() {
    _disposed = true;
    ++_revision;
    super.dispose();
  }
}

Map<String, Object?> _keyJson(ReminderKey key) => {
  'resource': key.resourceKey.toJson(),
  'expires': key.expiresAtUtc.toIso8601String(),
  'stage': key.stage.name,
};

String _encode(ReminderLedger ledger, ReminderScope scope) => jsonEncode({
  'version': 1,
  'scope': scope.stableId,
  'states': [
    for (final entry in ledger.states.entries)
      {..._keyJson(entry.key), 'status': entry.value.name},
  ],
  'batches': [
    for (final batch in ledger.batches.values)
      {
        'id': batch.id,
        'status': batch.status.name,
        'members': batch.members.map(_keyJson).toList(),
      },
  ],
});

ReminderLedger _decode(String? text, ReminderScope scope) {
  if (text == null) return ReminderLedger();
  final value = jsonDecode(text) as Map<String, dynamic>;
  if (value['version'] != 1 || value['scope'] != scope.stableId) {
    throw const FormatException('Reminder scope mismatch');
  }
  ReminderKey key(dynamic raw) {
    final item = raw as Map<String, dynamic>;
    final resource = ResourceKey.fromJson(
      item['resource'] as Map<String, dynamic>,
    );
    final expires = item['expires'] as String;
    if (!scope.contains(resource) || !expires.endsWith('Z')) {
      throw const FormatException('Invalid reminder identity');
    }
    return ReminderKey(
      scope: scope,
      resourceKey: resource,
      expiresAtUtc: DateTime.parse(expires),
      stage: ReminderStage.values.byName(item['stage'] as String),
    );
  }

  final states = <ReminderKey, SubmissionStatus>{
    for (final item in value['states'] as List)
      key(item): SubmissionStatus.values.byName(item['status'] as String),
  };
  final batches = <String, ReminderBatch>{
    for (final item in value['batches'] as List)
      item['id'] as String: ReminderBatch(
        id: item['id'] as String,
        status: SubmissionStatus.values.byName(item['status'] as String),
        members: (item['members'] as List).map(key).toList(),
      ),
  };
  return ReminderLedger(states: states, batches: batches);
}
