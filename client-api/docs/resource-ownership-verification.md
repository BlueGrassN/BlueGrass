# Ownership-query follow-up verification — 2026-10-12

Write scope is `client-api/` only. Laravel source was inspected read-only under
`E:/comcode/sunipip-dash/backend`; no PHP execution, live-account access, source
configuration/credential reads, database operations or deployment. All existing
dirty endpoint, Dart, app and native changes were preserved. No commits or
additional agents.

## Changes

- `internal/source/resources.go`: shared current-binding/delivery predicates;
  supported null assignment; no max-ID suppression; same-customer bare-asset
  anti-join; supplier identity checks; pending order projection without asset
  name; current direct assignment survives a historical release marker.
- `internal/source/resources_test.go`: no-network SQL-boundary assertions and
  preservation of separate subscription identities/expiries; existing script
  driver still rejects writes, transactions, locking reads and unbounded pages.
- `internal/integration/resources_test.go`: corrected historical-subscription
  expectation plus 32 lifecycle/bare-asset fixture cases. List/detail are checked
  against the same expected ownership; foreign detail, expiry and incomplete
  coverage are checked too.
- `internal/testdb/testdb.go`: **test-schema declarations only** gain the existing
  source JSON/supplier/shared fields and instance tables required by fixtures.
  Neither this schema nor the fixture DML was executed.
- API/resource docs now link the implemented matrix and exact residual cases.
  No model/interface, capability value, cursor or server deployment changed.

## Executed verification

From `E:/workspace/APP/client-api`, using
`D:/Program Files/Go/bin/go.exe` and existing `C:/msys64/ucrt64/bin` GCC in the
process PATH. `GO_API_TEST_CONFIG` was explicitly empty for every run, ensuring
the isolated database gate skipped before reading a config or opening a socket.

| Check | Result |
| --- | --- |
| New ownership-source predicate test before implementation | Failed: missing null-assignment support, missing delivery/supplier checks, max-ID suppression and foreign bare-asset suppression. |
| `go test ./internal/source -count=1` | Exit 0 after implementation. |
| `go test -race -json -count=1 ./...` | Exit 0; all eight tested packages completed without a failure/race. Source: 48 top-level tests passed; HTTP: 28 passed. |
| `go vet ./...` | Exit 0. |
| `gofmt -l` for changed Go files | No files reported. |

[Full race output](resource-ownership-tests.jsonl) is retained. Go emitted 88
top-level `pass` events and 19 top-level `skip` events. **Two of those passes are
table-test containers whose entire 32 MySQL subcases were skipped.** They must
not be counted as executed SQL semantics tests; the other 86 top-level pass
events and local source/HTTP checks do not validate MySQL query results either.

## Explicitly skipped / unverified

- All isolated-MySQL fixture writes and assertions, including the 32 new cases;
  authentication/session DB tests and other integration checks behind the same
  gate were skipped. There was no database read or write in this follow-up.
- MySQL JSON comparison behavior, actual execution plans/rows examined,
  production-scale latency and runtime MVCC during transfers/renewals have not
  been revalidated. Ordinary non-locking SQL, limits and context deadlines are
  source/test-boundary evidence, not runtime concurrency proof.
- No public-server/API deployment, live ownership data survey, or client/native
  end-to-end test. Prior health checks do not verify these local query changes.

The current scope/matrix is [resource-ownership-mapping.md](resource-ownership-mapping.md).
Cross-customer shared-ownership precedence and failed-order/refund classification
are specific remaining product decisions; broken delivery identities need data
reconciliation. Known normal relationships are implemented rather than left as
generic external blockers. Complete global snapshots remain a separate feature:
`snapshot_complete:false`, partial coverage and generic 404 semantics are kept.
