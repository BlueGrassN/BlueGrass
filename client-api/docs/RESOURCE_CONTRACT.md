# Read-only resource and expected-egress contract

Local implementation: 2026-10-12, branch `feature/windows-client`. No server
deployment, source schema change, database write, external provider call or live
account query was performed. SMS remains deferred. This contract is additive to
the existing password/session/node API.

## Source evidence and the exact remaining blockers

Evidence was read from `E:/comcode/sunipip-dash/backend` without executing Laravel.
Paths and line numbers below refer to that local source snapshot.

| Evidence | Consequence |
| --- | --- |
| `app/Models/ProxyIp.php:132` parses `ip_address` from the first component of a SOCKS connection string; `database/migrations/2024_01_01_000005_create_proxy_ips_table.php:14` describes that string as `ip:port:user:pass`. | The address is a connection endpoint. It is not authoritative proof of externally observed egress. |
| `app/Services/Ny/NyForwardService.php:70` uses that address as `dest_host`; line 140 uses it in the forwarding destination. | A relay destination is still not a guaranteed fixed public exit IP. |
| Source search of PHP application code and migrations found no `expected_exit_ip`, `exit_ip`, `egress`, `public_ip` or `real_ip` field representing a per-resource fixed external IPv4. Existing Chinese exit references concern NY device groups and forwarding selection. | Node DTOs explicitly emit `expected_exit_ip: null`; no value is guessed from subscription host, IP asset, DNS, device group, or observed traffic. An authoritative fixed-egress field/mapping, write ownership and lifecycle rules are still required. |
| `app/Services/Customer/Subscription/SubscriptionListApp.php:23` scopes subscriptions by `customer_id`; `app/Services/Svc/Subscription/SubscriptionStatusService.php:13` derives expired status from customer expiry. | Subscription identity and expiry come from `subscriptions.id`, `customer_id`, and `expires_at`, including expired records. |
| `app/Http/Controllers/Api/V1/Customer/ProxyIpController.php:30` scopes assets by `assigned_customer_id`. Its 72-hour display cutoff at line 36 is a website visibility rule. | Asset ownership is checked separately. The client resource query does not treat the website's expiry cutoff as proof of reclamation. |
| `app/Console/Commands/ExpireSubscriptions.php:349` clears assignment and writes `released_at` during reclamation. `app/Http/Controllers/Api/V1/SubscriptionController.php:2278` transfers the subscription customer and line 2290 transfers asset assignment. | A retained historical subscription alone is insufficient evidence of current asset ownership. |
| `app/Http/Controllers/Api/V1/Customer/SubscriptionController.php:464` accepts the customer's assignment OR null assignment with an actual customer subscription; `IpReclaimGuardService.php:23` rejects an explicit different assignment. | A null assignment is not automatically unowned. A current matching subscription can establish ownership when there is no conflicting current customer entitlement. |
| `app/Services/Svc/Subscription/IpReclaimGuardService.php:26-48` checks delivery identity, occupancy and supplier bindings; `ProvisionSubscriptionService.php:59-76` writes `product_snapshot.upstream_asset`. | Queries now compare the delivery object and check unique supplier binding using ordinary EXISTS/NOT EXISTS. Cleanup locks and its blanket occupancy veto are not copied into reads. |
| `SubscriptionListApp.php:100-106` and customer `SubscriptionController.php:51-57` hide asset/forward relations for pending subscriptions. | Own provisioning/review records are included even with a pending/stale asset binding; the summary does not select that asset's name. |
| `ProxyIpController.php:423` sets current assignment/status without clearing old release timestamps; `SubscriptionService.php:405-417` restores assignment and `last_renewed_at` after renewal. | Subscription lifetime is compared to release time; a bare asset uses current assignment/status, not an unconditional `released_at IS NULL` test. |
| `ProxyIp.php:107` has many subscriptions. Shared mode/count fields exist, but application-code references to shared counts are model/pricing metadata; assignment/transfer code still uses one `assigned_customer_id`. | Same-customer overlapping subscriptions remain separate entitlements. Shared counts do not create membership or override an explicit foreign assignment. Cross-customer conflicting current delivery needs a defined business decision. |

The remaining full-Beta blockers are explicit:

1. No verified fixed-egress source. All new resource summaries have
   `connectable: false`; existing `/nodes` remains a legacy eligible-endpoint list
   with a null expected exit, not proof of Windows connection eligibility.
