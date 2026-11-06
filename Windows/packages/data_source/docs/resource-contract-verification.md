# Resource query verification - 2026-10-12

`E:/tools/flutter/bin/dart.bat test --reporter expanded`: 81 tests passed, exit 0.
`E:/tools/flutter/bin/dart.bat analyze`: no issues, exit 0.

- [Full package test output](resource-contract-tests.txt)
- [Static analysis output](resource-contract-analyze.txt)
- [Server contract and source evidence](../../../../client-api/docs/RESOURCE_CONTRACT.md)
- [Combined verification and skipped DB tests](../../../../client-api/docs/resource-contract-verification.md)

The initial new-query tests failed against the absent methods. Final tests use
static types and exercise capability negotiation/old-server behavior, typed
resource summaries/details, UTC/null expiry, invalid DTOs/pagination, no-network
input validation, transient failure handling and late replies after logout.
Existing tests for sessions, HTTP and expected-exit parsing also passed.

The optional `ResourceQueryDataSource` interface and `CustomerResourceQueries`
extension leave `CustomerDataSource` implementations compatible. No new domain
fields were needed: `NodeRecord.expectedExitIpv4`, `ResourceKey`, `ResourceBrief`
and `Page` are reused unchanged.

The implementation is local only. No test account, public API request, database
write or deployment was used. The API has no authoritative egress source and
only conservative current-ownership queries. Consumers must keep missing exit
values blocked and must not treat terminal cursors as a complete snapshot or
deletion evidence. Full Windows/reminder end-to-end behavior remains outside
this package's test coverage.

The subsequent [ownership source audit](owned-resource-source-audit.md) refines
the blocker wording: existing assignment, shared metadata and delivery snapshot
fields are present. Conservative query omissions and missing DB validation must
not be described as proven absence of upstream ownership data.
