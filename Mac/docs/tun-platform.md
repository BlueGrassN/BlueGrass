# Darwin native TUN platform

This is the administrator-authorized desktop utun backend for the development build. It uses the repository's exact pinned Xray module, `github.com/xtls/xray-core v1.260327.1-0.20261017152356-92ccc939aaf4`, including `proxy/tun` and its native TCP/UDP stack. It introduces no Network Extension or second proxy/packet engine. Authorization, installation, IPC, PF, route/resolver transactions and app packaging belong to the other platform components.

## Host integration contract

- `InterfaceName` is `utun233` on Darwin and remains `BlueGrass` elsewhere. Opening requires effective UID 0. Any existing `utun233` is refused, including one left open by another BlueGrass process. The kernel exclusively allocates the exact utun unit; the opener never attaches to or renames an existing interface. Inherited Xray `xray.tun.fd` / `XRAY_TUN_FD` mode is refused.
- `InterfaceLUID()` means the actual kernel interface **index** on Darwin. It is neither 233 nor the kernel control unit number 234. The existing engine publishes this index after native activation. The checked device validates identity at Start, closes once, and checks disappearance during Close.
- `nativeOptions` retains gateway/MTU and DNS intent. Immediately before `tun.NewTun`, the opener deep-clones the protobuf config and clears `AutoSystemRoutingTable`, `DNS`, `AutoSystemDnsToGateway`, `AutoOutboundsInterface` and `AutoSystemWfpBlockLeak`. Xray sets MTU and its owned interface addresses/connected routes, but does not install split/default routes or change system DNS. The manager owns PF, other routes and DNS, including cleanup and external-writer checks.
- **Darwin reverses the Windows link-address convention:** for `172.31.255.1/30`, Xray assigns `.2` to the host and `.1` to the remote TUN peer. The DNS target is `.1`. Since `VirtualPrefixes()` returns the masked `.0/30`, the manager obtains this DNS target with `VirtualPrefixes()[0].Addr().Next()`. `.Next().Next()` targets the host and is wrong on Darwin. Upstream currently assigns only IPv6 link-local addresses, despite retaining the configured IPv6 gateway intent; public IPv6 protection belongs to the root manager.
- Continue to compose `New(Config)`, `Prepare`, `SetBusinessGate` and `InterfaceLUID`. PF must already block forwarding while the adapter opens. Keep PF protection until owned routes/resolvers are verified, and retain it through failures and cleanup. This platform alone makes no connected/protection claim.

`SystemNetwork(ctx)` delegates to Darwin-only `SystemNetworkWithDNS(ctx, nil)`. The implementation reads Go's interface/address enumeration, the Darwin route RIB and `/usr/sbin/scutil --dns`. It requires two matching canonical snapshots, excludes this product's adapter/routes, ignores ephemeral ARP/cloned routes, and does not treat scoped copies of default routes as the primary route. Physical selection considers only Ethernet/Wi-Fi (`enN`), bridge, bond and VLAN interfaces. Other utun, PPP, IPsec, tunnel, loopback and Apple peer links cannot become physical fallbacks. Equal-cost conflicting underlays fail closed. Supplemental domain-only DNS is not promoted into entry/bootstrap DNS.

`SystemNetworkWithDNS(ctx, originals map[string][]netip.Addr)` takes **interface names**, such as `en0`, as keys. A present empty value means that interface originally had no DNS. The trusted manager must first verify its own installed override still matches the current settings; only then may it provide recorded original effective DNS. Substitution happens on a copied adapter list **before** `systemSnapshot` hashes it. Owned DNS takeover consequently does not change generation, while adapter, route, gateway and unmasked resolver changes do. This function does not authenticate ownership itself and must not accept a GUI/IPC-provided overrides map. The manager must serialize its originals map against concurrent updates.

Darwin process-path lookup intentionally remains unavailable (`not found`); GUI/API HTTPS therefore requires the protected host's explicit underlay address pins. No process-based API exception is claimed here. The existing optional `Config.UIExecutable` validation is Windows-oriented; callers can omit it, as its default cannot match a Darwin process lookup.

## Persistent fake-IP cache

