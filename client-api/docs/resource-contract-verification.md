# Resource contract verification - 2026-10-12

This records the initial resource contract work. The subsequent ownership-query
fixes and their current verification boundary are recorded in
[resource-ownership-verification.md](resource-ownership-verification.md) and
[resource-ownership-mapping.md](resource-ownership-mapping.md).

Scope: `client-api/` and `Windows/packages/data_source/`. Existing dirty work was
retained on `feature/windows-client`. No commits or deployment were performed.
Laravel was read as source only. No database connections/writes, source
configuration files, live credentials or account fixtures were used.

## Red and green evidence

- HTTP tests first failed because `expected_exit_ip` was absent and all new
  protected routes returned 404 instead of requiring authentication.
- Source tests first failed because the repository lacked the optional
  `ResourceSource` implementation. A subsequent ownership regression test failed
  while nullable asset assignment could authorize a historical subscription;
  the SELECT was tightened to require assignment equality.
- Dart resource tests first failed against the missing resource query/capability
  methods. They now use static types, including the `CustomerDataSource`
  extension, and pass along with all existing package tests.

Final local commands used Go `D:/Program Files/Go/bin/go.exe`, GCC from
`C:/msys64/ucrt64/bin`, and Dart `E:/tools/flutter/bin/dart.bat`:

| Command | Result |
| --- | --- |
| `go test -race -json -count=1 -timeout 5m ./...` | Exit 0; 84 top-level tests passed, 19 top-level DB tests skipped. |
| `go vet ./...` | Exit 0. |
| `dart test --reporter expanded` in data_source | 81 tests passed, exit 0. |
| `dart analyze` in data_source | No issues, exit 0. |
| `git diff --check -- client-api` | No whitespace errors. Git only reported local LF/CRLF conversion notices. |

The first full Go run could not download dependencies from proxy.golang.org.
The final successful run used `GOPROXY=https://goproxy.cn` in that command's
process only; dependency versions and checksum verification were retained.
No global Go/network configuration was changed.

Recorded Go results and the complete skip list:
[resource-contract-tests.txt](resource-contract-tests.txt).
Dart results: [tests](../../Windows/packages/data_source/docs/resource-contract-tests.txt)
and [analysis](../../Windows/packages/data_source/docs/resource-contract-analyze.txt).

## What these tests prove

- HTTP envelope/authorization, session owner binding, kind-bound keyset cursor
  validation, rejection of customer overrides and malformed pagination, legacy
  source compatibility, and summary responses without connection credentials.
- Source ordinary-SELECT/deadline/closure contracts through the existing SQL
  fixture driver, one-statement DTO authorization, expiry conversion and
  status derivation, no upstream expiry substitution, and failure propagation.
- Dart negotiated/legacy capabilities, partial coverage, typed account/resource
  identity, null expiry, malformed/cross-kind DTO rejection, detail ID checking,
  query encoding, input rejection before network, retry metadata, and rejecting
  late resource/capability responses after logout. Existing session, transport,
  node and expected-exit parser tests remain green.

## Explicitly not validated

`GO_API_TEST_CONFIG` was cleared for every Go invocation. The actual isolated
MySQL environment was unavailable and DB writes were not authorized. All 19
DB-backed tests skipped, including new
`TestOwnedResourcesMVCCExpiryReleaseAndTransfer` and
`TestOwnedResourcesPendingUnlinkedAssetsAndSupersededSubscriptions`.
Adding those tests and their isolated fixture columns does not execute them or
change any business schema.

Consequently, SQL engine semantics, concurrent MySQL writer behavior, EXPLAIN,
query latency, shared/legacy ownership completeness and full reminder coverage
are not established by this local run. No endpoint was deployed or tested on
the public server; Windows app/native/service end-to-end behavior is outside
this write scope. Fixed expected egress and full ownership completeness remain
blocked as documented in [RESOURCE_CONTRACT.md](RESOURCE_CONTRACT.md).
