# Current ownership mapping — 2026-10-12 follow-up

This is the current implementation mapping. It supersedes the earlier list of
unimplemented query predicates in
`Windows/packages/data_source/docs/owned-resource-source-audit.md`; that audit's
Laravel evidence remains useful, but null assignment, max-ID suppression and
bare-asset suppression are no longer left as implementation blockers.

Public DTOs, pagination, capability names and generic unavailable responses are
unchanged. `owned:true` refers to the emitted resource kind; a pending subscription
is an owned pending order, not proof of its provisional asset binding. No
connection authority, expected egress or complete account snapshot is invented.
`all_owned_resources:false`, `ownership_coverage:conservative` and
`snapshot_complete:false` remain honest boundaries, not a reason to omit proven
relationships.

## Evidence and implementation decisions

All Laravel references below are read-only source paths relative to
`E:/comcode/sunipip-dash/backend`. No Laravel bootstrap, provider call, database
query, credentials/config read or deployment was used.

| Source | What it proves / what the SELECT now does |
| --- | --- |
| `app/Services/Customer/Subscription/SubscriptionListApp.php:23-31`; `app/Http/Controllers/Api/V1/Customer/SubscriptionController.php:40-57` | A subscription belongs to `customer_id`. Listing/reading historical orders is broader than current asset ownership, so terminal/reclaimed bindings cannot simply be copied into the current-assets view. |
| `Customer/SubscriptionController.php:464-484`; `app/Services/Svc/Subscription/IpReclaimGuardService.php:23-28` | Null assignment is a supported legacy condition. Include own nonterminal subscription with current delivery, live asset and no current competing customer. Explicit foreign assignment is not treated as null. |
| `app/Services/Svc/Subscription/ProvisionSubscriptionService.php:59-76` | A delivered subscription saves exactly `proxy_ip_id`, `spark_instance_id`, `ipipv_instance_id` in `product_snapshot.upstream_asset`. A present canonical snapshot must equal the current object, otherwise that historical delivery cannot represent the asset. Missing/JSON-null snapshot uses legacy binding checks. No product JSON is returned. |
| `IpReclaimGuardService.php:30-37`; `app/Models/ProxyIp.php:107-113` | Multiple subscription rows exist. “Other occupancy” blocks cleanup, not read visibility. The former global `newer.id > s.id` veto is removed; current subscriptions of one customer retain their own ID/status/expiry. |
| `IpReclaimGuardService.php:40-48` | A supplier pointer must identify the sole instance linked to the asset. Use matching EXISTS plus NOT EXISTS for a second instance, with binary string identity comparison. Do not aggregate/count or lock. Two supplier pointers, missing binding, duplicates or mismatched identity do not establish a current subscription binding. |
| `app/Console/Commands/ExpireSubscriptions.php:209-214,349-355`; admin `ProxyIpController.php:452-463,509-514`; admin `SubscriptionController.php:1181-1198` | Reclamation/release/unassignment clears the single current assignment and changes status/deletion state. Expiry alone and the customer page's 72-hour cutoff are not release proof. |
| `app/Services/Svc/Subscription/GatewayProxyService.php:92-96`; `app/Services/SubscriptionService.php:378-420` | Subscription reactivation uses `last_renewed_at` or `started_at` strictly after the release marker. Equality or unknown activation does not re-establish the old subscription. |
| admin `ProxyIpController.php:391-425,839-858`; `UpstreamRenewalService.php:342-363` | Assignment/import and successful restoration write current customer/status, and need not clear historical `released_at`. A currently assigned live bare asset therefore remains listable with an old marker. A cancelled/foreign/old-delivery subscription cannot suppress it; only the same customer's proven current binding can. |
| admin `SubscriptionController.php:2262-2290` | Transfer updates subscription ownership and transfers the asset when its assignment still matches the source. Each SELECT checks both current fields; old-customer detail reads fail when no longer owned. A present conflicting assignment is not overridden by the subscription ID or a shared counter. |
| `SubscriptionListApp.php:100-106`; customer `SubscriptionController.php:53-57` | Pending subscriptions deliberately hide the asset relation. Include own provisioning/review orders with null, deleted or provisional foreign bindings, but project no asset name. A pending row suppresses a bare asset only if its actual binding also passes the common current-binding predicate. |

