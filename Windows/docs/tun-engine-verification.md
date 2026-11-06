# Windows TUN engine verification

Date: 2026-10-12 (Asia/Hong_Kong).

Scope: `core/tunengine/`, the subsequently authorized
`exitprobe/`, `localengine/`, `protocolconfig/`, shared `engine.Profile` and
`control/` integration, and pinned module/sum consolidation, plus this report.
Existing dirty work, localengine entrydns integration, service/WFP/bridge and GUI
code were preserved. The concurrently assigned `tunengine/localonly*.go` work
belongs to Tesla and is outside this report's implementation scope. No
commit, subordinate agent, real TUN startup, firewall mutation, system proxy
change, adapter removal, elevation or connection to port 10808 was performed.
This is implementation and bounded verification evidence, not Beta release or
zero-leakage certification.

## Integration contract

```go
type Config struct {
    AllowLAN     *bool // nil defaults to true; false is explicit
    UIExecutable string // trusted absolute installation path, NEVER IPC input
    Network      func(context.Context) (NetworkSnapshot, error)
    CachePath    string // trusted absolute service-owned .db; empty means volatile
    AllowUDPRelay func(netip.AddrPort) error // trusted, session-bound WFP callback; nil denies UDP
}

func New(Config) (*Tun, error)
func SystemNetwork(context.Context) (NetworkSnapshot, error)
func RepairAdapter() error
func ClearCache(trustedPath string) error // explicit logical disconnect, after closing engines
```

`Tun` implements `engine.Engine` plus:

- `Address() string`: empty; no system-proxy or general-purpose probe listener.
- `VerifiedIPv4() string`: empty unless the current verified gate is open.
- `Counters() (uint64, uint64)`: application upload/download, including admitted
  LAN traffic; controlled probe sockets are excluded. Counters start at zero on
  Prepare; the controller owns logical-session aggregation across reconnects.
- `ResolvedEntry() netip.AddrPort`: this attempt's single literal IPv4 endpoint.
- `InterfaceLUID() uint64`: zero until verified TUN activation finishes.
- `VirtualPrefixes() []netip.Prefix`: TUN v4/v6 and the entire reserved
  `198.18.0.0/15` FakeIP candidate space, available immediately after Prepare.
  Including both candidate pools prevents stale FakeIP LAN exemptions.
- `LocalDNSEndpoints() []netip.AddrPort`: controlled local-name resolvers only.
- `NetworkChanged() error`: invalidate the proof and stop this engine.
- `Close() error`: terminal; subsequent Prepare fails.

Required host order:

1. Authenticate the owner and check profile/expiry. Arm constrained bootstrap
   protection with `Manager.ArmDNSWithPolicy(...)` (or its legacy ArmDNS adapter)
   before resolving the entry through the service's explicit numeric DNS policy.
   Put the chosen literal IPv4 in
   `Profile.Entry.Host`; this engine never implicitly uses the system resolver.
2. `Manager.Arm(ownerSID, accountKey, resourceID, entry, dns, allowLAN)` while
   ordinary traffic is blocked. Bootstrap and local resolver lists are distinct:
   `SystemNetwork.EntryDNS` contains the default network's configured DNS
   candidates; `SystemNetwork.DNS` contains only approved local-name resolvers.
3. `New(Config{AllowLAN: &allow, UIExecutable: installedGUIPath,
   Network: SystemNetwork, CachePath: installedCachePath,
   AllowUDPRelay: allowRelayForThisSession})`, then `Prepare(sessionCtx, p)`.
   **sessionCtx is the connection attempt/session lifetime, not an IPC request.**
4. Apply `Manager.SetVirtualPrefixes(tun.VirtualPrefixes())` before permitting
   traffic; include stale-pool protection described above.
5. `ProbeExit(sessionCtx)`, check exact expected IPv4 and current eligibility.
6. `SetBusinessGate(true)` creates Wintun, stack and routes with the internal
   gate still closed. After successful initialization and a second network
   generation check, it opens the internal gate and returns a nonzero LUID.
7. `Manager.AllowTunnel(tun.InterfaceLUID())` is the final host permission. Any
   failure keeps/reinstates `Manager.Block()` and closes the engine.

For pause, expiry, network change, replacement or failure: **WFP Block first,
then SetBusinessGate(false)/Stop/Close**. The engine closes tracked TCP/UDP,
cancels DNS/probes, destroys its router/TUN and never releases WFP. Cleanup
errors remain sticky: an asynchronous cleanup error cannot become a later
successful Stop. Recreate only after the host has resolved the cleanup failure.
Only an explicit host recovery/disconnect transaction may release protection.

Runtime Stop/Close, reconnect and Prepare replacement preserve the FakeIP cache.
An explicit logical disconnect, logout or protection release must additionally
call `tunengine.ClearCache(installedCachePath)` after closing the runtime and
check the result. Do not clear it during ordinary Block/reconnect. ClearCache
acquires the same exclusive database ownership and rejects a still-open cache.
This is a separate API because the existing Engine.Stop/Close boundary also
serves protection pause and runtime replacement, which must retain mappings.

`allowRelayForThisSession` must verify that its captured attempt/session is still
the host's active verified session, then call `Manager.AllowUDPRelay(endpoint)`.
Serialize that check and authorization with Block/session replacement. A bare
method value on a manager reused across attempts does not establish this identity
boundary: an old callback could otherwise run after a new Arm/AllowTunnel. Do not
call engine Stop/SetBusinessGate while holding a callback lock that the stopping
runtime may wait on. The engine holds no engine/gate lock while invoking the
callback. WFP permissions must have bounded session lifetime and be cleared on
Block; this callback is intentionally not a general service UDP permission.

