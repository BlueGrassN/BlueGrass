# Current ownership coverage audit - 2026-10-12

This audit refines the ownership-blocker wording in the existing server resource
contract. It changes neither resource endpoints nor capability flags. The
implementation remains conservative, so `allOwnedResources` is still false.
That flag must not be described as proof that Laravel lacks ownership data.

Evidence was read only from the existing Laravel source under
`E:/comcode/sunipip-dash/backend`. No application execution, credentials, database
query/write or deployment was involved.

## What the source already establishes

- `app/Services/Customer/Subscription/SubscriptionListApp.php:23` builds the
  customer's subscription query and scopes it by `subscriptions.customer_id`.
  `app/Http/Controllers/Api/V1/Customer/ProxyIpController.php:30` separately scopes
  assets by `proxy_ips.assigned_customer_id`. Both ownership fields exist.
- `app/Services/Svc/Subscription/SubscriptionStatusService.php:13` derives
  expiry from the customer's subscription expiry. The website's 72-hour display
  cutoff in `Customer/ProxyIpController.php:36` is not proof of reclamation.
- `app/Console/Commands/ExpireSubscriptions.php:349` clears asset assignment and
  records release during reclamation. `Api/V1/SubscriptionController.php:2278`
  and line 2290 transfer subscription and asset ownership. The existing client
  query also handles activation/renewal after historical release markers.
- `app/Models/ProxyIp.php:38` includes `access_type`, shared-user counts and
  assignment. Shared-related metadata is not absent from the source.
- `app/Services/Svc/Subscription/IpReclaimGuardService.php:14` defines delivery
  identity using proxy/supplier instance IDs. Lines 26-28 compare the
  subscription's `product_snapshot['upstream_asset']`; lines 30-37 check other
  occupation; lines 40-48 verify supplier instance binding. These fields and
  checks exist. A cleanup guard is not by itself a complete read-only listing
  contract, and its documented caller locking requirements must not be copied
  into a client source SELECT.

## Precise current implementation gaps

The following are limitations of `client-api/internal/source/resources.go`,
not established absence of upstream fields or proof that excluded rows are
unowned:

| Current predicate | Known coverage consequence |
| --- | --- |
| Bound subscription requires `ip.assigned_customer_id = s.customer_id` (line 47). | Missing assignment is excluded. The Laravel guard only rejects an assignment mismatch when assignment is populated; it does not establish that every null assignment is historical/unowned. |
| Any newer retained subscription for the same asset suppresses the earlier row (line 50). | Overlapping/shared entitlement semantics have not been mapped. The client query does not read `access_type` or shared membership metadata. Completeness cannot be inferred from this suppression rule. |
| Bare-asset query excludes an asset when any retained subscription references it (lines 37-38), regardless of that subscription's customer. | A directly assigned asset can be omitted because of a historical/conflicting retained subscription. The related subscription may also fail the stricter assignment check; neither list then proves full coverage. |
| Delivery snapshot and supplier instance binding are not checked by the client resource query. | Those source fields exist, but lifecycle reconciliation using them is not implemented here. Do not describe this as a missing database field. |
| Only the enumerated subscription and asset status sets are returned. | Unknown/future statuses and selected failure/terminal states are deliberately excluded. This read model is not an exhaustive entitlement classifier. |

The precise blocker is therefore an incomplete read-only mapping of existing
business ownership/lifecycle semantics plus unexecuted DB validation. The source
evidence is sufficient to identify those gaps; a broader production read is not
needed for this audit. No claim is made that production currently contains any
of the inconsistent or overlapping cases above.

## Separate limitations that must not be conflated

- The current API has per-statement reads, no cross-page account snapshot,
  monotonic resource revision or tombstone contract. This is an API consistency
  limitation: even a broader listing would not make page absence deletion
  evidence. Generic resource 404 remains ambiguous.
- The isolated MySQL suite, EXPLAIN and runtime concurrency checks were skipped
  in the preceding task. Unit SQL-boundary tests do not close that validation
  gap. This is a verification gap, not evidence of absent ownership data.
- No authoritative fixed external egress mapping was established. Preserve
  `expected_exit_ip: null` as optional metadata without inventing a mapping.
  Under the 2026-10-12 revised contract this does not block connection
  qualification or listing known owned summaries. Actual exit is observed after
  connection; an unsuccessful probe leaves the connection's IP unknown.

Preserve partial coverage and unknown state in consumers. Do not turn
`allOwnedResources` on, delete unseen reminders, or claim full-account reminder
coverage from this audit or the new effective-time getter alone.