## Current-case matrix

| Case | Result |
| --- | --- |
| Null assignment, own current subscription, no competing customer | Include subscription, including expired but unreclaimed. |
| Two own current subscriptions to the same asset, dedicated or shared-labelled | Include both distinct subscription resources; no max-ID winner. |
| Explicit own assignment, unrelated newer foreign historical row | Include own valid subscription; foreign row is not authorization for the other customer. |
| Null assignment, foreign cancelled/refunded or provably old delivery | Does not block own current subscription. |
| Null assignment, two customers with retained subscriptions to the same current delivery | Omit the conflicting bound subscriptions; do not infer shared membership. |
| Shared mode/count exists, but no subscription/assignment links a requested customer | No ownership inferred. |
| Matching canonical delivery snapshot or absent legacy snapshot with valid binding | Include, subject to remaining current-state checks. |
| Changed/malformed snapshot, missing/duplicate/different supplier instance, dual supplier pointers | Do not emit a bound-subscription claim. A separately proven current direct assignment may still produce a non-connectable bare asset. |
| Old release followed by own renewal/reactivation | Include current subscription; no duplicate bare asset. |
| Released/deleted/unassigned inventory | Do not emit as a current asset. |
| Current assigned bare asset with only foreign/terminal/old-delivery subscriptions | Include bare asset with unknown customer expiry; those rows no longer suppress it. |
| Own pending order with unresolved asset binding | Include order summary, hide asset name, no connection eligibility. |

## Exact remaining product decisions / data repair cases

1. **Cross-customer shared entitlement precedence.** Migration
   `2024_01_01_000054_add_shared_fields_to_proxy_ips.php:12-17` and `ProxyIp.php:38-39`
   define mode and counts, not member identities. Actual assignment, cleanup,
   renewal and transfer operations still use single-customer ownership. If
   `access_type='shared'`, subscriptions link A and B to the same current delivery,
   but assignment is null or points to A, should B retain a present entitlement
   after A's assignment/transfer? The read code alone does not settle that
   conflict. Until explicitly defined, counts/flags cannot override assignment
   or resolve a null-assignment conflict. This is not absence of subscription
   ownership fields.
2. **Failed provisioning awaiting refund.**
   `ProvisionSubscriptionService.php:101-137` preserves failed orders/refund
   state; `Customer/Subscription/ProvisionProgressApp.php:71-90` deliberately
   keeps failure history visible. Whether a failed, undelivered order with an
   unresolved refund belongs in *current resources* or only in a financial/order
   history view is not defined by that visibility rule. It is not emitted as
   `owned:true` merely because money is outstanding. Cancelled/refunded history
   and unknown future statuses likewise are not manufactured into current assets.
3. **Malformed legacy identity/conflicting lifecycle data.** Canonical delivery
   snapshots are now checked locally. Noncanonical scalar types, partial objects,
   mismatched instance IDs or duplicated supplier rows need reconciliation;
   these are specific contradictory data cases, not missing-field excuses.
   The reader does not alter them or guess which row should win.

No unresolved product decision is needed for the fixed normal cases above.
Complete account snapshots, monotonic revisions/tombstones and fixed public
egress remain separate contracts. Querying both kinds does not create a global
snapshot; missing pages/404 cannot authorize deleting reminder state.

## Read and test boundaries

- One ordinary SELECT per page/detail; customer checks and delivered DTO share
  that statement's MVCC view. No BEGIN, locking read, DDL/DML, provider request,
  credentials projection, COUNT or OFFSET. Existing deadlines/read-only pool stay.
- Ascending keyset and `LIMIT per_page+1` (maximum 101) bound returned records;
  `LIMIT 1` bounds detail. Correlated identity/anti-join probes require runtime
  plan validation; this is not a bound on physical rows examined by MySQL.
- Existing no-network driver tests reject writes/transactions/locks, inspect
  authorization and lifecycle predicates, and verify errors, pagination,
  account isolation, expiry mapping and unchanged incomplete coverage.
- New isolated-MySQL fixtures exercise null/current/overlap/shared ambiguity,
  snapshots, supplier bindings, renewal/release and bare-asset suppression.
  **They are not executed in this no-DB-write task.** MySQL JSON behavior,
  optimizer plans and runtime MVCC/concurrency remain explicitly unverified.