`engine.Profile.ProtocolConfig *protocolconfig.Config` now carries the optional
closed version-1 typed protocol contract. Normal Prepare consumes it; absent
configuration supports only legacy SOCKS5 Auth. All types require a literal public
IPv4 entry in TUN and an exact public IPv4 expected exit. LAN policy changes
require a new engine/Prepare/proof. The
default GUI constant is `C:\Program Files\BlueGrass\bluegrass.exe`; installers
using any other path/name must provide the trusted `UIExecutable` override. It
creates a TCP/443 IPv4 control exception for that GUI and its exact installed
sibling `BlueGrass.Update.exe`, never a service-wide direct rule.

Simplification: removed the redundant `Config.NeverResolve` flag. It never
changed behavior. Entry parsing still unconditionally requires a literal public
IPv4 before network snapshot/runtime creation; hostnames, private/loopback,
FakeIP, unspecified, mapped IPv6 and IPv6 entries are rejected. Tests verify both
the early rejection and successful literal-entry Prepare with its gate closed.

## D-03 protocol contract and controller support

The accepted minimal `protocolconfig.Config` version 1 is implemented in native
code, not just a capabilities declaration. See
[`protocolconfig/README.md`](../native/core/protocolconfig/README.md) for the exact
closed schema, enum/credential/TLS bounds and source-level UDP matrix.

| Protocol | Implemented native combination | Physical entry transport |
| --- | --- | --- |
| SOCKS5 | version 5, paired RFC1929 auth or explicit no-auth TCP; guarded TUN UDP relay | TCP; authenticated relay separately authorized |
| Shadowsocks | 3 AEAD and 3 SS2022 methods; single-key SS2022; no plugin | TCP + UDP, exact entry |
| VMess | AEAD AES128-GCM / ChaCha20-Poly1305, alterId 0, TCP, optional TLS | TCP |
| VLESS | TCP + verified TLS; empty flow or Vision; XUDP | TCP |
| Trojan | TCP + verified TLS | TCP |
| HTTP / HTTPS | HTTP CONNECT; HTTPS verified TLS with HTTP/1.1 ALPN | TCP; no UDP capability |
| Hysteria2 | verified TLS/QUIC, fixed peer, optional Salamander; no realm/hopping | UDP |
| TUIC | verified TLS/QUIC, native/quic relay, cubic/new_reno/bbr, no 0-RTT | UDP |
| AnyTLS | verified TLS and bounded native session-pool lifecycle; UoT | TCP |

`engine.Profile` now has `ProtocolConfig *protocolconfig.Config` with wire key
`protocolConfig`. `Profile.EffectiveProtocol()` requires exact protocol matching
and validation. With no explicit config only legacy SOCKS5 Auth is eligible;
other protocols cannot be synthesized from its fields. Explicit config is the
sole protocol parameter source. `EffectiveProtocol` centrally rejects nonempty
legacy Auth for every typed non-SOCKS resource; typed SOCKS permits empty legacy
Auth or an exact username/password copy only. This applies to nativehost, direct
controller calls and both Prepare entry points, so an IPC-only duplicate guard
is unnecessary. Typed API/Dart producers should emit the existing required
`auth` object with empty values.

Controller DecodeRequest accepts only the exact optional field and rejects null,
unknown/duplicate nested keys and unsupported TLS settings. ValidateProfile
enforces typed eligibility before engine calls. Apply detaches the entire typed
object before asynchronous Prepare and includes it in the replay digest; changed
credentials under a retained request ID are a conflict. Controller dispatch stays
on `Engine.Prepare`, preserving service wrappers; both native engines now consume
the explicit Profile field there. `PrepareProtocol` remains available to typed
native callers and delegates to the same Profile validation and Prepare path.
All protocols use the existing exact exit/expiry gate.

Tests exercise raw JSON -> strict controller decoder -> asynchronous Controller ->
real Local Prepare -> actual pinned outbound constructors for all ten types.
Only external exit evidence is replaced. The TUN controller matrix exercises
actual typed option construction and gate sequencing with a mocked OS runtime;
a separate matrix constructs each real full Box without starting a native TUN.
Cipher/security/congestion variants are constructed against sing-box 1.14.2.
These are native/controller implementation checks, **not remote handshake or UDP
acceptance**. Production resource delivery requires provider data supplying the
typed contract; Dart/API mapping is owned separately and not established by these
native tests.

All outbounds have one validated numeric public IPv4 entry, a 12-second dial
timeout and no arbitrary route/detour. Explicit original TLS serverName survives
entry resolution; insecure/SNI-disable/custom certificate paths are unavailable.
TUIC's optional ALPN defaults to `h3` because the pinned TUIC client passes TLS
unchanged and quic-go requires NextProtos; explicit ALPN is preserved. HTTPS uses
only `http/1.1`; Hysteria2's pinned client supplies `h3` if omitted.

`protocolconfig.Requirements` returns exact EntryTCP/EntryUDP needs and a
UDPTransport category for the service's WFP policy. It never authorizes WFP
itself. TUN registers the real protocol constructors, then overrides SOCKS with
the existing guarded UDP-relay implementation. The full router's DNS-before-LAN,
public IPv6 rejection and proxied public DNS policy remain in force.

Local now owns native outbound clients as well as application sockets. Block,
Stop, replacement, reliable mismatch and Close retire those clients exactly once,
including QUIC/AnyTLS pools; cleanup errors remain sticky. Close is terminal.
Tests first reproduced the previous ability to Prepare after Close, then verified
its rejection. AnyTLS's upstream sleeping idle worker can exit up to 15 seconds
after Close, while client cancellation and tracked socket closure are immediate.

Ablation/simplification: the existing Controller -> Engine.Prepare dispatch and
shared Profile helper serve every protocol. No parallel controller or raw kernel
configuration path was added. The duplicate local credential-to-outbound builder
and redundant retirement assignments were replaced by the same typed builder and
retirement owner used for all protocols.

