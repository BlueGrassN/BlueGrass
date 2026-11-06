# NativeClient committed-switch context recovery

Date: 2026-10-12 (Asia/Hong_Kong).

Scope: integration-review finding 2 only. This task changes
`app/lib/native_client.dart` and adds `app/test/native_context_recovery_test.dart`
and `app/test/utils/native_context_recovery_fixture.dart`. AppController and the
existing protected-switch test/fixture remain owned by main. Nativehost's marker
and Go regression tests were implemented separately by Poincare and checked
against this client contract. No network, service, WFP, proxy or adapter mutation
is used by these tests.

## Exact transition contract

Only a failed reply to the currently pending `SwitchResource` can retire the
client's bound context after the service has committed its journal destination.
The reply snapshot must omit the `context` field entirely and include:

```json
{
  "contextRecovery": {
    "version": 1,
    "reason": "resource_switch_committed",
    "requestId": "the-pending-request-id",
    "previousContext": "the-exact-previous-ControlContext-object",
    "resourceKey": "the-committed-destination-ResourceKey-object"
  }
}
```

The two object placeholders above represent their existing complete typed wire
objects, not strings in the real protocol. No extra marker fields are accepted.
The client verifies all of these conditions before clearing its private context:

- The reply is `ok:false` for the pending SwitchResource; the marker request ID
  matches the frozen request. It is not an event, Hello, GetStatus, successful
  switch or unrelated rejection.
- Snapshot runtime equals the runtime bound by this child's Hello. If a top-level
  runtime is supplied it also matches. The old context is exactly equal to both
  the request's frozen context and the currently bound context, including client,
  runtime, logical-session, attempt and complete resource key.
- Destination key equals the request's frozen profile key, differs from the old
  key, and retains the same environment, dataSource and accountKey. Kind/sourceId
  may change. The marker's old/destination objects receive normal strict shape
  validation, including schemaVersion 1 for ControlContext.
- `connection:paused`, `actualMode:tun`, `protection:wfp_blocking`,
  `protectionIntent:true`, `gateKnown:true`, `businessGateOpen:false`,
  `systemProxyApplied:false`, and explicitly `proxyRecoveryRequired:false`.
- `context` is absent, including rejection of explicit null, malformed objects
  or the old context alongside a recovery marker.

The safe reply remains a failed command and preserves its existing sanitized
error handling. Clearing the binding grants no exit proof or forwarding. The
caller receives the original validated snapshot, so AppController can clear its
own current-attempt context from that response; no new public getter is needed.
Afterward ordinary contextless protected status/Pause replies remain valid and a
fresh Connect may bind the committed resource after native eligibility checks.
Diagnose and SwitchResource remain locally unavailable until a new context is
bound. A rejected switch before journal commit retains the original context.

Nativehost emits the marker only in its post-commit/rejected-Apply branch, using
an old context captured at admission. It does not store the marker in Host or
emit it in subsequent status replies. See `nativehost/host.go`,
`nativehost/switch_resource.go`, and `nativehost/context_recovery_test.go`.

## Regression evidence

The new tests exercise the real NativeClient over an isolated Dart subprocess
pipe; only the native peer is a fixture. They do not replace the client transport,
request queue, decoder, identity checks or cleanup behavior.

Initial RED run: 35 passed and 4 failed. The valid committed-switch recovery
failed with `NATIVE_CONTEXT_MISMATCH`; the old-event-after-recovery scenario failed
at the same missing transition. A malformed marker alongside the old context and
a marker on initial Hello were previously ignored and accepted, contrary to the
explicit marker contract. Raw result:
`.artifacts/verification/native-context-recovery-red.log`.

A subsequent focused RED confirmed that an explicit `context:null` was accepted
despite nativehost's omitted-field-only contract. The final client rejects it.
Evidence: `.artifacts/verification/native-context-recovery-null-red.log`.

Coverage includes missing/malformed/extra marker fields, integer version,
reason/request ID, both runtime locations, old client/session/attempt/key,
destination mismatch, cross-account/environment/source switches, same-resource
switch, protection/gate/proxy invariants, unsolicited/status/success markers,
queued stale attempts, retained binding before commit, and stale old-context
events after recovery. The positive path confirms GetStatus, fresh Connect and
Diagnose remain usable on the same child without Disconnect or Release.

Commands from `Windows/app` use the installed Flutter tool snapshot directly so
they do not contend with main's Flutter launcher lock:

```powershell
$dart = 'E:/tools/flutter/bin/cache/dart-sdk/bin/dart.exe'
$flutterTool = 'E:/tools/flutter/bin/cache/flutter_tools.snapshot'
& $dart $flutterTool test --no-pub test/native_context_recovery_test.dart test/native_client_test.dart --reporter expanded
& $dart analyze lib/native_client.dart test/native_context_recovery_test.dart test/utils/native_context_recovery_fixture.dart
& $dart format --output=none --set-exit-if-changed lib/native_client.dart test/native_context_recovery_test.dart test/utils/native_context_recovery_fixture.dart
```

The final combined result is **118 tests passed**: 40 new recovery tests and
78 existing NativeClient tests, recorded in
`.artifacts/verification/native-context-recovery-green.log`. Static analysis
reports no issues; formatting reports zero changed files. Analysis evidence is
`.artifacts/verification/native-context-recovery-analyze.log`.

The actual nativehost marker implementation was read and its targeted regression
suite was independently checked from `core`:

```powershell
go test -mod=readonly -tags with_gvisor -count=1 -run 'Test.*ContextRecovery|TestSwitchResourceExpiryAfterJournalCommit' ./nativehost
```

Result: PASS. Evidence:
`.artifacts/verification/native-context-recovery-host.log`. Poincare also reported
its full nativehost race/vet pass; that owner report is separate from the commands
run here. Main's unified app verification and finding 3 integration remain its
own checkpoint.

## Frozen source fingerprints

SHA-256, relative to `Windows/`:

```text
app/lib/native_client.dart
B584FB6E6C3C06073C9FD919029616172A6EF1640501E10F638361B5DE878354
app/test/native_context_recovery_test.dart
A0B651B69B01EA4DEF688AA4242ABD86DB8DCA6A0211641CC4E8C8BA12DFAFC5
app/test/utils/native_context_recovery_fixture.dart
79ACC18B335603B3BFA27F3524FBE0AB6A74843CDB0D4EB1EF2831C75DBE986D
```