2. Locally determinable null-assignment, same-customer overlap, delivery identity,
   supplier binding and historical bare-asset suppression are now implemented.
   Remaining decisions are specific: cross-customer same-delivery subscriptions
   without an unambiguous assignment; whether a shared flag can override an
   explicit foreign assignment; and whether failed provisioning with an unpaid
   refund is a current resource or a commercial claim. Corrupt/noncanonical
   identity and unknown statuses are not silently repaired. See the
   [lifecycle mapping matrix](resource-ownership-mapping.md).
   `all_owned_resources` remains false; no complete reminder coverage is claimed.
3. No account-wide snapshot token, monotonic resource revision, or tombstone
   feed. Page completion does not prove absence/reclamation. Generic detail 404
   also does not prove the exact cause or authorize deleting reminder state.
4. Isolated MySQL integration, source EXPLAIN and production-scale runtime
   behavior need separate authorized verification. Local unit SQL-boundary
   checks do not execute MySQL or prove query latency.

## Capability negotiation

Authenticated `GET /api/client/v1/capabilities` returns the normal envelope with:

```json
{
  "connectable_nodes": true,
  "resource_queries": true,
  "all_owned_resources": false,
  "expiry_summaries": true,
  "sms_login": false,
  "authoritative_expected_exit": false,
  "resource_kinds": ["subscription", "asset"],
  "ownership_coverage": "conservative"
}
```

`resource_queries` and `expiry_summaries` advertise paginated queries, not
complete ownership/reminder coverage. The optional Go `model.ResourceSource`
extension leaves existing implementations of `model.Source` compatible. When
absent, the two query flags are false and query endpoints return 501
`capability_unavailable`. An older server may return 404 `not_found` for
capabilities; the Dart adapter then retains legacy node-only capabilities.
Authentication/transient/malformed responses are surfaced as errors, never
successful empty results. Login/restore do not add mandatory new HTTP calls.

## Endpoints and identity

All paths use the existing authenticated session, request timeout, read rate
limit, no-store headers and safe JSON envelope/error handling.

| Path | Meaning |
| --- | --- |
| `GET /resources?kind=subscription&per_page=50` | Customer subscription summaries, including supported non-connectable/expired/pending resources. |
| `GET /resources?kind=asset&per_page=50` | Directly assigned assets not represented by a proven current subscription of the same customer. |
| `GET /resources/expiry?kind=subscription&per_page=50` | Same lightweight projection, independently paginated for reminders. Also supports `kind=asset`. |
| `GET /resources/subscription/11` | Reauthorize and fetch one subscription summary. |
| `GET /resources/asset/11` | Reauthorize and fetch one assigned asset summary. |

`kind` defaults to `subscription`. Query both kinds to cover this implementation's
supported scope; an asset represented by a current, same-customer bound
subscription is not emitted again as a bare asset. Foreign, stale-delivery and
terminal subscription rows cannot suppress it. IDs are decimal strings, scoped by environment, account,
data source and kind. An asset ID must never be sent to `/nodes/{id}`.

`per_page` defaults to 100 on the server and 50 in Dart; valid range 1-100. Each
query reads at most that many emitted rows plus one lookahead, using ascending
ID keysets. `cursor` is opaque and tied to the kind. A cursor from another kind,
duplicate parameters, unknown parameters (including customer overrides), zero
IDs, malformed cursors and invalid limits return 422. No OFFSET/total/COUNT.

```json
{
  "items": [
    {
      "id": "11",
      "kind": "subscription",
      "name": "美国-198.87.96.137",
      "customer_remark": "我的办公节点",
      "status": "expired",
      "owned": true,
      "connectable": false,
      "expires_at": "2026-10-10T04:00:00Z"
    }
  ],
  "pagination": {"per_page": 50, "has_more": false},
  "coverage": {
    "kind": "subscription",
    "consistency": "per_page",
    "ownership": "conservative",
    "snapshot_complete": false
  }
}
```

`name` is the actual `proxy_ips.asset_name`, with surrounding whitespace removed,
never `subscriptions.customer_remark`. A present asset name is returned directly
(for example, `美国-198.87.96.137`) without an appended subscription or asset ID.
Names can repeat; identity remains the separate `kind` and `id`. This applies to
resource lists, details and expiry summaries, and to node lists and details.

