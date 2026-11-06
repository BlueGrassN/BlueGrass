# BlueGrass shared domain contract (partial K-02)

Windows consumers: `packages/domain` (Dart) and `native/core` (Go).
All paths in this document are relative to `Windows/`.

Public Dart import: `package:bluegrass_domain/bluegrass_domain.dart`.

Frozen integration signatures:

```dart
ResourceKey({required String environment, required String dataSource,
  required String accountKey, String kind = 'subscription', required String sourceId});
NodeRecord({required ResourceKey key, required String name, required String protocol,
  required String host, required int port, String username = '', String password = '',
  String countryCode = '', String countryName = '', String city = '',
  DateTime? expiresAtUtc, String? expectedExitIpv4, ProtocolConfig? protocolConfig,
  bool isForwarded = true});
Page<T>({required List<T> items, required bool hasMore, String? nextCursor});
ProfileResult prepareProfile(NodeRecord node, DateTime effectiveNowUtc);
// sealed result: Ready.profile or Rejected.error (code, message).
```

`ConnectionProfile` can only be obtained from `prepareProfile`. It exposes key,
protocol, host, port, username, password, protocolConfig, expectedExitIpv4 and validUntilUtc.
`expectedExitIpv4` is `String?` source metadata only. Missing, changed or malformed
metadata does not gate preparation or connection and is never copied from host.
Invalid resource identity remains SYNC-003. See [EXIT_OBSERVATION.md](EXIT_OBSERVATION.md)
for the current user-authorized semantics superseding the former expected-IP gate.
Null expiry means no known deadline; exact deadline is expired (ELIG-003).
Non-SOCKS protocols require an explicit, validated, matching `ProtocolConfig`.
Only SOCKS5 can use legacy username/password when that field is absent.
Protocol types/options follow the frozen native `protocolconfig/config.go` v1;
see [protocol integration](../packages/domain/PROTOCOL.md) for the public API,
legacy binding rules, equality and current native wrapper coordination issue.

Additional pure Dart API:

```dart
ConnectionState reduceConnection(ConnectionState state, ConnectionEvent event);
List<ResourceBrief> orderRecent(List<ResourceBrief> items, RecentSuccessIndex history);
ReminderEvaluation evaluateReminders(ExpirySnapshot snapshot, ReminderLedger ledger, DateTime effectiveNowUtc);
TrafficView deriveTraffic(TrafficSnapshot? previous, TrafficSnapshot current);
```

`ResourceKey` is value-equal on all five fields. `sourceId` stays a string,
including leading zeroes. No identity is inferred from host/name/index.
Both raw nodes and profiles may omit expected exit; only `prepareProfile` grants
a profile. Protocol validation does not establish live protocol support; protocol,
entry, identity, expiry, forwarding eligibility and session checks remain separate.

## JSON layout and native consumption

| File | Content and use |
| --- | --- |
| `domain/resource.schema.json` | ResourceKey, normalized NodeRecord and IPC ConnectionProfile with explicit protocol binding |
| `domain/protocol.schema.json` | Closed version 1 protocol options, TLS and exact native whitelists |
| `fixtures/protocol/config-v1.json` | Shared raw JSON: valid options plus unknown, duplicate, conflicting, null and malformed negative cases |
| `ipc/messages.schema.json` | Complete bound ControlContext, session-bound ControlRequest, TrafficSnapshot, normalized NativeConfirmation |
| `fixtures/eligibility/cases.json` | Raw-node eligibility and public IPv4 negative cases; no traffic is sent to sample addresses |
| `fixtures/connection/generations.json` | Full contextA/contextB, time, optional expected metadata, observed IPs, ordered events and expected final phase/attempt |
| `fixtures/connection/control-requests.json` | Complete Go-compatible Connect/Pause/ApplyResourceUpdate/Disconnect/ReleaseProtection JSON requests |
| `fixtures/connection/invalid-messages.json` | Unknown fields/version/command and malformed identity negative vectors |
| `fixtures/traffic/counters.json` | Context + base sample + per-case patch, expected rates/completeness, uint64 decimal boundaries |
| `fixtures/reminders/stages.json` | UTC stage boundaries, catch-up, null, equivalent offsets, actual grouped members |
| `fixtures/time/clock.json` | Trusted UTC/monotonic anchor and expected same-runtime reading |

