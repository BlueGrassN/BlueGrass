# Privileged macOS TUN network ownership

`Windows/native/core/mactun` composes the pinned Xray `tunengine` with the root-owned macOS PF, route and resolver transaction. This is the password-authorized development backend. It requires no Apple certificate and is not a Network Extension. Authorization, installed-worker verification, private IPC, nativehost dispatch, Dart HTTPS and packaging remain separate components.

## Composition contract

`mactun.New(ownerUID uint32) (*Tunnel, error)` is Darwin/root-only and acquires a system-wide nonblocking lease. `ownerUID` comes from the authenticated IPC peer, never from a JSON profile. Another owner, an unsafe directory, an incompatible journal, or an unowned occupied anchor is refused. Non-Darwin `New` returns `MACOS_ROOT_TUN_REQUIRED`; the transactional implementation and fake backend tests remain runnable on Windows.

The returned `Tunnel` implements:

- `engine.Engine`: `Prepare`, `ProbeExit`, `SetBusinessGate`, `Stop`.
- Nativehost network/tunnel contracts: `Address`, `VerifiedIPv4`, `Counters`, `Close`, `Active`, `Release`, `SetAllowLAN`.
- `SetEntryDNS(entrydns.Policy) error` and `ResolveControlHost(context.Context, string) ([]netip.Addr, error)`.
- `ResumeLogicalSession(engine.ResourceKey) (string, error)`, `BindLogicalSession(engine.ResourceKey, string) error`, and `SwitchLogicalSession(old, next engine.ResourceKey, id string) error`.
- `Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)`.
- Optional `ProtectionState(bool) string`, `NetworkChanged() bool`, and `Forwarding() bool`.

The privileged nativehost constructor should create `mactun.New(uid)`, pass it to `nativehost.NewTunnel`, and always close the host/engine on transport loss. Its serialized dispatch/poll loop must call `Host.NetworkChanged()` when `Tunnel.NetworkChanged()` is true. The manager has already closed forwarding; this notification changes the controller and GUI to paused instead of leaving a stale connected snapshot. The latch clears only after a fresh successful `Prepare`. Protection labels are `pf_blocking` and `pf_forwarding`; uncertain cleanup returns `unknown`.

Nativehost's logical-session error mapper must recognize `mactun.ErrOwner` as `OWNER_MISMATCH`, alongside its existing Windows sentinel. The rejection is already enforced by the manager; this preserves truthful platform-neutral error classification.

`Prepare` validates and binds the resource checkpoint, arms PF before any takeover, resolves the entry with bound root sockets, and prepares the existing Xray engine. It leaves the business gate closed. `SetBusinessGate(true)` starts the real adapter, verifies the actual kernel index, installs and verifies owned split-default routes and DNS, enables its PF tunnel permit, and only then succeeds. Repeated activation verifies the current transaction instead of applying it again.

Darwin uses `utun233`. `InterfaceLUID()` is its actual kernel interface index, not 233. The platform opener supplies interface addresses and starts the device without automatic global routes or DNS. **The DNS peer is `VirtualPrefixes()[0].Addr().Next()`**, for example `172.31.255.1`. Xray assigns `.2` to the Darwin host; using `.2` for the resolver would incorrectly target local delivery.

## PF and socket policy

The manager owns only `com.apple/zz.bluegrass` beneath the already-installed `anchor "com.apple/*" all` filter hook and its own `pfctl -E` reference. It never loads `/etc/pf.conf`, calls global `-f`, flushes global rules/states, or calls `pfctl -d`. Explicit release uses only the recorded token with `pfctl -X`.

Admission is deliberately conservative. The main filter ruleset must be exactly the supported Apple wildcard hook. Its parent and other child anchors must have no filtering/nested rules. Existing PF states and skipped non-loopback interfaces are refused because they can bypass the new blocking policy. Unsupported setups fail before takeover rather than being rewritten. PF rules are parsed by `pfctl` with optimization disabled and compared with kernel readback; the active hook, enabled state and owned reference are checked independently.

All emitted passes use `no state`, with explicit TCP `flags any`. Thus removing a permit cannot leave a state-table exception behind. Rules allow loopback, physical DHCP, validated/scoped LAN destinations when enabled, the current IPv4 utun, and precise endpoint exceptions. Public IPv6 is blocked, including globally addressed connected IPv6 prefixes. LAN exceptions do not become an unrestricted public rule.

When LAN is enabled, mDNS uses only the verified default physical link, `224.0.0.251:5353` / `[ff02::fb]:5353`, with scoped IPv6 neighbor discovery. These local multicast exceptions also accommodate macOS's non-root mDNSResponder; they do not permit public UDP or cross-interface discovery.