The final credential regression first reproduced acceptance of conflicting
legacy Auth in EffectiveProtocol and direct Controller.Apply. It now verifies
typed SOCKS empty/identical acceptance, username/password/partial conflicts,
typed non-SOCKS rejection even when duplicate values match, and rejection before
any controller engine call. Both Local/TUN Prepare and PrepareProtocol retire the
old session on this rejection. The raw JSON all-protocol Local matrix now uses
empty legacy Auth, consistent with the wire contract. RED and GREEN evidence is
saved alongside this report; no credential values are included in failure text.

## Updater direct-route correction

The GUI's trusted installed path is the sole source for deriving its exact sibling
`BlueGrass.Update.exe`. Both identities receive only IPv4 TCP destination port
443 through `control`; service images, a different directory, suffix lookalikes,
UDP and other ports are excluded. DNS interception and virtual-prefix rejection
still precede this rule. Real pinned route matchers cover a relocated install,
both allowed images and all those negative cases.

`cmdupdate`/`update` compiled hostname allowlists, strict TLS and redirect handling
were not changed. The service independently verifies the helper identity and
WFP permission. Neither a Profile-supplied executable bypass nor a broad service
process bypass is introduced. Tests establish config/helper behavior only; signed
installed updater routing still requires live acceptance.

## Optional in-engine Diagnose

Both Local and Tun implement
`Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)`.
They capture the current native session/generation and verified selected outbound,
run the existing fixed HTTPS probe under a 20-second/session-cancelled deadline
without holding the engine mutex, then recheck identity. No endpoint, DNS, direct
dial, certificate override or UDP test target comes from this binding.

The shared `exitprobe.Diagnostic` adapter supplies the standard report with
unmeasured areas skipped; it does not duplicate HTTPS transport/parser code.
UDP remains `skipped` / `probe_not_configured`. A reliable mismatch returns
`proxy_exit`, `failed`, `exit_mismatch`, and the validated observed IP; only the
still-current captured session is retired. The wrapper can then Block+Close with
all engine locks released. A successful observation does not extend/regrant proof.

Cancelled/stale work returns a cancelled report with observations removed and
`STALE_DIAGNOSTIC`; actual TUN network-generation failure returns `NETWORK_CHANGED`.
A regression test first reproduced diagnostic cancellation being mistaken for
network change at the initial snapshot check; it now preserves a healthy verified
session. Tests also cover a late old mismatch after replacement, selected-only
dial targets/deadlines, active mismatch shutdown and skipped UDP. No diagnostic
result claims physical leakage, IPv6 or host WFP acceptance.

## Shared HTTPS exit probe and simplification

Both engines now call **one** production `exitprobe.Probe(ctx, selectedDial,
Options)` implementation. It owns the HTTP/TLS transport, fixed endpoint list,
strict parsers, total/attempt deadlines and fallback. The old localengine and
tunengine `parseExit`, `probeHTTP` and endpoint constants were removed. Parser
regressions moved into the shared package; each engine retains its own session
and gate tests. The helper has no expected-IP input, arbitrary endpoint option,
root-certificate override or direct dial fallback.

The user-authorized fixed third-party order is:

| Attempt | HTTPS endpoint | Response |
| --- | --- | --- |
| 1 | `https://api.ipify.org/?format=json` | strict JSON `ip` string |
| 2 | `https://icanhazip.com/` | one plain IP, edge whitespace allowed |
| 3 | `https://checkip.amazonaws.com/` | one plain IP, edge whitespace allowed |

Only network/TLS/HTTP failures and malformed or invalid responses may proceed
to the next endpoint. **The first valid public observation returns immediately**,
including a mismatched IPv4 or valid global IPv6. No later endpoint can replace
that evidence with a favorable expected IP. Both engines immediately retire the
current proof and active business session on reliable mismatch; the TUN runtime
is also stopped. A transient failed diagnostic preserves an already verified
session under addendum 11. Connection attempts still begin with no proof and a
closed gate.

There is one 20-second overall deadline and at most 7 seconds per attempt; the
third attempt inherits only the remaining overall budget. Caller/session
cancellation ends fallback. Each attempt owns a separate non-reusing transport,
with its selected dial explicitly bound to that attempt's context. This matters
because Go's `net/http` ordinarily strips request cancellation/deadlines from
the dial context to permit reuse. The explicit binding includes the adapter's
proxied DNS and SOCKS connect work in the same budget. No previous attempt's
connection or pending dial can supply a later proof.

TLS uses system trust, hostname/SNI verification and TLS 1.2 or later. Redirects
are never followed, environment proxies are ignored, and all connections use
the supplied selected dial. Each response must be HTTP 200 with at most 4096
body bytes (8192 header bytes). JSON rejects duplicate/unknown fields, non-string
values, multiple objects, private/reserved IPv4 and local/mapped IPv6. Plain bodies must
contain exactly one valid address. Public IPv6 is returned as evidence to the
engine's existing `ProbeResult.IPv4` field; it cannot pass the IPv4 gate. No
credential, business token, cookie or raw server/dial error is emitted.

The only parsing compatibility switch is `Options.AllowCountry`: TUN enables
the existing optional string `country` field, which is ignored. Localengine
retains its existing ip-only JSON contract. Neither country nor endpoint order
can influence the expected-IP comparison.