All paths above are relative to `Windows/contracts`. From `native/core`, the
fixture directory is `../../contracts/fixtures`. A Go test may use
`os.ReadFile` + `json.Unmarshal`; typed counter strings must use
`strconv.ParseUint(value, 10, 64)`, not float64. Do not unmarshal counters through
numeric interface values. This package does not provide a hidden Dart-only fixture set.

Generation vector interpretation: `context: "A"` means the complete `contextA`
object in that same file (likewise B). `connect` supplies the corresponding
profile/key; `disconnect` cancels the current attempt; `confirmed` is an injected
native result, not a new Connect command. Its sequence is `eventSequence`, its
time is `nowUtc`, and protection is forwarding. `verifiedExitIpv4` is the event's
observed IP; omission or null means unknown and must not fall back to the file's
`expectedExitIpv4` (profile metadata only). `expectedObservedIpv4`, when present,
asserts the reduced observation, including null. Tests must keep late results
attached to their original complete context. Eligibility `invalidObservedIps`
replaces `invalidExpectedIps`; those values test observations, not profile eligibility.

The session-bound request schema follows the existing Go `control/types.go` and
`engine.Profile` shape: `context`, `requestId`, `sequence`, `command`, `params`;
the profile uses nested `entry` and `auth`. The optional `expectedExitIpv4` wire
field is ignored compatibility metadata (string/null); new GUI profiles omit it.
`configVersion`, `configFingerprint`
and `mode` in the wire fixture are **explicit fixture metadata**. The current API
does not supply authoritative versions/fingerprints; the Dart profile does not
invent them. A production native adapter must obtain/define authoritative
metadata before serializing this request shape. Do not log request/profile JSON.

`sequence`/revision values in this subset are bounded to JSON's exact integer
range (2^53-1). `monotonicNanos` is an int64 JSON number for native Dart/Go
consumers; generic JavaScript decoders are outside this Windows subset.
Upload/download bytes are unsigned decimal **strings**, with uint64 bounds
checked by Dart/Go. Schema maxLength alone does not prove the uint64 upper bound.
`NativeConfirmation.verifiedExitIpv4` uses `bluegrass-public-ipv4`, matching the
domain public-unicast classifier when present; omission/null remains valid.
Expected metadata has no IPv4 validity requirement and must never be compared
with an observation. Raw failed or malformed probes must be normalized to unknown
before publishing a connected confirmation; a malformed non-null normalized
confirmation is rejected by the reducer.

API node JSON uses `protocol_config`; normalized domain/native JSON uses
`protocolConfig`. Omit the field for legacy SOCKS: explicit null is invalid in
the Dart boundary and shared schemas. `ProtocolConfig.toJson()` returns a
detached credential-bearing object; never include it in logs or diagnostics.
The resource schemas require matching config types for all non-SOCKS profiles
and preserve existing legacy fixtures. They do not assert that the current
server supplies protocol options or an authoritative expected exit.

Use `test_contracts.py`'s registered `FormatChecker` with the schemas. The custom
formats enforce UTF-8 credential byte limits (`bluegrass-secret-255/1024`), Go
integer representation (`bluegrass-int`), TLS identity (`bluegrass-server-name`)
legacy/typed authentication binding (`bluegrass-protocol-binding`) and observed
public IPv4 (`bluegrass-public-ipv4`). Its
`protocol_json` rejects duplicate keys, non-JSON constants and raw configs over
16 KiB before schema validation. Ordinary JSON decoding loses duplicate-key
evidence, and generic schema validation without these checks does not enforce
the full contract. Dart uses `ProtocolConfig.parse` for raw config JSON and
`decodeStrictJson` for API envelopes. Protocol parsing accepts the same JSON
surrogate-escape byte counts as native; validators must not crash on them.

## Reducer, ordering, counters and reminders

ConnectionState separately exposes `phase`, last actual `protection`, current
`context`, `profile`, `verifiedExitIpv4`, event/revision sequences and `effects`.
Events are `ConnectRequested(context, profile)`, `DisconnectRequested()`,
`PauseRequested()`, `ClockAdvanced(now)`, `ResourceUpdateAccepted(context,
revision: ..., profile: ..., unavailable: ...)` and `NativeConfirmed(context,
eventSequence: ..., phase: ..., protection: ..., effectiveNowUtc: ...,
verifiedExitIpv4: ..., isSnapshot: ...)`. Effects are one-transition outputs,
not a retained queue. An ignored event clears effects to avoid duplicate sends.

