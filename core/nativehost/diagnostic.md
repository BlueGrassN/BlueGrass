# Selected-resource diagnostic IPC

The optional engine capability is:

```go
Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)
```

The local engine and authenticated service wrapper implement it. The engine
captures its existing session/generation, checks its open gate, uses only trusted
probe configuration and the selected transport, and honors cancellation. It must
allow concurrent gate closure. Exit observations and probe failures are advisory
and never revoke forwarding. No direct fallback or caller-selected endpoints exist.

## Request and snapshots

Send `Diagnose` with a new request ID and the exact current `context` from a
connected native snapshot. `Diagnose` accepts no configuration parameters; the
other command accepting `context` is `SwitchResource`, which requires its exact
old context and a new profile. The host rechecks eligibility, gate and
proxy ownership before admitting the job.

The normal reply snapshot contains `diagnostic`:

```json
{
  "state": "running",
  "context": { "...": "the complete current ControlContext" },
  "generation": 1
}
```

Poll ordinary `GetStatus` to receive `state: completed` and `report`, containing
the existing `diagnostics.Report`. Other terminal states are `cancelled` and
`failed`, with a fixed `error` and no report. Only the latest job is retained.
The full envelope context and generation must match before the GUI consumes a
report; a result never authorizes a connection. The report's timestamp is a local
observation time, not authoritative server time.

Repeating the latest job's request ID/context returns its current envelope.
Use a new request ID for a new run. A different request while an engine worker
exists is rejected with `DIAGNOSTIC_BUSY`, including after cancellation if that
worker has not returned. An unsupported engine or closed/unknown/expired gate
returns `DIAGNOSTIC_UNAVAILABLE`. Missing/mismatched context returns
`INVALID_CONTEXT`/`STALE_CONTEXT`.

## Binding and limits

- `Binding.Connected` is true only after nativehost admission.
- `Binding.Generation` is the host's increasing diagnostic-job generation.
- `Binding.ResourceID` is lowercase hex SHA-256 of `json.Marshal(ResourceKey)`
  in Go struct order: environment, dataSource, accountKey, kind, sourceId. This
  encodes the complete authenticated key in 64 bounded characters.
- The existing runner computes `ResourceFingerprint` as lowercase hex of the
  first 16 bytes of SHA-256 of that ResourceID string. Nativehost verifies both
  this fingerprint and the report generation.
- A job has a 30-second deadline and one engine-worker slot. Nativehost cancels
  on pause, switch, disconnect, network change, EOF/close and lost eligibility.
  An independent watcher observes controller expiry even without UI polling.
- Reports require version 1, at most 24 unique known result IDs, fixed diagnostic
  codes, valid public observed exit addresses and a maximum encoded size of
  16 KiB. Raw engine errors never enter IPC. Invalid reports produce
  `DIAGNOSTIC_INVALID_REPORT`; engine errors produce `DIAGNOSTIC_FAILED`.
- Cancellation and timeout use `DIAGNOSTIC_CANCELLED` and `DIAGNOSTIC_TIMEOUT`.
  Late/cancelled/wrong-context reports cannot alter a replacement session.

The unconfigured UDP endpoint remains `proxy_udp: skipped/probe_not_configured`.
UDP and exit probe failures do not pause or disconnect. A valid current observation
is `proxy_exit: passed/observed_exit` with a public numeric `ObservedIP`. No cached
observation or expected IP is required to start diagnostics, and a changed observed
address is not an error. Legacy `expected_exit_match` and `exit_mismatch` report
codes are rejected as invalid reports without changing forwarding. Engine errors
return `DIAGNOSTIC_FAILED` with no partial report or private error details.

A completed valid `proxy_exit: passed/observed_exit` updates the controller's
optional `verifiedExitIpv4` snapshot and increments its event sequence. Publication
atomically rechecks the full current context, open gate, clock and unexpired lease;
rejection discards the stale report. Later status polls retain this observation
without publishing the completed job again. Engine caches are never copied into
the snapshot, and a late initial probe cannot overwrite this newer observation.

Diagnostic and other control replies reuse the last accepted traffic sample.
Only explicit valid `GetStatus` requests create traffic samples.
