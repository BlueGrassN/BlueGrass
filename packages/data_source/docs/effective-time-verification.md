# Effective server time verification - 2026-10-12

Write scope: `packages/data_source/` only. Existing resource/API edits
were preserved. No domain/base CustomerDataSource interface, server, app or
native changes; no commit, deployment, database access or live account use.

## Public surface

- `ApiV2DataSource.effectiveNowUtc`: nullable UTC DateTime, computed from an
  accepted authenticated HTTPS `server_time` plus monotonic Stopwatch elapsed
  time. It never uses the DateTime wall clock for elapsed time or a fallback.
- `ApiV2DataSource.environment`: the normalized `_baseUri.toString()`, available
  before fetching a page. Resource data source identity stays `api_v2`.

SessionCoordinator has two optional local callbacks: accepted authenticated
response and generation reset. Acceptance follows generation checks; login and
refresh additionally validate token/account identity and finish durable storage.
Receipt timing is held in weak per-response metadata, outside DTOs and token
storage. Reset clears the anchor before asynchronous logout/termination work.

A sample older than the currently advancing effective time is clamped to that
current value and continues advancing. Account/login generations do not inherit
the previous generation's anchor or floor. Restoring cached credentials in a new
instance leaves time unknown until an authenticated HTTPS response succeeds.
The effective-time getter does not change existing token refresh scheduling.

## Tests

The first focused run failed because `effectiveNowUtc` did not exist. Its
pending-read/new-login case also reached the timeout after the missing-getter
failure interrupted release of its pending fixture. See the unmodified
[RED output](effective-time-red.txt). The implemented focused suite then passed;
three further acceptance/refresh cases were added before full verification.

Final commands used `E:/tools/flutter/bin/dart.bat` in this package:

| Command | Result |
| --- | --- |
| `dart test --reporter expanded` | 95 passed, exit 0, including 14 new time/generation tests. |
| `dart analyze` | No issues, exit 0. |
| `dart format` on the three changed/new Dart files | Formatted successfully. |

- [Full test output](effective-time-tests.txt)
- [Analysis output](effective-time-analyze.txt)
- [Time tests](../test/effective_time_test.dart)

Coverage includes real Stopwatch progression through wall-clock jumps; backward
samples after a known expiry; forward samples; failed HTTP/invalid timestamps;
malformed login and storage failure; HTTPS versus loopback HTTP; cached and
refreshing restore; receipt-to-persistence elapsed time; refresh identity
mismatch; old read/refresh/login replies across generations; logout responses;
and terminal authentication invalidation. The existing 81 session/transport/
resource tests remain green.

These are local tests with synthetic transport data and real monotonic timers.
They do not claim deployed server clock accuracy, Windows native clock
calibration, reminder scheduling or end-to-end qualification. Main can read a
fresh getter value for the optional native `Request.serverTimeUtc`; null still
means no trusted anchor in the current generation.

The separate [ownership audit](owned-resource-source-audit.md) clarifies that
source assignment/shared/delivery fields exist; current conservative selection,
unmapped lifecycle cases and skipped DB verification limit full ownership
coverage. This time change does not change those capabilities.
