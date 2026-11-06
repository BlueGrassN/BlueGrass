import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  final f = fixture('reminders/stages.json');
  final now = DateTime.parse(f['nowUtc'] as String);
  const scope = ReminderScope(
      localUser: 'fake-os-user',
      environment: 'fixture',
      dataSource: 'api-v1',
      accountKey: 'fake-account');
  ExpirySnapshot snapshot(List<ExpirySummary> items,
          {bool complete = false, String generation = 'round-1'}) =>
      ExpirySnapshot(
          scope: scope,
          items: items,
          complete: complete,
          syncGeneration: generation);
  ExpirySummary summary(String id, DateTime? expiry, {bool owned = true}) =>
      ExpirySummary(key: key(id), name: id, expiresAtUtc: expiry, owned: owned);
  test(
      'expired reminders retire only after 72 hours, including accepted notices',
      () {
    final expiry = now.subtract(const Duration(hours: 72));
    final snap = snapshot([summary('A', expiry)]);
    final boundary = evaluateReminders(snap, ReminderLedger(), now);
    expect(boundary.dueItems.single.stage, ReminderStage.expired);
    final prior = boundary.dueItems.single;
    final later = evaluateReminders(
        snap,
        ReminderLedger(states: {prior: SubmissionStatus.accepted}),
        now.add(const Duration(microseconds: 1)));
    expect(later.dueItems, isEmpty);
    expect(later.cancelledKeys, [prior]);
    expect(later.batchDraft, isNull);
  });
  for (final c in f['cases'] as List) {
    test(c['caseId'] as String, () {
      final expiry =
          c['expiry'] == null ? null : DateTime.parse(c['expiry'] as String);
      final result = evaluateReminders(
          snapshot([summary('A', expiry)]), ReminderLedger(), now);
      expect(result.dueItems.map((e) => e.stage.name),
          c['stage'] == null ? [] : [c['stage']]);
      if (c['stage'] != null) {
        expect(result.dueItems.single.expiresAtUtc.isUtc, isTrue);
        expect(result.batchDraft!.members, result.dueItems);
      }
    });
  }
  test('partial_summary_is_not_complete', () {
    final old = ReminderKey(
        scope: scope,
        resourceKey: key('B'),
        expiresAtUtc: now,
        stage: ReminderStage.expired);
    final ledger = ReminderLedger(states: {old: SubmissionStatus.pending});
    final result = evaluateReminders(snapshot([]), ledger, now);
    expect(result.coverageComplete, isFalse);
    expect(result.cancelledKeys, isEmpty);
    expect(
        evaluateReminders(snapshot([], complete: true), ledger, now)
            .cancelledKeys,
        [old]);
  });
  test('changed expiry null or explicit loss cancels old keys', () {
    final old = ReminderKey(
        scope: scope,
        resourceKey: key('A'),
        expiresAtUtc: now,
        stage: ReminderStage.expired);
    final ledger = ReminderLedger(states: {old: SubmissionStatus.pending});
    for (final item in [
      summary('A', now.add(const Duration(hours: 2))),
      summary('A', null),
      summary('A', now, owned: false)
    ]) {
      expect(evaluateReminders(snapshot([item]), ledger, now).cancelledKeys,
          [old]);
    }
  });
  test(f['grouped']['caseId'] as String, () {
    final grouped = f['grouped'] as Map<String, dynamic>;
    final items = (grouped['sourceIds'] as List)
        .map((id) =>
            summary(id as String, DateTime.parse(grouped['expiry'] as String)))
        .toList();
    final snap = snapshot(items);
    final result = evaluateReminders(snap, ReminderLedger(), now);
    final draft = result.batchDraft!;
    final included = ReminderBatch(
        id: draft.id,
        members: draft.members.take(grouped['includedCount'] as int).toList());
    final failed = ReminderLedger().recordSubmission(included,
        accepted: false, snapshot: snap, effectiveNowUtc: now);
    expect(evaluateReminders(snap, failed, now).dueItems, hasLength(5));
    expect(failed.states.values, everyElement(SubmissionStatus.failed));
    final accepted = failed.recordSubmission(included,
        accepted: true, snapshot: snap, effectiveNowUtc: now);
    expect(accepted.states.values.where((s) => s == SubmissionStatus.accepted),
        hasLength(3));
    expect(
        evaluateReminders(snap, accepted, now)
            .dueItems
            .map((k) => k.resourceKey.sourceId),
        ['D', 'E']);
    expect(accepted.batches[included.id]!.members, included.members);
  });
  test(
      'same round paging uses stable batch id and revalidates before submission',
      () {
    final first = snapshot([summary('A', now)]);
    final second = snapshot([summary('A', now), summary('B', now)]);
    final batch = evaluateReminders(first, ReminderLedger(), now).batchDraft!;
    expect(evaluateReminders(second, ReminderLedger(), now).batchDraft!.id,
        batch.id);
    final updated = snapshot([summary('A', null)]);
    expect(revalidateBatch(batch, updated, now).members, isEmpty);
    final ledger = ReminderLedger().recordSubmission(batch,
        accepted: true, snapshot: updated, effectiveNowUtc: now);
    expect(ledger.states.values.where((v) => v == SubmissionStatus.accepted),
        isEmpty);
  });
  test('account namespace isolates cancellation and batches', () {
    const other = ReminderScope(
        localUser: 'fake-os-user',
        environment: 'fixture',
        dataSource: 'api-v1',
        accountKey: 'other');
    final k = ReminderKey(
        scope: other,
        resourceKey: ResourceKey(
            environment: 'fixture',
            dataSource: 'api-v1',
            accountKey: 'other',
            sourceId: 'A'),
        expiresAtUtc: now,
        stage: ReminderStage.expired);
    final ledger = ReminderLedger(states: {k: SubmissionStatus.pending});
    final result = evaluateReminders(snapshot([], complete: true), ledger, now);
    expect(result.cancelledKeys, isEmpty);
    final foreign =
        ExpirySummary(key: k.resourceKey, name: 'Other', expiresAtUtc: now);
    expect(
        evaluateReminders(snapshot([foreign]), ledger, now).dueItems, isEmpty);
  });
  test('later page updates existing summary without losing accepted members',
      () {
    final first = snapshot([summary('A', now)]);
    final batch = evaluateReminders(first, ReminderLedger(), now).batchDraft!;
    final ledger = ReminderLedger().recordSubmission(batch,
        accepted: true, snapshot: first, effectiveNowUtc: now);
    final next = evaluateReminders(
        snapshot([summary('A', now), summary('B', now)]), ledger, now);
    expect(next.dueItems.map((k) => k.resourceKey.sourceId), ['B']);
    expect(next.batchDraft!.id, batch.id);
    expect(next.batchDraft!.members.map((k) => k.resourceKey.sourceId),
        ['A', 'B']);
  });
}