Node, physical resolver, authenticated SOCKS UDP relay and split-routing direct endpoint exceptions are limited to **UID 0** on the captured physical interface and exact IPv4/port/protocol. There is no unrestricted root or user exception. Xray already binds its node/direct/relay sockets to the physical interface. The manager's resolver additionally checks the underlay before and after dialing, binds the source address, and applies `IP_BOUND_IF`; it never falls back to the system resolver or an unbound socket.

Root node/DNS/relay/direct sockets need no globally visible host route. Only GUI HTTPS gets an owned physical `/32` pin, with a TCP 443 permit restricted to the authenticated owner UID. `ResolveControlHost` rejects IP literals, malformed names, local/reserved names, and nonpublic answers. It returns a numeric public IPv4 only after the physical route and PF permit verify. Dart retains the original URL/SNI/certificate validation and bearer tokens; this package sends no API requests and stores no account tokens.

The engine receives UDP callbacks only when its typed protocol requires an authenticated SOCKS relay. Callbacks are bound to the current session, validate public IPv4/port/protocol, maintain reference counts, and reject retired sessions. Global mode receives no split-direct callback. `bypass_cn` delegates destination decisions to the existing routing engine and grants only its precise direct callback endpoints. Permit counts and API pins are bounded; exhaustion fails closed.

## Journal, recovery and external writers

The shared IPC constants are used directly:

- Public marker: `/Library/Application Support/BlueGrass/tun-intent.json` (`mactunipc.IntentPath`), mode 0644, only version/owner UID/intent/recovery flags. Its root-owned parent is mode 0755 so the authenticated user's startup can discover retained protection after reboot.
- Private state: `/Library/Application Support/BlueGrass/Tunnel`, root-owned mode 0700.
- Journal: `network-v1.json`, root-owned mode 0600.
- System lease: `network.lock` inside the private directory; a process-held `flock`, not a PID-file lease.

Runtime sockets/transport state use Apple's canonical `/private/var/run` directory; discovery intent does not depend on that volatile directory. Marker writes and removals use the shared stable `IntentPath` and sync its actual parent. Directory chains reject symlinks, non-root owners, group/other writes, extended ACLs and filesystems ignoring permissions. Files reject symlinks, hard links and unsafe modes. Writes use a same-directory temporary file, sync/full-sync, atomic rename, and parent directory sync. Existing foreign permissions are not repaired or chmodded into acceptance.

The versioned journal contains only ownership/recovery metadata: a random transaction identity, hashed resource key, opaque logical-session identity, PF rules/reference, DNS original/desired settings and exact owned routes. It contains no node username/password, profile, API hostname, bearer token or account token. PF enable tokens are kernel ownership receipts, not account credentials.

Every network mutation has a durable checkpoint first. PF records both current and pending compiled rules to handle a crash between kernel commit and final acknowledgement. DNS preserves automatic configuration as an empty original server list and separately records effective/independent DHCP DNS. Routes are created only when the exact route is absent; an existing matching physical API route may be borrowed but is never journaled or deleted. Owned routes are compared by prefix, interface, gateway and metric before verification/deletion. Exact kernel hopcount is read independently from the route RIB.

Restoration compares current settings against the recorded original and installed values. A changed foreign route/DNS setting is left untouched, its recovery record is retained, and Release fails. An already-restored original or already-absent owned route is accepted as a completed prior step. Failed writes, partial restoration, marker failure or journal removal failure cannot acknowledge a successful disconnect. Explicit retries resume from the durable checkpoint.

Pause and process close retain the journal, marker and PF intent. The fast pause transaction first records a narrower ruleset and removes all tunnel/root-peer passes, preserving scoped LAN and owner HTTPS. Only then does it perform slower hook/reference verification and engine cleanup. Root DNS permissions are reauthorized by a subsequent explicit control lookup. Transport loss therefore prioritizes kernel blocking and recovery metadata before the IPC worker's short exit grace; process death also closes its utun/sockets. No background thread reconnects or selects another resource.

Recovery installs a blocking guard and returns paused, with no recovered forwarding/relay/direct permissions. A missing physical link still permits a pure blocking guard. A verified missing owned token can be replaced with a new owned reference; token existence uses Apple's read-only `DIOCGETSTARTERS` ABI, while mutations remain `pfctl -E/-X`. A recorded token's already-completed release is idempotent. If a crash occurred after `-E` but before its returned token was durably recorded, ownership of that particular reference cannot be established: the journal is deliberately retained and recovery reports an error instead of guessing a token or disabling PF globally.

Explicit `Release` first closes forwarding, restores and verifies owned DNS/routes, then clears only the owned anchor, releases only the owned PF reference, removes the public marker and finally removes the private journal. The manager never deletes an unrecovered journal. Logical-session switching validates the full previous key and same account boundary before stopping or rebinding; arbitrary account/resource takeover is rejected.

## Generation and timeout boundaries