TUN's remaining adapter checks the shared exact hostname/TCP/443 allowlist
**before** DNS. All three names resolve through its existing `public` TCP DNS
transport over the selected proxy, with IPv4-only answers and cache disabled;
only public literal IPv4 answers may reach the selected outbound. Private,
FakeIP, IPv6 and mapped IPv6 answers are rejected. DNS failure never falls back
to the system resolver or domain dialing. Localengine preserves its existing
selected outbound domain-dial path and entrydns integration; this does not force
the proxy's remote target resolution to IPv4. Its real upstream IPv4 endpoint
selection remains an acceptance limitation, and any observed global IPv6
still immediately revokes proof rather than triggering another endpoint.

Localengine additionally clears proof on every Block/pause, withdraws the old
controlled-probe outbound at the beginning of Prepare (including invalid
replacement profiles), and rejects stale Prepare completion after Stop or a
new attempt. Probe completion checks generation and cancellation under the
same mutex that retires proof/connections; an old mismatch cannot close a new
session. Entry DNS still uses the main owner's policy, native network candidates
and `entrydns.Resolve`, now canceled when its Prepare is retired.

Verification uses actual HTTP and strict TLS over `net.Pipe` with a test-only
root, plus the real pinned Box constructed without Start. It covers ordered
fallback, first-valid mismatch/IPv6, duplicate/null/oversized responses,
redirect/TLS rejection, no environment proxy, bounded cancellation, selected
DNS/connect deadlines, allowlisting before DNS, IPv4 answer filtering, and both
engines' stale-proof/teardown behavior. No public endpoint was contacted. The
shared implementation and removal of duplicate transport/parser code are the
ablation result; tests retain the same safety contracts in one place.

The independently deployed first-party probe service, its finalized response
contract and trusted remote endpoint governance are **not deployed or claimed
complete** by this change. These fixed third-party fallbacks are the concrete
interim implementation authorized for this task.

## Runtime and policy

- Uses the real sing-box 1.14.2 Box/router/DNS/TUN lifecycle and cached sing-tun
  `v0.9.6-0.20260924001923-ddaa4ca25e3b`; Wintun is embedded by that dependency.
- Build with **`-tags with_gvisor`** on Windows. A build without the tag compiles
  for tooling/tests but refuses native interface creation. There is no silent
  fallback to system/mixed stacks.
- Prepare starts the router and selected outbound with a delayed TUN inbound.
  A guarded router rejects business traffic until verification/activation.
- Config enables `auto_route`, `strict_route`, IPv4/IPv6 capture and physical
  `auto_detect_interface`. The platform adapter clears ONLY native
  `tun.Options.StrictRoute` at `tun.New`: upstream Windows strict_route creates
  its own dynamic WFP filters. The host manager must implement equivalent strict
  DNS/route protection. gVisor avoids the upstream system stack's separate
  automatic Windows Firewall TCP exception. This package contains no WFP calls.
- DNS TCP/UDP 53 interception precedes LAN rules, including DNS addressed to a
  private router. Public A uses IPv4 FakeIP; actual resolution and other public
  query types use TCP DNS to `1.1.1.1:53` through the selected outbound.
  Public AAAA returns NOERROR/NODATA and public IPv6 routing is explicitly
  rejected. There is no direct public DNS fallback or proxy fail-open.
- Local names, including case/root-dot normalization and label boundaries, use
  the explicitly mapped local resolver. Queries of every type follow the same
  classification. Local reverse zones are prefix-bounded; unavailable private
  reverse lookups fail instead of reaching public DNS. localhost is answered
  locally; .local/link-local reverse queries use mDNS on one explicit link.
  Missing local resolvers/LAN-off produce SERVFAIL, with no public fallback.
- Actual LAN routes are bound per interface, longest prefix then route metric.
  Scope-less ambiguous link-local/multicast destinations are withheld instead
  of choosing an interface by metric. Link-local IPv6 direct sockets carry the
  selected interface zone. No route means reject, never remote fallback.
  No blanket `ip_is_private`, `::ffff:0:0/96`, global multicast or arbitrary
  public static-route exemption is generated. Loopback stays locally bound.
- Virtual pools are chosen from bounded candidates after occupied-route checks.
  Sing-box restores current FakeIP mappings before route matching; unmapped
  current FakeIP is rejected by the dependency and the entire old candidate
  IPv4 pool is rejected before LAN routing.
- HTTPS probing calls the selected outbound directly. It resolves the fixed
  endpoint through proxied TCP DNS and dials a literal IPv4 while retaining the
  HTTPS hostname/SNI and normal certificate verification. No environment proxy,
  redirects, business token or subscription credentials enter the HTTP request.
  Response size/schema/duplicates/trailing data are checked. Reliable different
  IPv4 or IPv6 evidence revokes an already-open session; transient diagnostic
  failure does not invent a new proof or silently change the exit.
- Logs are disabled in the embedded box. Public errors use bounded codes, not
  raw dependency errors containing addresses, paths or protocol credentials.

## Persistent FakeIP mapping lifecycle

Accepted addendum section 7.2 requires mappings to survive kernel restart and be
cleared on logical disconnect. ADR0031 requires old/pool-conflicting FakeIPs to
remain virtual, and ADR0032 isolates local DNS by network/resolver/policy. With
nonempty trusted `Config.CachePath`, the registered FakeIP transport now uses a
small replacement `adapter.FakeIPStore` backed by the already-pinned
`github.com/sagernet/bbolt`. It remains a sing-box DNS transport and router
FakeIP store; no standalone resolver or alternate routing stack was added.

Why not just enable sing-box `store_fakeip`: the pinned Store.Create logs and
ignores persistence errors, returns the issued address anyway, and saves its
allocation metadata separately/asynchronously. Its cachefile FakeIP buckets are
also global rather than isolated by the host's current policy. This package
instead synchronously commits forward mapping, reverse mapping and allocation
cursor in a single transaction **before returning the address**. Failure returns
`FAKEIP_CACHE_UNAVAILABLE` without an address; backend paths/domains/errors are
not logged. No credential, exit proof or connection eligibility is persisted.