The trusted root host supplies `Config.CachePath`; it is never profile/IPC input. Empty means volatile cache. Provision the final parent as root-owned mode 0700 before opening, for example `/Library/Application Support/BlueGrass/Tunnel/fakeip.db` if **every** ancestor satisfies the checks. The implementation never creates, repairs or chmods a directory. Use physical path spellings: `/private/var/...` rather than macOS's `/var` symlink.

Every ancestor is pinned using descriptor-relative `openat` with `O_NOFOLLOW`; each must be root-owned, non-writable by group/other and free of an extended ACL. The final parent must also deny all group/other access. The DB must be a root-owned, mode-0600, single-link regular file within the size limit. Extended ACLs, symlinks, hardlinks, permissive modes, untrusted owners and mounts that ignore permissions are refused. bbolt retains exclusive ownership and the existing transactional fake-IP format/retention/limits. `Stop` preserves mappings.

For explicit Mac disconnect/release, call `ClearCacheMappings(path string) error` after all engines close. It transactionally empties the names/address buckets while retaining validated metadata and allocation highwaters for **both** `198.18.0.0/16` and `198.19.0.0/16`. An application can retain a FakeIP DNS answer for its 600-second TTL across disconnect/reconnect; its old address must fail reverse lookup, never identify another domain. Reopening either pool continues above its previous highwater, including after repeated releases; an exhausted pool stays exhausted. Empty paths and empty databases are safe no-ops. The operation rejects live stores, invalid metadata and mappings inconsistent with the recorded highwater before deleting any mapping evidence. Existing Windows callers retain `ClearCache`'s full-reset semantics; the Mac release path must use `ClearCacheMappings` instead.

ACL inspection uses descriptor-based `fgetattrlist(ATTR_CMN_EXTENDED_SECURITY)`, validating the attribute reference and accepting only no ACL or Apple's exact `KAUTH_FILESEC_NOACL` representation. Layout references: [Apple kauth.h](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/kauth.h) and [Apple vfs_attrlist.c](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/vfs/vfs_attrlist.c). It rejects an explicitly empty ACL as well as populated ACLs. Unsupported attribute reads fail closed.

## Tests and CI execution

Portable tests execute on Windows: policy cloning, no caller mutation, DNS substitution before hashing, empty originals, topology-change detection, resolver scoping/supplemental handling, physical-name policy, security-buffer parsing and the kernel test's actual Xray TCP/UDP redirect/echo fixture through a memory-backed Xray stack. Cache regressions disconnect/reopen real bbolt stores within the DNS answer TTL, switch between both pools and prove old addresses cannot identify a newly queried domain. They also cover repeated/empty releases, byte-for-byte metadata preservation, retained pool exhaustion, live-store refusal, atomic rejection of corrupt allocation evidence and unchanged legacy full-reset behavior. Existing shared engine, DNS, fake-IP, policy and lifecycle tests remain applicable. Windows options and platform implementations retain their behavior.

Activation rollback tests enforce the platform's route contract: Darwin supplies no automatic system routes because the manager owns them; Windows retains Xray-owned routes. Gate, host-DNS/interface policy and rollback checks still apply on both platforms. Wintun GUID/driver repair tests are Windows-only; Darwin adapter ownership is instead checked by the dedicated utun identity tests. Portable TCP/UDP, SOCKS authentication, relay permission/release, concurrent-flow, control-EOF, cancellation and real Xray packet tests must run on Darwin without skips.

The desktop `protocolconfig/dialer_desktop.go` Xray system-dialer adapter compiles for Windows and Darwin. It consumes each flow's `WithPhysicalDialer` guard and retains physical sockets only for the correct flow/session lifetime, while leaving default Xray socket creation/interface binding intact for unguarded dials. `TestXrayPhysicalGuardIsUsedForTCPAndUDP` exercises this boundary with a loopback-only fallback and fails explicitly if the guard is ignored. ARM CI run `42008762639` exposed the original missing Darwin registration, which prevented SOCKS authorization and let physical handshakes survive caller cancellation. The fix shares the production adapter; portable transport tests and physical guards remain enabled.

