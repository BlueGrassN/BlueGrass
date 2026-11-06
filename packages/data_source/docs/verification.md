# K06 verification — 2026-10-11

Tool: `E:\tools\flutter\bin\dart.bat`, Dart 3.13.5 stable, windows_x64.
Working directory: `E:\workspace\APP\packages\data_source`.
All implementation, tests and evidence belong to this package. No branches,
commits, parent UI edits, live login, real credentials or deployments.

## Results

- `dart test --reporter expanded`: **68 passed, 0 failed**, exit 0.
  Full output: [verification-tests.txt](verification-tests.txt).
- `dart analyze`: **No issues found**, exit 0.
  Full output: [verification-analyze.txt](verification-analyze.txt).
- `dart run tool/ablation.dart`: **4/4 removed protections cause behavioral
  regression**, experiment exits 0; each deliberately broken test exits 1.
  [Summary](ablation-results.txt). Only copied source under `.dart_tool/ablation`
  was mutated; copies were restored and live package sources remained intact.

## Actual ablation observations

| Temporary removal | Original regression | Observed failure | Decision |
| --- | --- | --- | --- |
| Refresh shared Future reuse | `concurrent_requests_share_one_refresh` | expected 1 refresh, already observed 2 | retain single-flight |
| Generation equality check | `late_refresh_cannot_overwrite_login` | old refresh resolves `FAKE_ACCESS_late` instead of rejecting | retain isolation |
| Durable pending write before refresh HTTP | `pending_uuid_is_durable_before_refresh_network` | expected persisted Map, observed null | retain atomic pending record |
| Restore shared Future reuse | `restore callers await pending recovery instead of exposing a premature customer` | second restore completed early, expected false but observed true | retain restore coordination |

Logs: [single-flight](ablation-single-flight.txt),
[generation](ablation-generation-isolation.txt),
[pending write](ablation-durable-pending-record.txt),
[restore](ablation-restore-single-flight.txt).

## Actual simplification

Deleted the adapter's duplicate dotted-IPv4 regex/octet-validation block and
reused domain `isPublicIpv4`. There is now one shared rule for a fixed public
exit address. Invalid/missing future fields still map to null; no fallback was
introduced. Required store/transport/UI interfaces remain because each has a
real platform or testing consumer; no generic repository or separate snapshot
object hierarchy was introduced.

- Before simplification: the original **65/65** tests passed
  ([log](before-simplification-tests.txt)).
- New loopback/private/documentation-prefix tests first failed against the old
  validator: **6 passed, 3 failed** in the targeted IPv4 group, exit 1
  ([RED log](ipv4-simplification-red.txt)).
- After simplification: the **same original 65 tests** pass
  ([log](after-simplification-original-tests.txt)); all **68/68** including the
  three new cases pass, and analyzer reports no issues.

## Covered behavior

20 concurrent refresh consumers; 20 protected access failures; one original
access retry; UUID persisted before network and reused after network/429/503;
both Retry-After forms; deadline preserved across restart beyond the recovery
window; failed pending write prevents HTTP; crash before and after replacement
save; stale/expired refresh result recovery from committed state; late login
and refresh isolation; terminal auth response invalidates other in-flight reads;
all documented terminal codes; idle refresh expiry; environment separation;
explicit local-only logout error; malformed DTO/envelope; required node fields;
null expiry; opaque cursor on empty pages; missing/future invalid exit values;
unknown server messages excluded from error strings.

Real `dart:io` tests use a local ephemeral HTTP server only: explicit loopback
opt-in, no redirect follow/credential forwarding, response byte limit, and total
timeout abort. Default public endpoints require HTTPS, direct proxy policy and
platform certificate validation with no permissive certificate callback.

## Review and limitations

Final review was an author self-review (no separate reviewer agent available).
Tests caught and fixed terminal-auth late-read acceptance and premature concurrent
restore completion before final verification. The simplification additionally
removed inconsistent future exit validation.

DPAPI channel/atomic filesystem behavior, Windows UI lifecycle, certificate trust
against a real HTTPS service and end-to-end login/connection were not exercised by
this package. SessionStore durability is an adapter contract tested with fault
injection here. One coordinator owns one record in one process; cross-process
store arbitration is an app responsibility. The later 2026-10-12 contract removes
expected-exit gating: missing metadata permits profile preparation and the actual
exit is observed independently after connection. See
[current exit observation contract](../../../contracts/EXIT_OBSERVATION.md).
The original run did not cover assets or summaries; subsequent resource-query
evidence is recorded in resource-contract-verification.md. Complete ownership
snapshots and SMS remain unsupported. No release/Beta gate is claimed from these
package tests.