Configuration and protection:

- CachePath is an absolute `.db` file path provided by service configuration,
  normally under its installed ProgramData directory. It is not part of the
  Profile or IPC schema. Empty explicitly retains the previous volatile mode;
  the production service must supply its path to enable this feature.
- The parent directory must already exist, be owned by SYSTEM or Administrators,
  and have a protected DACL granting full access only to those two principals.
  The engine does not create directories, elevate, or repair permissions. New
  DB files receive that same protected owner/DACL policy. Existing files must
  pass the same checks; Windows `0600` alone is not relied upon.
- Every ancestor is opened without delete sharing and checked for reparse
  points, pinning the path during use. The database is exclusively opened;
  reparse points, hardlinks, directories in place of a file, untrusted owner/ACL,
  relative/UNC/device paths, alternate streams and oversized files fail closed.
- Nonempty cache configuration also requires all resource namespace fields:
  Environment, DataSource, AccountKey, Kind and SourceID. A SHA-256 namespace
  covers these plus protocol, expected exit, network snapshot/generation,
  DNS/LAN policy and selected FakeIP pool. Raw account/resource identifiers are
  not stored. DNS names are protected file contents, not logs.

Restore, invalidation and bounds:

- Re-Prepare and engine/service reconstruction with the same namespace restore
  both lookup directions and the allocation cursor. The business gate remains
  closed and a fresh exact exit proof is mandatory. The cache cannot restore a
  credential, session, WFP permission or prior exit verification.
- Network, resolver, LAN/local-name policy, account/resource/environment or pool
  changes remove old mappings atomically. Only one namespace is retained. The
  two candidate pools keep allocation high-water marks across such resets, so a
  stale application address cannot immediately name another domain. Returning
  to an old network/pool does not resurrect its old map. Existing full-candidate
  `198.18.0.0/15` rejection still precedes LAN routing.
- At most 16,384 active mappings and a 32 MiB database; no eviction/address wrap.
  Each `/16` cursor may advance through `.0.2` to `.255.254`; exhausted capacity,
  exhausted cursor or storage failure refuses new allocations rather than
  reassigning issued addresses. Existing valid mappings remain available.
- On reopen, more than 24 hours since the last successful Start/Create/Close,
  or a timestamp more than five minutes in the future, invalidates the map while
  retaining cursors. This bounds **reuse of an idle cache**, not wall-clock file
  deletion while the service is stopped. An active runtime does not expire live
  mappings mid-connection. Explicit ClearCache ends the logical lifecycle and
  resets mappings/cursors transactionally. Deleted DB pages are reusable storage,
  not a forensic secure-erasure guarantee.
- Local names and configured local routing domains never enter the map; public
  IPv6 FakeIP creation is rejected. Ordinary DNS answers/local-resolver results
  are not persisted. Old closed/canceled stores reject late Create/Reset calls.
- A malformed cache/schema, inconsistent reverse map, or cursor behind an issued
  address fails startup. No silent volatile fallback is used when CachePath was
  supplied. Clear errors must be surfaced by the host; corrupt caches are not
  automatically deleted or permissions changed.

Tests use a private test-only opener for real bbolt files in temporary user
directories. They verify reopen and process exit without Close, engine
Prepare/recreation without proof reuse, namespace/pool transitions, retention,
capacity/exhaustion, corruption/write failure, concurrent allocation, late-store
isolation, exclusive-clear rejection and real sing-box transport registration.
Pure Windows SDDL tests cover owner/protected DACL/allow-list requirements; a
read-only negative test confirms the production opener rejects an ordinary user
directory without creating its DB. There is no production ACL-bypass switch.
The successful SYSTEM-owned ProgramData path and power-loss hardware durability
still require service/elevated acceptance; the subprocess test demonstrates
process-exit durability, not a physical power-cut test.

## Authenticated SOCKS5 UDP relay authorization

The selected SOCKS outbound is registered by this package. It retains the pinned
`dialer.New` physical-interface behavior and wraps it with a per-association
guarded dialer passed to the real `sing/protocol/socks.NewClient`. In the pinned
`sing` version `v0.9.6-0.20260922013354-87c33f17688f`, `client.go:162` calls
`DialContext("udp", response.Bind)` only after the SOCKS5 handshake succeeds.
The guard intercepts that exact call before the underlying UDP socket is created.

Order and restrictions:

1. The current Prepare/session must be alive and its verified business gate open.
   TCP probe/public TCP DNS remains available with a closed business gate; UDP
   diagnostic or business associations cannot use that exception.
2. The physical TCP handshake connects only to this Prepare's literal IPv4
   entry/port. UDP requires nonempty RFC1929 username/password, each at most 255
   bytes. Missing callback or credentials denies UDP before any handshake.
3. The incoming handshake headers must select username/password authentication,
   report authentication success and UDP ASSOCIATE success, and carry IPv4 wire
   address type. This closes two pinned-client gaps: accepting an unoffered
   no-auth downgrade, and unwrapping IPv4-mapped IPv6 before address validation.
4. Updated by the 2026-10-20 [elevated acceptance](elevated-tun-acceptance.md):
   the authenticated BND.ADDR may differ from the TCP entry, but must be a public
   IPv4 with a nonzero BND.PORT. Domains, IPv6, mapped IPv6, private/virtual/CGNAT
   and unspecified addresses fail closed. Every exact endpoint is allowed only
   through a fresh callback for that association. It is never
   inferred from the logical packet destination or TCP entry port.
5. `Config.AllowUDPRelay(actualBind)` runs synchronously. Its failure is sanitized
   to `UDP_RELAY_DENIED` and creates no UDP socket. The session, gate and caller
   context are checked again afterward. A Block/cancellation during the callback
   prevents the subsequent physical dial. A socket whose dial was already in
   flight at retirement is closed when it returns and is never handed to a caller.
