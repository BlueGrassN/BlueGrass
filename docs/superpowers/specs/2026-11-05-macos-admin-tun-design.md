# macOS administrator-authorized TUN development build

## User decision

The user explicitly requests full TUN functionality before obtaining Apple certificates, using a system password authorization prompt. This supersedes the previous development-package decision to disable TUN. UI remains shared with Windows; development remains on Windows and macOS builds run on GitHub. The user performs actual Mac acceptance.

Use the pinned Xray native Darwin utun implementation and TCP/UDP stack. This is a privileged desktop utun implementation, not a Network Extension or an entitlement bypass. A later Network Extension distribution still needs its own signing/deployment/lifecycle acceptance.

## Architecture

1. `tunengine` supplies Darwin adapter/network/cache implementations. Keep shared Xray routing, fake DNS, TCP/UDP, DNS policy, traffic and resource lifetime logic. Disable upstream automatic OS route/DNS mutation on Darwin; the owned platform manager controls those transactions. Physical sockets bind to real interfaces through existing Xray/sing utility APIs. No second proxy engine or packet stack.
2. `mactunipc` supplies administrator authorization, a protected worker installation and private Unix IPC. The unprivileged BlueGrass.Native broker presents macOS's system password dialog through a fixed osascript launcher. Passwords never enter Flutter or application storage. Only the bundled `BlueGrass.Tunnel` executable whose SHA-256 is embedded into the broker is installed into a root-owned content-addressed location, verified after copying, then launched on demand. No arbitrary command/config/path IPC and no startup LaunchDaemon.
3. The root worker accepts only the authorized broker's UID, PID and kernel process-start identity, on a protected Unix socket. Bounded framed requests retain existing nativehost JSON schemas and generation checks. It watches broker death; connection loss closes forwarding and preserves recovery/protection intent. Late authorization cannot create a usable worker after the broker has exited.
4. `mactun` composes Xray with root-owned PF, route and resolver transactions. Its durable journal stores ownership/recovery metadata only, never account tokens or node secrets. Kernel PF protection survives worker failure; explicit Disconnect/Release restores owned DNS/routes before releasing only this product's PF anchor/token. No global PF flush, disabling other users' PF references, or overwriting another writer's settings.
5. The shared UI enables TUN and truthful administrator-authorization copy. API HTTPS remains in Dart, preserves TLS hostname/certificate validation and bearer-token boundaries, and uses numeric IPv4 addresses resolved/pinned by the protected native worker while TUN is active. It never falls back to uncontrolled DNS/direct traffic on failure.

## Integration interfaces

- `tunengine.InterfaceName`: Darwin uses a checked, dedicated `utun233`; other platforms retain BlueGrass. Refuse an existing unrelated adapter.
- `tunengine.SystemNetwork(ctx)` works on Darwin and excludes this product's adapter/routes. `tunengine.SystemNetworkWithDNS(ctx, map[string][]netip.Addr)` permits trusted host substitution of recorded original DNS only after the host verifies its owned override is still installed. Use physical adapter/interface binding; don't select other utun devices as underlay.
- `tunengine.New(Config)` is retained. Darwin opens its adapter and stack without automatically installing default routes/DNS. `InterfaceLUID()` returns the real interface index. Host owns routes/DNS/PF and advertises connected only after all are verified.
- `mactun.New(ownerUID uint32) (*Tunnel, error)` is root-only. Tunnel implements nativehost.NetworkEngine and TunnelEngine, plus SetEntryDNS, ResolveControlHost, required logical-session and diagnostic contracts. It owns global network lease/journal and fail-closed recovery.
- `nativehost.RunPrivilegedDarwinTunnel(input io.Reader, output io.Writer, ownerUID uint32) error` is the lead-owned composition entry; it constructs mactun, calls NewTunnel and runs the existing host stream. Privileged IPC authenticates before invoking it.
- `mactunipc` owns constants: `RuntimeDirectory=/Library/Application Support/BlueGrass/Run`, `StateDirectory=/Library/Application Support/BlueGrass/Tunnel`, `IntentPath=/Library/Application Support/BlueGrass/tun-intent.json`. Every runtime ancestor must be root-owned and non-writable by group/other; macOS's group-writable `/private/var/run` is unsuitable. The durable intent marker exposes only UID/active/recovery state, not secrets, so unprivileged startup can recognize required TUN recovery after reboot stops the worker. A new authenticated worker acquires the exclusive lease before removing any stale owned socket.
- `mactunipc.ExpectedWorkerSHA256` is injected at build time. Build/sign BlueGrass.Tunnel first, calculate its hash, build BlueGrass.Native with that hash, then sign the rest without modifying the already hashed worker.
- Darwin nativehost broker owns RunTunnelBroker and recovery handoff from system-proxy startup. The worker command is `cmd/bluegrass-tunnel`; binary `BlueGrass.Tunnel` is beside other app executables.

## Network behavior and validation

- Public IPv4 TCP/UDP follows selected resource and global/bypass_cn policy; public IPv6 is blocked. LAN, local names/mDNS and control traffic retain explicit scoped rules.
- Protect before exposing default routes. Allow only scoped loopback/control/entry/DNS/LAN/direct-policy exceptions. SOCKS UDP relay permissions follow authenticated BND replies, are session-scoped and are revoked on pause.
- Detect link/route/resolver changes, close forwarding before rebuilding; never silently switch resource, protocol or bypass protection. Preserve original system preferences and external changes using compare-before-restore.
- Tests cover authorization cancellation/late approval, wrong UID/PID/reused PID, schema/size/time bounds, partial setup/rollback, external writers, journal failures, crash recovery, pause/expiry/disconnect and socket cleanup.
- Run real utun packet TCP/UDP/DNS tests on ephemeral GitHub macOS runners using only owned test routes/prefixes, with before/after cleanup assertions. Validate PF syntax/owned anchor behavior without interrupting GitHub's control connection. Device/global-route acceptance stays explicitly separate.
- Package both arm64 and Intel, all helpers, development signatures, version metadata, SHA-256 and exact test instructions. No production account credentials or Apple certificate required for this development path.
