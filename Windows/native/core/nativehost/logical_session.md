# Durable logical-session continuity

The authenticated service engine optionally implements both methods:

```go
ResumeLogicalSession(engine.ResourceKey) (string, error)
BindLogicalSession(engine.ResourceKey, string) error
```

Nativehost validates the closed profile schema, protocol parameters, mode,
entry DNS preference and current eligibility before journal access. Trusted
server time calibration precedes the eligibility check. The service derives
the Windows SID; the host supplies the entire validated resource key.

A nonempty resumed ID is reused only after the service verifies the same SID
and full resource key. Empty plus nil starts a native-generated ID only when
no existing protection is active. Missing identity/capability on recovered
protection remains `RECOVERY_REQUIRED`; an owner/key mismatch returns
`OWNER_MISMATCH`. The host never releases protection to bypass either error.

After request decoding and preference acceptance, the host durably binds the
selected ID before creating/dispatching the controller attempt. A failed bind
prevents Prepare and returns a fixed error without exposing journal details.
The service remains responsible for exact pending cleanup before runtime rearm.

Restart/upgrade preserves only the logical identity. Runtime epoch, client
epoch and attempt ID are new. A full eligible source profile and successful fresh
Prepare remain necessary before forwarding; an advisory selected-node exit probe
runs after the connected commit. Hello cannot reconstruct a connected
context from the journal. GUI-selected logical-ID overrides are not accepted.

Pause and process Close retain the durable ID. Explicit successful release
retires it; the next connection obtains a new ID. Recovered byte counters are
marked incomplete because cumulative totals are not persisted by this journal.

## Explicit resource switch

`SwitchResource` requires the exact current snapshot `context` plus a new strict
`profile`; `mode`, `allowLan`, `entryDns` and `serverTimeUtc` follow Connect rules.
The new key may change kind/sourceId, but must retain environment, dataSource
and accountKey. A missing/stale context, unchanged resource or cross-owner key
is rejected. Ordinary Connect cannot change an unreleased resource binding.

The host pauses and joins old work before calling the optional service method:

```go
SwitchLogicalSession(oldKey, newKey engine.ResourceKey, newID string) error
```

The service authenticates the SID, confirms exact cleanup, and commits a new
native-generated logical ID while retaining protection. No Release/Restore is
called. The new controller attempt still requires fresh preparation and eligibility.
Its observed exit starts unknown; probe failure does not undo connection. If the
journal commits but controller dispatch rejects the attempt (for example, the
profile expires during commit), the host removes the obsolete old context and
reports protected/paused with no context. A fresh eligible Connect can then
resume the committed destination without releasing protection.

Only that failed SwitchResource reply carries `snapshot.contextRecovery`:

```json
{
  "version": 1,
  "reason": "resource_switch_committed",
  "requestId": "the failed SwitchResource request ID",
  "previousContext": { "...": "the exact old context frozen at admission" },
  "resourceKey": { "...": "the committed destination resource key" }
}
```

The marker requires no context and an unchanged runtime epoch, with
`connection: paused`, `actualMode: tun`, `protection: wfp_blocking`,
`protectionIntent: true`, `gateKnown: true`, `businessGateOpen: false`,
`systemProxyApplied: false` and `proxyRecoveryRequired: false`. It is not stored
in Host and cannot appear on later status responses. Clients must correlate it
to the pending failed switch, its frozen/current old context and destination;
unmarked or unmatched context loss does not authorize rebinding.

For system proxy, SwitchResource reuses the same owned listener/bypass and
original settings record. Same-key Connect also reuses that transaction and
logical ID, but only when the old gate is known closed and paused/failed. An
open gate, lost proxy ownership or changed bypass is never reused. Every retry
requires fresh preparation, and reset engine counters are marked incomplete.