6. Gate retirement closes both active UDP sockets and their TCP associations.
   Session cancellation also cancels pending physical dials and closes sockets.
   Every association obtains its own handshake evidence; no relay authorization
   is cached by this package. The host owns exact WFP rule expiry and revocation.

No all-UDP process exemption, UDP-over-TCP fallback, implicit relay DNS lookup or
alternate-IP compatibility fallback was introduced. Servers advertising a relay
on a different IP/domain, anonymous UDP servers and malformed replies are
explicitly unsupported. The host callback must finish promptly; its signature has
no context or rollback operation. If authorization succeeds but socket creation
later fails, its exact permission remains subject to the host's bounded lifetime
and clear-on-Block policy.

Verification uses `net.Pipe`, the actual pinned SOCKS5 client and a wire-level
fixture server. Only physical dial creation is mocked; there are no listening
sockets or OS UDP packets in these tests. Tests cover exact callback-before-dial
ordering, both DialContext/ListenPacket, returned-port changes, changed peer,
authentication refusal/downgrade, mapped IPv6 wire replies, nil callback,
closed/retired/canceled sessions, callback error sanitization, concurrent Block,
late socket teardown, and repeated associations without reused authorization.
The constructed real Box selects this guarded adapter; its Config callback is
exercised through the real outbound registry with the final physical dialer
replaced by the same fixture. Box Start and native TUN creation are never called.

## Read-only Windows network discovery

`SystemNetwork` uses `GetAdaptersAddresses` and `GetIpForwardTable2`, and frees
the OS table with `FreeMibTable`. It does not run PowerShell, WMI or resolver
queries and never writes routes or adapter settings.

The fingerprint includes actual adapter indices/LUIDs, state, unicast
prefixes, interface metrics, configured DNS, destination prefixes, next hops
and route metrics. Route ages/lifetimes are excluded because they tick without
a topology change. The exact BlueGrass interface and all its routes are
excluded. Two consecutive matching snapshots are required; unstable snapshots
fail closed. Real default route, NIC metric and resolver changes invalidate the
generation. Host interface/route/DNS-policy monitoring must call Block and
NetworkChanged promptly; checking the generation at probe/activation alone is
not a continuous network-change monitor.

Automatic local DNS chooses the unambiguous default IPv4 link and the first
locally reachable private/local resolver for `lan` and `home.arpa`. Public
resolvers stay in EntryDNS only. Generic search suffixes are not automatically
trusted routing domains. Explicit enterprise mappings can be supplied by a
trusted Network provider. Raw default/global routes are not LAN exemptions.

Live read-only smoke on this workstation: stable nonzero generation across two
calls, 9 unambiguous local routes, 1 approved local resolver, 20 occupied prefixes.
An earlier snapshot exposed 19 routes before ambiguous multi-interface scoped
routes were withheld; the final smoke uses the stricter filtering. No
native engine Start/EnableTunnel or RepairAdapter was called in this smoke.

## Independent adapter repair

`RepairAdapter` enumerates the network device class with SetupAPI and checks:

1. Driver registry `NetCfgInstanceId` equals
   `{a72ced18-5879-4bd0-6e9c-344e1e99d94f}`. This reproduces the pinned
   `tun_windows.go:631` MD5(`wintun` + `BlueGrass`) native GUID byte layout.
2. The corresponding network `Connection\Name` equals `BlueGrass`.
3. PnP service and hardware ID both identify Wintun.

It rejects incomplete/mismatching/duplicate identities, revalidates the exact
device instance before `DIF_REMOVE`, then enumerates again to confirm absence.
It never calls WintunDeleteDriver or removes a shared driver package. No matching
device is an idempotent success. Failure is `*AdapterRepairError` with a bounded
Code; non-Windows returns `ADAPTER_REPAIR_UNSUPPORTED`. The caller owns service
shutdown, elevation, WFP repair and current-user proxy restoration. The actual
SetupAPI removal path and reboot-required behavior remain untested.

## Latest verification and dependency consolidation

Verified from `core` with Go `go1.27.2 windows/amd64`, tag
`with_gvisor`, and MSYS2 UCRT64 GCC 16.2.0 for race. The official go.mod/go.sum
are used directly with `-mod=readonly`; no temporary modfile is required.

Required imports were consolidated into the existing pinned module graph: 22
additional indirect requirements and the verified checksum union. No existing
version was upgraded. Roots remain sing-box `v1.14.2`, sing
`v0.9.6-0.20260922013354-87c33f17688f`, and sing-tun
`v0.9.6-0.20260924001923-ddaa4ca25e3b`. Native protocol additions include
sing-anytls `v0.0.11`, sing-quic `v0.7.1-0.20260924092235-6a3a24d65b99`,
quic-go `v0.61.0-sing-box-mod.7`, sing-shadowsocks `v0.2.8`, and
sing-shadowsocks2 `v0.2.1`, as selected by the already pinned graph.
`go mod verify` returned `all modules verified`.

Module file SHA-256 at this checkpoint:

```text
go.mod 8F89033DE49EBED5AEFF38271C8FD3FC660C0A115B8F6D3085FE409349A7E7BC
go.sum 3B367848C360913A90D7712F6AC56F0C1333D6E7C22215BE8D505816A36FC7AA
```

The latest complete unit suite, complete race suite, vet and build passed:

```powershell
go test -mod=readonly -tags with_gvisor -count=1 ./...
$env:PATH = 'C:\msys64\ucrt64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
go test -mod=readonly -tags with_gvisor -race -count=1 ./...
go vet -mod=readonly -tags with_gvisor ./...
go build -mod=readonly -tags with_gvisor ./...
```