`customer_remark` is a separate, always-present string from the current owner's
`subscriptions.customer_remark`, with surrounding whitespace removed. Null,
empty or whitespace-only remarks return `""`. Pending provisioning/review
subscriptions retain their own customer remark even while their asset name is
withheld. Bare `kind=asset` summaries return `""`: they have no corresponding
current subscription whose remark can safely be used. Internal
`subscriptions.remark` / `proxy_ips.remark` are never selected or exposed.
The field is additive; clients talking to an older server may treat an absent
`customer_remark` as empty.

Null, empty or whitespace-only asset names produce `Resource #ID` in resource
summaries. Pending provisioning/review subscriptions still suppress unverified
asset names and use this neutral fallback. Nodes fall back to `country_name #ID`
or `Node #ID` when the country name is also missing. Customer remarks never
replace or provide a fallback for the asset name. Names are not inferred from
connection hosts, SOCKS strings or observed IPs; an IP embedded in a name does
not establish an authoritative expected exit.

Bare assets have `expires_at: null` because no customer subscription expiry is
available. Neither endpoint selects upstream expiry or connection credentials.
UTC conversion uses the existing configured source timezone. Null remains null;
an `active` subscription with expiry at/before the source clock is projected as
`expired` without writing the business row.

## Ownership selection and concurrency

Subscription summaries require authenticated `s.customer_id`, an enabled and
non-deleted customer, and status active/expired/suspended/provisioning/review.
Pending records are owned order summaries regardless of provisional binding;
their asset name is suppressed. Other subscriptions require a live non-deleted
asset in assigned/expired/disabled state, matching delivery identity, a release
marker older than activation/renewal (or no marker), and matching assignment OR
null assignment with no conflicting customer entitlement to the current delivery.
Supplier-backed bindings require one exact matching instance and no second
instance for that asset. Missing legacy snapshots are supported; present
snapshots must match the canonical object written by Laravel.

Multiple subscriptions for the same customer are retained independently. A
higher subscription ID alone never revokes ownership. With explicit matching
assignment, a foreign historical subscription does not suppress the current
customer. Null assignment plus overlapping foreign current delivery remains
ambiguous and is omitted; a shared count/flag cannot override this boundary.

Bare assets require authenticated current assignment, enabled/non-deleted
customer and a live asset in assigned/expired/disabled state. Their anti-join
uses the same bound-subscription predicate and the **same customer**. A foreign,
released, mismatched-delivery or terminal subscription cannot hide a bare asset.
Old release markers alone do not contradict a subsequently restored current
assignment: the actual release paths clear assignment atomically. Bare summaries
do not borrow expiry from any unproven subscription and do not authorize a
connection. Account/asset status and customer identity are checked in each SELECT.

Each detail performs the same authorization as its list in one statement. A
prior authentication read cannot authorize the DTO snapshot. There are no
source writes, source transactions, locking reads, cached ownership decisions,
provider calls, or session-table changes. Existing source read-only pool and
query deadlines remain in force. Keysets and `LIMIT` bound returned data; actual
rows examined and query plans still need isolated DB validation, particularly
the correlated subscription checks.

Different pages/kinds can observe concurrent transfers or renewals. A terminal
page finishes a traversal only. The server exposes no consistent global
snapshot, revision, or authoritative deletion proof. Reminder consumers may
update observed records but must retain unknown coverage and cannot delete
unseen reminders or declare complete account coverage from these pages.

## Dart integration

`CustomerDataSource` remains unchanged. The exported optional
`ResourceQueryDataSource` plus `CustomerResourceQueries` extension adds
`refreshCapabilities`, `listResources`, `listExpirySummaries`, and `getResource`.
Existing alternate implementations fail with `capability_unavailable` through
the extension without gaining abstract methods.

`OwnedResourceRecord.summary` reuses domain `ResourceBrief` and `ResourceKey`;
`status` preserves the server status. `ResourceQueryPage` wraps domain
`Page<OwnedResourceRecord>` and exposes the coverage boundary. No domain, app or
native changes are required. Account/session generation isolation applies to
all new authorized requests; login/restore/logout reset negotiated capabilities.
An absent or malformed expected exit still maps to `NodeRecord.expectedExitIpv4`
null through the existing parser and must block connection preparation.

The adapter never fetches node credentials while synchronizing summaries.
Callers must keep a traversal per kind and track partial errors separately from
terminal cursors. Returned resource data is not an authorization to connect,
remove protection, delete unseen reminders, or claim full-Beta completion.
