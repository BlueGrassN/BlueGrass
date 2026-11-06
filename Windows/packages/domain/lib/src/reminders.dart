import 'dart:convert';
import 'resources.dart';

final class ReminderScope {
  const ReminderScope(
      {required this.localUser,
      required this.environment,
      required this.dataSource,
      required this.accountKey});
  final String localUser, environment, dataSource, accountKey;
  bool contains(ResourceKey key) =>
      key.environment == environment &&
      key.dataSource == dataSource &&
      key.accountKey == accountKey;
  String get stableId =>
      jsonEncode([localUser, environment, dataSource, accountKey]);
  @override
  bool operator ==(Object other) =>
      other is ReminderScope && stableId == other.stableId;
  @override
  int get hashCode => stableId.hashCode;
}

enum ReminderStage { sevenDays, threeDays, oneDay, expired }

enum SubmissionStatus { pending, accepted, failed, cancelled }

final class ReminderKey {
  ReminderKey(
      {required this.scope,
      required this.resourceKey,
      required DateTime expiresAtUtc,
      required this.stage})
      : expiresAtUtc = expiresAtUtc.toUtc();
  final ReminderScope scope;
  final ResourceKey resourceKey;
  final DateTime expiresAtUtc;
  final ReminderStage stage;
  String get stableId => jsonEncode([
        scope.stableId,
        resourceKey.stableId,
        expiresAtUtc.toIso8601String(),
        stage.name
      ]);
  @override
  bool operator ==(Object other) =>
      other is ReminderKey && stableId == other.stableId;
  @override
  int get hashCode => stableId.hashCode;
}

final class ExpirySummary {
  const ExpirySummary(
      {required this.key,
      required this.name,
      this.customerRemark = '',
      required this.expiresAtUtc,
      this.owned = true});
  final ResourceKey key;
  final String name, customerRemark;
  final DateTime? expiresAtUtc;
  final bool owned;
}

final class ExpirySnapshot {
  ExpirySnapshot(
      {required this.scope,
      required List<ExpirySummary> items,
      required this.complete,
      required this.syncGeneration,
      this.nextCursor})
      : items = List.unmodifiable(items);
  final ReminderScope scope;
  final List<ExpirySummary> items;
  final bool complete;
  final String syncGeneration;
  final String? nextCursor;
}

final class ReminderBatch {
  ReminderBatch(
      {required this.id,
      required List<ReminderKey> members,
      this.status = SubmissionStatus.pending})
      : members = List.unmodifiable(members);
  final String id;
  final List<ReminderKey> members;
  final SubmissionStatus status;
}

final class ReminderLedger {
  ReminderLedger(
      {Map<ReminderKey, SubmissionStatus> states = const {},
      Map<String, ReminderBatch> batches = const {}})
      : states = Map.unmodifiable(states),
        batches = Map.unmodifiable(batches);
  final Map<ReminderKey, SubmissionStatus> states;
  final Map<String, ReminderBatch> batches;
  ReminderLedger recordSubmission(ReminderBatch batch,
      {required bool accepted,
      required ExpirySnapshot snapshot,
      required DateTime effectiveNowUtc}) {
    final valid = revalidateBatch(batch, snapshot, effectiveNowUtc);
    final status = valid.members.isEmpty
        ? SubmissionStatus.cancelled
        : accepted
            ? SubmissionStatus.accepted
            : SubmissionStatus.failed;
    final updated = Map<ReminderKey, SubmissionStatus>.of(states);
    for (final key in valid.members) {
      if (updated[key] != SubmissionStatus.accepted) updated[key] = status;
    }
    return ReminderLedger(states: updated, batches: {
      ...batches,
      batch.id:
          ReminderBatch(id: batch.id, members: valid.members, status: status)
    });
  }
}

final class ReminderEvaluation {
  ReminderEvaluation(
      {required List<ReminderKey> dueItems,
      required List<ReminderKey> cancelledKeys,
      required this.coverageComplete,
      this.batchDraft})
      : dueItems = List.unmodifiable(dueItems),
        cancelledKeys = List.unmodifiable(cancelledKeys);
  final List<ReminderKey> dueItems, cancelledKeys;
  final bool coverageComplete;
  final ReminderBatch? batchDraft;
}

ReminderStage? _stage(DateTime expiry, DateTime now) {
  if (!isResourceVisible(expiry, now)) return null;
  final remaining = expiry.difference(now);
  if (remaining <= Duration.zero) return ReminderStage.expired;
  if (remaining <= const Duration(days: 1)) return ReminderStage.oneDay;
  if (remaining <= const Duration(days: 3)) return ReminderStage.threeDays;
  if (remaining <= const Duration(days: 7)) return ReminderStage.sevenDays;
  return null;
}

String _batchId(ExpirySnapshot snapshot) =>
    jsonEncode([snapshot.scope.stableId, snapshot.syncGeneration]);

ReminderEvaluation evaluateReminders(
    ExpirySnapshot snapshot, ReminderLedger ledger, DateTime effectiveNowUtc) {
  final items = {
    for (final item in snapshot.items)
      if (snapshot.scope.contains(item.key)) item.key: item
  };
  final complete = snapshot.complete && snapshot.nextCursor == null;
  final cancelled = <ReminderKey>[];
  for (final key in ledger.states.keys) {
    if (key.scope != snapshot.scope) continue;
    final item = items[key.resourceKey];
    if (item == null) {
      if (complete) cancelled.add(key);
    } else if (!item.owned ||
        item.expiresAtUtc == null ||
        !item.expiresAtUtc!.isAtSameMomentAs(key.expiresAtUtc) ||
        _stage(item.expiresAtUtc!, effectiveNowUtc) != key.stage) {
      cancelled.add(key);
    }
  }
  final due = <ReminderKey>[];
  for (final item in items.values) {
    if (!item.owned || item.expiresAtUtc == null) continue;
    final stage = _stage(item.expiresAtUtc!, effectiveNowUtc);
    if (stage == null) continue;
    final key = ReminderKey(
        scope: snapshot.scope,
        resourceKey: item.key,
        expiresAtUtc: item.expiresAtUtc!,
        stage: stage);
    if (ledger.states[key] != SubmissionStatus.accepted) due.add(key);
  }
  final batchId = _batchId(snapshot);
  final previous = ledger.batches[batchId];
  final retained = previous?.members.where((key) {
        final item = items[key.resourceKey];
        return key.scope == snapshot.scope &&
            item != null &&
            item.owned &&
            item.expiresAtUtc != null &&
            item.expiresAtUtc!.isAtSameMomentAs(key.expiresAtUtc) &&
            _stage(item.expiresAtUtc!, effectiveNowUtc) == key.stage;
      }) ??
      <ReminderKey>[];
  return ReminderEvaluation(
      dueItems: due,
      cancelledKeys: cancelled,
      coverageComplete: complete,
      batchDraft: due.isEmpty
          ? null
          : ReminderBatch(
              id: batchId, members: {...retained, ...due}.toList()));
}

ReminderBatch revalidateBatch(
    ReminderBatch batch, ExpirySnapshot snapshot, DateTime effectiveNowUtc) {
  final current = evaluateReminders(snapshot, ReminderLedger(), effectiveNowUtc)
      .dueItems
      .toSet();
  return ReminderBatch(
      id: batch.id,
      status: batch.status,
      members: batch.id != _batchId(snapshot)
          ? []
          : batch.members.where(current.contains).toSet().toList());
}