Outputs are `tun-engine-unit-results.txt`, `tun-engine-race-results.txt`,
`tun-engine-vet-results.txt`, and `tun-engine-build-results.txt`.
All protocol constructors, Controller/Local/TUN
matrices, diagnostic and updater tests passed; the complete core unit result
also includes the concurrently implemented service, journal and local-only work,
without attributing that implementation to this task.

During race verification, the cancellation callback exposed unguarded test-only
reads of `Tun.runtime` in diagnostic_test.go and protocol_test.go. Production
writers already hold `Tun.mu`; those test assertions now take the same mutex.
The related diagnostic, stale-session, cancelled-session and protocol-Prepare
regressions passed 30 repeated race runs (`-count=30`); the result is recorded in
`tun-engine-targeted-race-results.txt`. The subsequent full-core race command
passed without exclusions, including the separate local-only/service packages.
The earlier service journal snapshot and its
successful isolated retest are retained in
`tun-engine-intermediate-race-results.txt` and
`tun-engine-service-race-results.txt`.

Concurrent intermediate snapshots also included the independently assigned
local-only DNS NXDOMAIN/Reset/cancellation regressions and local journal tests
being introduced before implementation. Those files were not edited here. A
previous owned-package race run with `-skip '^TestLocalOnly'` passed and is saved
in `tun-engine-owned-race-results.txt`; it is explicitly not a full-suite result.

The new central Auth regression's failing-before-fix evidence is
`tun-engine-auth-red-results.txt`; its first passing Engine/Controller/Local/TUN/
nativehost run is `tun-engine-auth-green-results.txt`. Additional behavioral RED
cases in this stage included terminal Local.Close, TUIC's omitted required ALPN,
and cancelled diagnostic work accidentally retiring a healthy verified session.
These are bounded unit/source checks. No command started a real TUN, modified
WFP/routes/system proxy, elevated, or contacted a production proxy.

At the final source check, nativehost had removed its duplicate legacy-auth
branch and used the shared `EffectiveProtocol` guard. The complete passing race
run includes that simplification. Nativehost was edited by its owner, not here.

Files for this protocol/updater/diagnostic integration stage:

- `protocolconfig/{config,options,native}.go`, `config_test.go`, `native_test.go`,
  `testdata/valid-v1.json`, and `README.md`.
- `engine/engine.go`, `engine/protocol_test.go`, `control/controller.go`,
  `control/decode.go`, and `control/protocol_test.go`.
- `localengine/{engine,diagnostic}.go` plus `protocol_test.go`,
  `controller_test.go`, and `diagnostic_test.go`; entrydns integration retained.
- `tunengine/{config,runtime,engine,diagnostic}.go` plus `policy_test.go`,
  `protocol_test.go`, `controller_test.go`, and `diagnostic_test.go`.
- `exitprobe/diagnostic.go` and `diagnostic_test.go`; the previously consolidated
  shared probe remains the sole HTTPS transport implementation.
- `go.mod`, `go.sum`, this report and its command-output text files.

No `tunengine/localonly*.go`, entrydns, winservice, winprotect, nativehost, Dart,
or API source was edited by this task. Full-workspace results cover those owners'
current files only as an integration checkpoint.

## Earlier-stage verification and dependencies

Go `go1.27.2 windows/amd64`; race compiler MSYS2 UCRT64 GCC 16.2.0.
Tests use pure configuration/native-layout inputs, in-memory connections and
mock lifecycle/repair backends. The pinned Box is constructed and closed but
never started in its integration test. Actual route and DNS matchers from the
pinned dependency evaluate emitted policy fixtures.

TDD failure evidence includes unimplemented API, incorrect network generation,
private PTR leaking to public DNS, an old probe stopping a replacement session,
IPv6 mismatch preserving proof, ambiguous link-local metric selection, stale
FakeIP becoming LAN, hidden asynchronous teardown failure and repair identity
guard/removal behavior. Each regression was observed failing before its fix.
UDP tests were first run before the adapter/field existed and failed with
`undefined: guardedSOCKS`, `undefined: newGuardedSOCKS` and the missing prepared
callback field; the real handshake and lifecycle assertions then passed with the
implementation. The mapped-address/authentication tests exercise pinned behavior
that endpoint checks alone would not catch.
FakeIP TDD added failing tests before the API/store existed. A later regression
test failed with `cursor would reassign an existing FakeIP`; high-water validation
fixed it. Persistence tests run real local file transactions and a child test
process that exits without Store.Close, with no TUN/network mutation.

Shared-probe TDD first failed before the shared API/adapter existed. Behavioral
RED evidence then included `pause retained reusable exit proof`, `reliable
mismatch retained proof`, `failed Prepare retained previous proof or outbound`,
`Prepare left the previous outbound available for a new probe`, `Prepare
finishing after Stop restored an obsolete outbound` and `selected DNS/connect
dial lost its attempt deadline`. The final unit and race runs pass all of those
regressions. The latter reproduced Go HTTP's actual detached-dial behavior;
the fix binds each attempt's adapter work to its own bounded context.

Initial verification used a temporary module copy because the original module
files were outside this task's edit scope. Main has since reconciled the shared
module dependencies. Final shared-probe verification runs directly from
`core` using the existing module in readonly mode:

```powershell
go test -mod=readonly -tags with_gvisor -count=1 ./exitprobe ./localengine ./tunengine
go test -mod=readonly -tags with_gvisor -race -count=1 ./exitprobe ./localengine ./tunengine
go vet -mod=readonly -tags with_gvisor ./exitprobe ./localengine ./tunengine
go build -mod=readonly -tags with_gvisor ./exitprobe ./localengine ./tunengine
go test -mod=readonly -tags with_gvisor -count=1 ./...
```