User intent emits effects; it never claims actual protection changed. Matching
native confirmation is required for connected; observed IPv4 may be absent.
A present observation is validated but never compared to expected metadata.
Accepted connected snapshots with no observation clear older IPs. Pause preserves
last actual protection until acknowledgement. Sequence gaps request GetStatus;
only an explicitly marked authoritative snapshot can bridge them. An accepted
resource revision is scoped to the current attempt; late/older versions and
updates to a terminated attempt do not revive it. Config changes require a new
attempt after pausing. Expected metadata is excluded from configuration comparison.
Expiry-only and metadata-only changes emit applyResourceUpdate for native
acknowledgement; adapters must execute/track that effect, not just store UI state.

Call `RecentSuccessIndex.record(reducedState)` only on the result of the current
reduction. Only its recordRecentSuccess effect changes the idempotent monotonic
success order. Persist sequences and acknowledged contexts within local-user
storage. `orderRecent` changes display order only; retain the original Page and
opaque cursor. Merge/filter/de-duplicate pages by ResourceKey before sorting.

TrafficView uses BigInt for cumulative totals; only the already-subtracted byte
delta is converted to double for B/s. First sample has no rate; gaps, regression,
duplicate or epoch changes do not synthesize zero/peak rates. A new resource or
logical session starts from its own current counters. Derivation is stateless:
the caller must filter late contexts against the current native session and
retain the newest accepted baseline (never install an out-of-order sample).
The native `complete` flag must remain false for the rest of a session whose
counters cannot be recovered; two isolated samples cannot reconstruct lost data.

ExpirySnapshot takes `scope`, `items`, `complete`, `syncGeneration`, `nextCursor`.
It is an accumulated, already-accepted summary snapshot, not one page pretending
to be complete. Presence of a cursor prevents coverageComplete=true. The data
source must isolate sync generations and resolve per-resource authority before
evaluation. Missing items cancel existing keys only in a complete snapshot;
explicit ownership loss, null or changed expiry cancels them even in a partial one.

ReminderScope contains localUser/environment/dataSource/accountKey. ReminderKey
adds ResourceKey, normalized UTC expiry and stage. Its stableId is compact JSON
of `[scope.stableId, resourceKey.stableId, expiresAtUtc.toIso8601String(), stage.name]`;
scope stableId is compact JSON of its four fields, resource stableId of its five
fields in constructor order. UTC ISO uses at least millisecond precision, plus
microseconds when nonzero, always `Z`. Equal instants normalize to the same key.
Batch ID is compact JSON of `[scope.stableId, syncGeneration]`; paging/retries in
the same evaluation round keep it stable. The caller persists that round ID.

`evaluateReminders` returns dueItems, cancelledKeys, coverageComplete and an
optional batchDraft. Cancellation effects must be applied to pending/platform
notifications while preserving historical accepted deduplication records.
Select actual included members, call `revalidateBatch(batch, snapshot, now)`
immediately before platform submission, then `ledger.recordSubmission(batch,
accepted: ..., snapshot: latestSnapshot, effectiveNowUtc: now)` after the result.
Only still-valid included members are recorded; failure is not acceptance and
acceptance is not reading/delivery. The library performs no OS notification I/O.

## Verification and boundaries

In `packages/domain`: `dart analyze`, `dart test`.
In `Windows/contracts`: install `requirements-test.txt` into `.schema-tools`, then
`py -3.14 test_contracts.py` with the installed CPython 3.14 dependencies.
Schema validation uses only local registered schemas. The MSYS `python` on PATH
cannot load this workspace's Windows `.schema-tools` extension modules.
The package-local `python tool/ablate.py` runs real safety removals on an isolated
copy and restores them, with counts/source differences in `ABLATION.md`.

This is **partial K-02**, not the complete HTTP/OpenAPI, ResourceQuery/capability,
expiry-summary transport or IPC contract. Hello/rebinding, unbound GetStatus,
CommandAck/full NativeSnapshot serialization, missing commands, production
fingerprints/capabilities and schema-to-transport generation remain to be frozen.
No production API, persistence store, native IPC authentication, engine, system
proxy, WFP/TUN, notification delivery or cross-reboot time recovery is validated
by these pure tests. EffectiveClock refuses runtime changes or monotonic rollback;
it does not impose any extra offline deadline. Keep unsupported scope explicit.
