# bluegrass_data_source

Windows API v1 integration. Import `package:bluegrass_data_source/bluegrass_data_source.dart`.
Depends only on the sibling `bluegrass_domain` package at runtime.

```dart
final source = ApiV2DataSource(baseUri: configuredApiBase, store: dpapiStore);
final customer = await source.login(
  username: username, password: password, deviceId: persistentInstallationUuid);
final page = await source.listNodes(perPage: 50);
final latest = await source.getNode(selected.key.sourceId);
```

UI uses `CustomerDataSource`, with `Customer.id/username/displayName` strings.
`restore()` returns the saved customer, recovering a pending/expired access token
first. A still-valid cached access token does not prove current server membership;
the next protected request verifies it. `customer` remains available after a
transient restoration failure. UI should display the error and may retry later.

One `ApiV2DataSource` instance per environment/store in one UI process. Do not
create a coordinator for each list/detail request. Cross-process concurrent use
of one store is unsupported; the app must enforce one owner. Retain device ID
separately across logout, and pass that same installation UUID on login.

## DPAPI adapter

```dart
abstract interface class SessionStore {
  Future<Map<String, Object?>?> read();
  Future<void> replace(Map<String, Object?> snapshot);
  Future<void> clear();
}
```

Map these to `bluegrass/platform` methods `readRecord {key}`, `writeRecord
{key,value}` and `deleteRecord {key}` using JSON strings. `replace` must atomically
and durably replace the entire encrypted record before resolving; `read` returns
a detached JSON map. Exceptions become `storage_error` without retaining raw
platform messages. Use a key scoped to the canonical API base/environment.

Schema version 1 has `environment`, `device_id`, `access_token`, `refresh_token`,
`session_id`, UTC `access_expires_at`, UTC `refresh_expires_at`, and `customer`
with numeric `id`, `username`, `display_name`. `pending_refresh` is null or an
object with original `session_id`, `refresh_token`, `request_id`, UTC `started_at`,
and nullable UTC `retry_not_before`. The adapter need not understand this schema.
The login password is never persisted. Account identity lives inside the record;
resource keys additionally include environment, `api_v2`, customer ID and subscription ID.

## Failure and resource semantics

`ApiFailure.code/status/retryAfter` are structured; `toString` never includes
server messages, response bodies, request URLs or credentials. Match documented
codes for UI text rather than displaying arbitrary server error text.

Refresh retries at most three transport attempts per invocation with exponential
backoff and jitter, always the same persisted token/UUID, and honors both forms
of Retry-After. A wait beyond the default 120-second recovery window returns
`retry_later` while retaining auth and the persisted deadline. A later retry still
uses the original UUID; the server determines whether its result remains
recoverable. The original protected request is retried at most once. Resource
and login calls do not automatically retry; their failure exposes Retry-After
to the caller. An explicit new login changes account generation.

`logout()` invalidates the local generation and clears durable storage before
awaiting best-effort server revocation. `logout_remote_unconfirmed` means local
exit succeeded but remote revocation was not confirmed. `storage_error` means
durable local clearing cannot be claimed. The caller must coordinate disconnect
and caches; this package never changes network connections.

Capabilities start with legacy `connectableNodes` only. Explicitly call
`refreshCapabilities()` after login/restore to negotiate resource queries.
Old-server 404 retains legacy capabilities; transient/auth/contract failures
remain errors. This local server advertises `resourceQueries` and
`expirySummaries`, but `allOwnedResources`, `authoritativeExpectedExit` and
`smsLogin` stay false. The new endpoints have not been deployed by this task.
`expires_at:null` remains null. Missing or invalid `expected_exit_ip` remains
null and never derives from the relay host. This optional metadata does not gate
profile preparation, connection or the independently observed exit IP.
`authoritative_expected_exit` is informational: true is retained; absent,
unrecognized or false values report false without blocking capability negotiation.
Ownership coverage, pagination and session requirements are unchanged.

Nodes may add optional `protocol_config`, matching the frozen native version 1
schema. The parser maps it to immutable `NodeRecord.protocolConfig`; malformed,
unknown, duplicate, explicitly null or conflicting options yield the safe
`invalid_response` failure. A non-SOCKS node must supply a valid explicit config
whose type equals `protocol`. Legacy SOCKS nodes without that field continue to
use their original required username/password fields and retain a null typed
config. Explicit configs allow omitted/empty legacy credentials; nonempty
legacy credentials are accepted only for SOCKS when both exactly match the
typed options. See [protocol integration](../domain/PROTOCOL.md).