Earlier shared-probe discovery: 9 exitprobe tests, 15 localengine tests and 47 tunengine tests,
including one subprocess-only FakeIP helper that skips in the parent and runs
in its child. The localengine count includes the other owner's existing review
regressions, which were preserved. **All five commands above passed** on that stage's
source, including the complete core suite. Race verification covered the three
owned packages; the full-core result is a normal unit run, not full-core race.

Earlier shared-workspace runs observed out-of-scope build/schema failures in
`cmd/bluegrass-diagnostics`, `cmdupdate`, `nativehost`, and an earlier clock
calibration failure. An intermediate shared-probe full run also failed
`nativehost.TestReviewRunEOFCleansListener` with `EOF left listener alive`.
The final full-core run passed all these packages after concurrent main work;
none of those packages was edited by this task. This records a current checkout
result and does not attribute their fixes to the probe change.

The initial root-module readonly build lacked sums for full-router transitive
imports. The temporary module added these pinned-graph requirements:

```text
github.com/go-chi/chi/v5 v5.2.5
github.com/gofrs/uuid/v5 v5.5.1
github.com/hashicorp/yamux v0.1.2
github.com/sagernet/bbolt v0.0.0-20260915102804-500ee1e84832
github.com/sagernet/sing-mux v0.3.8
github.com/sagernet/sing-vmess v0.2.8
github.com/sagernet/smux v1.5.50-sing-box-mod.1
go.uber.org/multierr v1.11.0
go.uber.org/zap v1.27.1
golang.org/x/crypto v0.54.0
```

At that earlier stage main owned module/sum changes; the UDP adapter, FakeIP store
and shared probe added no new module version or module-file edits. The store imports the already
pinned bbolt module directly (currently listed indirect in the shared module).
Existing sing-tun and
miekg/dns imports become direct dependencies; their pinned versions are unchanged.
`proxy.golang.org` timed out; downloads completed using a command-local
`GOPROXY=https://goproxy.cn`. No machine proxy setting or proxy listener changed.

## Remaining release/runtime boundaries

- No elevated TUN forwarding, WFP persistence/boot/crash protection, physical
  capture, DNS leakage, IPv6 scope/multicast discovery, network-change timing or
  service/GUI installation test has been performed. Pure tests do not certify
  these properties. The wrapper must supply virtual-prefix policy, installed UI
  path and active network monitoring before runtime acceptance.
- SOCKS5 UDP relay authorization is implemented and tested as described above.
  Actual UDP forwarding, packet loss/fragmentation behavior, relay lifetime
  cleanup, and the resource's fixed IPv4 UDP exit are **not runtime verified**.
  Main must supply a session-bound callback and the exact WFP relay permission;
  nil callback intentionally leaves UDP blocked. Different relay IPs/domains
  remain unsupported. HTTPS success is never UDP evidence.
- Host-network acceptance still needs elevated multi-NIC/default-route/metric,
  DNS-server change, sleep/resume and roaming tests with physical capture. The
  read-only generation snapshot excludes this product's own TUN, but prompt
  detection, Block-before-reconnect ordering and no stale proof/permission across
  replacement depend on the host monitor and wrapper. Snapshot unit/smoke tests
  do not establish their runtime timing.
- DNS acceptance still needs ordinary application TCP/UDP 53 sent to a private
  router to reach the classifier before LAN/WFP exceptions; proxied public DNS,
  local-name/mDNS/PTR classification, and no public/local fallback must be observed
  on physical interfaces, including during failures and network changes.
- IPv6 acceptance still needs global literals, AAAA-only and dual-stack/QUIC
  application cases to demonstrate NODATA/public IPv6 rejection without direct
  fallback, while permitted local IPv6, link scope and mDNS remain correct. No
  runtime IPv6 leak/scope test was performed, and the package does not disable
  system IPv6. These remain release blockers despite pure matcher tests passing.
- FakeIP persistence is implemented when the host supplies CachePath. The host
  must provision its protected directory and wire explicit-disconnect cleanup.
  Successful native ACL/file access as the service and application reconnect
  continuity still need acceptance. Applications retaining FakeIPs beyond an
  explicit logical disconnect may need DNS refresh/restart, as the addendum
  already documents. Empty CachePath continues to mean volatile mappings.
- Three fixed HTTPS third-party endpoints and bounded fallback are implemented
  and tested as described above. The independent first-party endpoint is not
  deployed; its finalized response/deployment contract and trusted remote
  endpoint updates remain outside this change. No arbitrary URL override is
  accepted. Real TLS/service reachability through production resources has not
  been tested. TUN explicitly dials resolved IPv4 targets; localengine's retained
  selected outbound domain-dial behavior does not independently force a remote IPv4 target.
- Automatic local resolver selection has no cross-network or public fallback.
  Enterprise routing domains, resolver failover, ambiguous scoped multi-NIC
  paths and multicast beyond explicitly supplied scopes remain constrained.
  Known local scopes with no trustworthy path fail closed. The entire FakeIP
  candidate benchmark range is reserved even on an explicit test LAN.
- The selected-outbound engine destroys its TUN/router on pause. Independent
  local-only DNS/TUN and service/WFP integration under addendum 6/ADR0032 are
  separately owned by Tesla/Dirac in localonly*.go and the host packages. This
  task did not edit or claim that implementation; it requires its own lifecycle
  and elevated acceptance evidence, with no selected outbound/public DNS/FakeIP.
- Entry-DNS policy/5-second budget/failover, controller expiry, IPC owner/signature
  enforcement, persistent WFP, repair orchestration and distribution/licensing
  remain integrated by their respective main components. Non-SOCKS native/controller
  implementations are covered above, but provider delivery and actual TCP/UDP
  handshakes are not runtime verified. SMS is outside this package and verification.
