# Android routing boundaries

The JSON contract remains `routingMode: "global" | "bypass_cn"`; omission
preserves legacy global mode. Rules come from the shared
`bluegrass/native/core/routingpolicy` package, using the same default data
provider as v2rayN: `Loyalsoldier/v2ray-rules-dat`. The pinned release is
`202610232206`; only CN/GEOLOCATION-!CN domains and CN IPv4 are bundled.
Source hashes and attribution are in that package's `assets/metadata.json`
and `assets/NOTICE.md`. This bundle is the offline fallback. The Android updater
checks official releases daily using JobScheduler and at foreground entry,
downloads both full source files with their release SHA-256 digests, validates
the selected data, and atomically saves a pair in private no-backup storage.
Failed checks retry after an hour; manual checks bypass that interval.

Each new DNS router captures an immutable snapshot from `routingupdate.Store`.
Existing routers retain their rules and FakeIP bindings until the session ends.
The updater never reconnects the VPN. A verified previous cache or the embedded
bundle remains usable after a failed download or corrupt cache. Status reads do
not perform network I/O. While a VPN is active the updater uses its selected
outbound and selected DNS, permits only official GitHub HTTPS hosts, and cancels
when that session closes; otherwise the VPN-excluded app uses its normal network.

In bypass mode, each validated A candidate gets a distinct FakeIP permanently
bound to the original domain and that candidate IPv4. Dialing a FakeIP prefers
its own candidate, with classification based on the original domain. If DNS
refresh removes that candidate, its old FakeIP fails closed instead of being
retargeted. Bindings are never evicted or reassigned; the process-wide allocator
does not reuse addresses across active or reconnected sessions.

TCP can try remaining validated candidates when a dial returns an error before
handoff. Xray may return a stream before its proxy handshake finishes. Such
asynchronous failures are returned to the application: multiple DNS answers let
the application try the next FakeIP/candidate. There is no general asynchronous
retry on a single connection, and application payload is never replayed. UDP
creation tries only the preferred candidate; it cannot prove remote reachability.

The in-memory positive DNS cache uses the minimum relevant A/CNAME TTL, capped
at 300 seconds. Zero TTL and negative answers are not cached. Concurrent cold
resolutions share a session-owned operation; one caller's cancellation does not
cancel other waiters. A successful new DNS answer satisfies existing waiters
and retires obsolete I/O, whose late result cannot replace the new answer.
Closing the session clears bindings/cache and cancels outstanding operations.
No DNS data is written to disk.

Limits are 8,192 domain/candidate bindings and 8,192 reserved/cache names per
session, 256 validated candidates per answer, and 131,070 FakeIP allocations
over the process lifetime. A multi-candidate answer reserves its bindings
atomically. Exhaustion rejects the answer; it does not truncate or reuse it.

The HTTP CONNECT regression uses the actual Xray adapter and a loopback proxy:
the first candidate returns 502 and the application's second DNS candidate
returns 200 and transfers data. This is local adapter validation, not Android
device acceptance.