This is an additive client parsing contract. No server fields/source mappings
are invented or deployed. Typed options do not alter negotiated capabilities,
ownership or expiry. An authoritative expected exit is not required to connect.

The optional `ResourceQueryDataSource` and `CustomerResourceQueries` extension
preserve existing implementations of `CustomerDataSource`:

```dart
final capabilities = await source.refreshCapabilities();
if (capabilities.resourceQueries) {
  final subscriptions = await source.listResources(kind: 'subscription');
  final assets = await source.listResources(kind: 'asset');
  final expiryPage = await source.listExpirySummaries(kind: 'subscription');
  final record = await source.getResource('11', kind: 'subscription');
  // record.summary is domain ResourceBrief; record.status is server status.
  // Continue each kind separately while page.hasMore, using page.nextCursor.
}
```

Summary pages contain no credentials. Their `ownershipCoverage` is
`conservative`, `consistency` is `per_page`, and `snapshotComplete` is always
false. A terminal cursor, empty page, failure or generic detail 404 must not
delete unseen resources/reminders or prove complete account coverage.
Bare assets have no known customer expiry. See the source evidence and exact
remaining blockers in [RESOURCE_CONTRACT.md](../../client-api/docs/RESOURCE_CONTRACT.md)
and local test evidence in [resource-contract-verification.md](docs/resource-contract-verification.md).
The [ownership coverage audit](docs/owned-resource-source-audit.md) distinguishes
existing source fields from conservative query omissions and unverified runtime
behavior; the completeness flag is not a claim that ownership data is absent.

## Effective server time

`ApiV2DataSource.effectiveNowUtc` is a nullable `DateTime` in UTC. It is available
after a successful authenticated HTTPS response in the current login generation,
including a validated, durably saved login or refresh. Time advances from the
response's `server_time` using an instance-local monotonic `Stopwatch`; wall-clock
jumps do not affect it. If a subsequent sample would move the current effective
time backward, the current value is retained and continues advancing, so a
known expired resource cannot become unexpired from a backward anchor.

```dart
final trustedNow = apiV2Source.effectiveNowUtc;
// Null means no authenticated HTTPS time is available in this generation.
// Pass an available value into reminder/qualification logic as appropriate.
```

The anchor resets on a new login, logout or terminal session invalidation.
Late responses from a previous generation cannot update it. Failed HTTP/auth
responses, invalid envelopes/timestamps and loopback HTTP responses cannot
establish an anchor. Restoring saved credentials without a successful network
response leaves it null; repeated restore within the same live session retains
an established anchor. It is never persisted with tokens or carried across
processes/accounts. Processing and login-storage delays count from receipt of
the response. Valid authenticated response timing is independent of later
resource DTO decoding; it does not establish resource eligibility or coverage.

The base `CustomerDataSource` interface is unchanged. The getter does not alter
existing session expiry/refresh scheduling or Retry-After behavior. Wiring this
value into the optional native `Request.serverTimeUtc` belongs to main/native
integration. Main/app/native consumers
remain responsible for handling a null time without inventing a trusted value.
See [time verification](docs/effective-time-verification.md).

`ApiV2DataSource.environment` exposes the normalized API base string before any
page is fetched. Pair it with data source namespace `api_v2` and the current
customer ID for history/reminder isolation; it matches `ResourceKey.environment`.

`IoHttpTransport` uses platform TLS verification and direct connections, disables
redirects, closes requests on a 20-second total deadline, limits responses to
2 MiB and JSON request bodies to 4096 bytes. HTTP requires explicit
`allowLoopbackHttp: true` and a literal loopback address. Tests inject transport,
clock, request UUID, and delay without credentials or a live API account.

## Verification

From this directory with `E:\tools\flutter\bin\dart.bat`:

```text
dart pub get
dart analyze
dart test --reporter expanded
dart run tool/ablation.dart
```

Evidence and limitations are in `docs/verification.md`; mutations run only in
copies under this package's `.dart_tool/ablation`, not in app-consumed sources.
