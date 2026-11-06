# Domain safety ablation (executed)

Run: `py -3.14 tool/ablate.py` with the configured Flutter SDK Dart on Windows.
Expected-exit metadata no longer gates connection; the former expected-exit mutation was replaced by observed-IP validation.
All mutations were applied only to `.ablation/lib/`; live source was unchanged.
Baseline: 162 passed, 0 failed. Restored: 162 passed, 0 failed.

| Removed mechanism | Passed | Failed |
| --- | ---: | ---: |
| observed-ip-validation | 138 | 24 |
| deadline-check | 161 | 1 |
| native-generation-check | 160 | 2 |
| exact-counter-representation | 159 | 3 |
| counter-sample-continuity | 161 | 1 |
| batch-membership-boundary | 161 | 1 |

## Exact temporary source differences

observed-ip-validation: `lib/src/connection.dart`

```diff
- !isPublicIpv4(event.verifiedExitIpv4)
+ false
```

deadline-check: `lib/src/profile.dart`

```diff
- if (node.expiresAtUtc != null &&
-       !effectiveNowUtc.isBefore(node.expiresAtUtc!)) {
+ if (false) {
```

native-generation-check: `lib/src/connection.dart`

```diff
- if (event.context != state.context ||
-           event.eventSequence <= state.lastEventSequence) return next();
+ if (event.eventSequence <= state.lastEventSequence) return next();
```

exact-counter-representation: `lib/src/traffic.dart`

```diff
- return parsed;
+ return BigInt.from(parsed.toDouble());
```

counter-sample-continuity: `lib/src/traffic.dart`

```diff
- current.sequence == previous.sequence + 1 &&
-       
+ 
```

batch-membership-boundary: `lib/src/reminders.dart`

```diff
- for (final key in valid.members) {
+ for (final key in evaluateReminders(snapshot, this, effectiveNowUtc).dueItems) {
```

All six checks are retained: removing them caused existing behavioral tests to fail.
Full stdout/stderr are in `.ablation/*.log` (ignored local evidence).

## Abstraction review

No forwarding service/repository/factory layer was present to remove. The package has
immutable contract models and pure functions only; its sealed result/event types are
required by K-05. No unperformed simplification is claimed as a source change.