Darwin-only tests compile for arm64 and amd64. They cover route-RIB decoding, scoped/default/connected/cloned routes, cancellation, VPN underlay refusal, DNS peer convention and adapter/inherited-FD rejection. **All Darwin system APIs and real-kernel behavior remain unverified on the Windows development host until macOS CI runs them. Cross-compilation is not execution evidence.**

`BLUEGRASS_DARWIN_KERNEL_TEST=1` enables two root-only tests; without the variable they explicitly skip. If it is set without UID 0, they fail:

- `TestDarwinKernelUtunXrayTCPUDPDNSAndCleanup` opens a real `utun233`, checks its real index, refuses a second open, adds only private host route `10.253.233.9/32`, and sends ordinary kernel TCP/UDP through utun, the pinned Xray native stack/router and a real Xray freedom outbound redirected to local echo servers. It sends TCP and UDP DNS to the remote peer through production DNS handling. It checks counters, unchanged physical-network generation during activation, no default/split-default route on its adapter, route removal, interface disappearance and restored generation. The private prefix must be unoccupied. The adapter's /30 is selected from the existing private test pools. No default-route, system DNS or PF writes are performed by the test.
- `TestDarwinKernelProtectedCache` creates a unique root-private directory beneath `/private/var/root`, proves persistence/exclusive access and rejects mode, ACL, hardlink and file/ancestor-symlink attacks. It removes only its own directory.

Run on an ephemeral macOS runner, once per architecture. From `core`, the preferred invocation builds as the runner user and runs only the compiled tests as root; root needs no Go/module/build cache:

```sh
set -eu
test_dir="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/bluegrass-tun-tests.XXXXXX")"
trap 'rm -rf "$test_dir"' EXIT
go test ./tunengine -count=1
go test -c -o "$test_dir/tunengine.test" ./tunengine
sudo /usr/bin/env BLUEGRASS_DARWIN_KERNEL_TEST=1 \
  "$test_dir/tunengine.test" -test.v -test.count=1 \
  -test.run '^TestDarwinKernel' -test.timeout=3m
```

For a direct `sudo go test`, first warm the exact module/toolchain as the runner user, then pass its resolved toolchain explicitly and allocate separate root-owned temporary/build caches. This avoids sudo dropping the downloaded Go toolchain from PATH or writing root files into the user's build cache:

```sh
set -eu
go test ./tunengine -run '^$'
test_go_root="$(go env GOROOT)"
test_mod_cache="$(go env GOMODCACHE)"
test_root_tmp="$(sudo /usr/bin/mktemp -d /private/var/root/bluegrass-go-tests.XXXXXX)"
trap 'sudo /bin/rm -rf "$test_root_tmp"' EXIT
sudo /bin/mkdir -m 700 "$test_root_tmp/build" "$test_root_tmp/tmp" "$test_root_tmp/gopath"
sudo /usr/bin/env \
  PATH="$test_go_root/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  GOROOT="$test_go_root" GOTOOLCHAIN=local \
  GOMODCACHE="$test_mod_cache" GOPROXY=off \
  GOPATH="$test_root_tmp/gopath" GOCACHE="$test_root_tmp/build" \
  GOTMPDIR="$test_root_tmp/tmp" BLUEGRASS_DARWIN_KERNEL_TEST=1 \
  "$test_go_root/bin/go" test -mod=readonly ./tunengine \
  -run '^TestDarwinKernel' -count=1 -timeout=3m -v
```

Use an already configured compiler and `go test -race -c ...` for an additional native race run where available. Keep logs showing both kernel tests actually ran; a skipped run is not acceptance. These tests establish native platform packet flow and cleanup, not full app authorization, PF ownership, global-route takeover, physical Mac UX or production signing acceptance. Those belong to the lead's CI/device integration.

Development-host verification uses unfiltered `go test ./tunengine -count=1`, `go vet ./tunengine`, Windows race tests, and `GOOS=darwin GOARCH=arm64/amd64 CGO_ENABLED=0 go test -c ./tunengine`. Windows requires the approved `Windows/scripts/assets/wintun/amd64/wintun.dll` fixture matching the runtime SHA-256 pin; with that fixture provisioned, do not exclude its validation test. Full-module tests must also be rerun after the parallel authorization/manager/nativehost work settles.