Network reads use `tunengine.SystemNetworkWithOwned(ctx, originalsDNS, ownedPins)`. Before supplying either masking input, the manager verifies its owned DNS override, the independent DHCP source/service mapping, its PF hook/rules/reference, and each exact physical route pin. Only journaled physical pins that still match enter `[]tunengine.OwnedPhysicalRoute`; prefix/interface/gateway/metric must all match. Adapter routes are excluded by the platform itself. Own DNS and API pins therefore disappear **before generation hashing**, without replacing the engine's generation or accepting an unexplained post-transaction baseline.

Real route/interface/resolver changes, loss/tampering of owned settings, or cancellation close forwarding and latch `NetworkChanged`. A fresh explicit Prepare restores the previous transaction before rebuilding. A paused control lookup also checks resolver proofs and network generation: DHCP DNS-only changes rebuild bootstrap even when interface/source/gateway are unchanged. The manager first narrows PF, then compares/restores owned DNS/routes and captures fresh resolver/network state. It never overwrites an external DNS/route writer or restarts business forwarding.

System commands use fixed absolute tools, argument arrays, fixed locale/PATH and no inherited environment. Each command has a 1.5-second limit and bounded output. Prepare has a 60-second preparation bound while retaining the caller's separate session lifetime; activation has 20 seconds, control lookup 8 seconds with a 5-second DNS budget, pause 3 seconds and explicit Release a shared 4-second transaction deadline. Cancellation interrupts an in-flight prepare/control operation before Stop waits for serialization. Engine close is the existing bounded platform cleanup. Timeout/error remains a failure with retained recovery, never a false disconnect acknowledgement.

`Config.CachePath` is the fixed root-private `/Library/Application Support/BlueGrass/Tunnel/fakeip.db`. The existing protected Xray cache implementation validates every ancestor/file and namespaces mappings by hashed resource/routing/network policy. Pause, process Close and recovery preserve logical-session mappings. Explicit Release calls `tunengine.ClearCacheMappings` only after all engines close, before releasing protection. This clears names/addresses while preserving validated cache metadata and per-pool allocator high-water marks, so an application-held old FakeIP cannot be reassigned to another name after disconnect. Windows retains its existing `ClearCache` behavior. A locked/unsafe/unclearable cache fails Release and retains the journal and guard; the underlying database's exclusive-lock wait is bounded at 100 ms. No IPC-selected cache path or credential data is accepted.

## Verification

From `Windows/native/core`:

```text
go test ./mactun
go test -race ./mactun
go vet ./mactun
```

Portable tests cover guard-before-takeover ordering, partial mutations, pending PF commit recovery, foreign writers, same-owner logical continuity, wrong-owner/version rejection, journal secret exclusion, DNS source masking, session permit revocation/reference counts, physical control resolution, permit injection, public IPv6 blocking, and release failure/retry. `Backend` is a typed OS boundary supplied only by trusted composition, not IPC configuration.

On an **ephemeral macOS runner**, compile tests normally and run the resulting binary as root:

```sh
go test -c -o "$RUNNER_TEMP/mactun.test" ./mactun
sudo /usr/bin/env BLUEGRASS_MACTUN_KERNEL_TESTS=1 \
  "$RUNNER_TEMP/mactun.test" -test.v -test.count=1 \
  -test.run '^TestDarwinKernel' -test.timeout=2m
```

The opt-in tests parse the actual PF policy, load/compare/remove an **unreferenced, unique test anchor**, add/verify/remove only `198.19.255.254/32` on loopback, and read PF reference metadata. They never enable/disable PF, attach the test anchor to the live hook, alter the default/control route, change configured service DNS, or touch the production journal/anchor. Existing test-route occupancy causes a skip. Cleanup checks ownership before removing anything. Run the platform worker's separate `tunengine` TCP/UDP/DNS utun tests as documented in `tun-platform.md`.

Windows unit/race/vet and Darwin cross-compilation are development checks, **not macOS execution evidence**. Actual PF parser/kernel/reference ABI, resolver takeover, full app authorization, global/split/LAN traffic, IPv6 blocking and real Mac recovery still require macOS CI/device acceptance. A skipped root test is not acceptance. The supported underlay is currently Ethernet/Wi-Fi `enN` with a usable IPv4 gateway. Resolver takeover selects only Up hardware links with a usable IPv4 source, and requires the default link's actual source; enabled but unassociated Wi-Fi, IPv6-only links and self-assigned/link-local IPv4 do not trigger DHCP inspection. Participating links' automatic DNS still requires an independently readable DHCP source. Unsupported managed PF configurations, VPN underlays and unverifiable active resolver sources fail closed.

Apple ABI references: [PF ownership structures](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/pfvar.h), [reference acquire/release/read implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/pf_ioctl.c), and [anchor traversal](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/pf.c).
