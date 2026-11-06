# Windows update and machine installation

Status: update core, fixed-config native update helper and Dart process adapter
implemented and at a stable compile/test checkpoint. The machine installer
template was compiled with a snapshot of **actual GUI/native binaries**, with
source/hash evidence; the next final snapshot must follow the unified build and
include both `BlueGrass.Update.exe` and `BlueGrass.Diagnostics.exe`.
**Not a distributable Beta.** The candidate unconditionally refuses installation.
The current native binaries predate the next `with_gvisor` integrated rebuild;
the GUI Release snapshot is also older than current source. No BlueGrass
installation, service start, firewall/TUN/proxy change or publication was run.
SMS is outside this work. All existing dirty work is retained.

## Current helper integration checkpoint

The source API is frozen for the unified build. No manifest URL, key, publisher,
package path, installer arguments or executable path is accepted from the UI.

```go
// bluegrass/native/core/cmdupdate
func Run(ctx context.Context, input io.Reader, output io.Writer) error
func Apply(ctx context.Context, candidateID, sourceCacheDir string) error
func TrustedHosts() []string
```

`Run` is the unelevated, one-request JSON boundary used by
`BlueGrass.Update.exe --rpc`. `Apply` is only the elevated internal staging path
(`--apply <opaqueID> <untrusted-source-cache-directory>`), not a UI API. Neither
accepts trust settings. `TrustedHosts` returns a defensive copy of the built-in
allowlist; the current unconfigured build returns no hosts.

`Windows/native/core/cmdupdate/release.json` is compiled into the executable.
All default release values are empty; check/download/launch return
`UPDATE_UNCONFIGURED` before trust inspection, storage or networking. Release
engineering must set the real version/build/channel, approved HTTPS manifest
URLs and hosts, and one/two real public Ed25519 keys **before building and signing
all product executables**. No keys or feeds have been fabricated. The publisher
pin is derived from the verified installed executable, not supplied by this file.
The service and helper must be built from the same config because the service's
DNS whitelist comes from `TrustedHosts()`.

### Flutter boundary

`Windows/app/lib/update_client.dart` exposes:

```dart
UpdateClient();
Future<UpdateSnapshot> check();
Future<UpdateSnapshot> download(String candidateId, {bool allowMetered = false});
Future<UpdateSnapshot> launch(String candidateId, {required bool installConsent});
Stream<UpdateSnapshot> get progress;
bool get busy;
void cancel();
Future<void> dispose();
```

The adapter starts only the exact adjacent `BlueGrass.Update.exe --rpc` using
`Process.start(runInShell: false)`, with no PATH or environment-based executable
selection. The helper additionally requires canonical KnownFolder
ProgramFiles/BlueGrass, protected machine ACLs and matching trusted GUI/helper
signer certificates. The helper RPC refuses elevated execution. One process owns
one request; input, output, frames, deadlines and concurrent calls are bounded.
The testing process factory is explicitly `@visibleForTesting`.

Wire request examples (one object, then close stdin):

```json
{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check"}
{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"download","candidateId":"<64-lowercase-hex>","allowMetered":true}
{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"launch","candidateId":"<64-lowercase-hex>","installConsent":true}
```

Replies are bounded JSON lines with matching `v`/`id`, `ok` and either `snapshot`
or a safe `error` code. Download progress also has `event: "progress"`. Snapshot
states are `up_to_date`, `available`, `downloading`, `ready`, `launch_requested`;
typed Dart equivalents are `upToDate`, `available`, `downloading`, `ready`,
`launchRequested`. Fields expose opaque candidate identity, version/build, signed
size/notes, minimumVersion/unsupported, downloaded/remaining and fromCache.
Unknown fields, duplicate request keys, trailing requests and candidate/path
substitution fail closed. Launch cancellation/timeout after elevation has an
unknown outcome (`UPDATE_LAUNCH_OUTCOME_UNKNOWN` in Dart); never retry it
automatically. Successful launch means process creation, not installation success.

### Floors, consent and elevation

The helper owns a per-user DPAPI state under KnownFolder
LocalAppData/BlueGrass/updates/<channel>-<arch>, with an exclusive process lock,
protected ACL, atomic write-through save and the raw signed manifest. It saves
the sequence/digest floor before exposing an accepted candidate. Every restart,
download and launch reconstructs the candidate by verifying the saved signature,
expiry, applicability and floor. Corrupt state fails closed; still-valid signed
cache can be shown after a failed online check with `fromCache=true`.

Per-user DPAPI is not a security boundary against the owning user deleting or
rolling back their own state. The elevated stage independently verifies the
handoff against a machine floor in ProgramData/BlueGrass/updates/<channel>-<arch>
(Administrators/SYSTEM only), and saves that floor before staging. It copies only
the exact candidate-derived package name into that protected directory, checks
size/hash/WinVerifyTrust/publisher again and holds a no-write/delete file handle
through direct process creation. An untrusted source directory does not select
the executable or its arguments. Installed version/build comes from the compiled
release baseline; a successful launch never advances it.

The helper now obtains route-specific Windows NLM cost. `download` with
`allowMetered=false` may proceed only when its verified physical route's exact
NLM connection reports unrestricted cost. Unknown/metered/expensive states return
`UPDATE_WAITING_NETWORK`; `allowMetered=true` permits only that verified candidate
during that request. Checking a feed does not authorize metered package download.
Cancellation leaves bounded resumable data; the UI owns bounded resumption and
must suppress automatic resumption after user cancellation. Installation remains
a separate explicit-consent path.

### D36/D37 physical-route cost implementation

`cmdupdate/route_binding_windows.go` consumes the shared, main/Dirac-owned APIs:

```go
entrydns.SelectPhysicalRoute(ctx, destination) (PhysicalRoute, error)
entrydns.VerifyPhysicalRoute(ctx, route) error
entrydns.DialPhysical(ctx, "tcp", numericEndpoint, route) (net.Conn, error)
```

Before a package request, authenticated service DNS resolves only a compiled host.
The helper selects a destination-specific physical route and binds its socket
through `DialPhysical`: exact numeric IPv4/TCP443, source IP and interface index.
The same immutable route snapshot supplies the adapter GUID used for cost. There
is no unbound `net.Dial` or system-DNS fallback. Manifest transfers also use the
physical transport, although they do not require package-download consent.

`cost_windows.go` directly calls Windows COM, using SDK `netlistmgr.h` interfaces:
enumerate `INetworkListManager.GetNetworkConnections`, match
`INetworkConnection.GetAdapterId` to the **selected physical interface GUID**,
require a connected, unambiguous match, and query that connection's
`INetworkConnectionCost.GetCost`. It never substitutes machine-wide default cost,
the VPN interface cost, interface names or Wi-Fi/Ethernet type guesses. It also
records the connection GUID and network GUID to detect a profile change even if
the IP address is reused. No SSID, network name or GUID is returned to the UI.

Only the exact `NLM_CONNECTION_COST_UNRESTRICTED` value permits automatic
download. Fixed/variable, over-limit, approaching-limit, roaming and congestion
flags wait for per-candidate consent. Missing capability, ambiguous/mismatched
adapter, malformed/unrecognized flags or failed queries are unknown. COM calls
run on one locked apartment thread; callers have a 350ms query deadline, and a
blocked COM call cannot spawn an unbounded set of workers. A cached cost sample
lasts at most 250ms. The physical-route API admits verified hardware
Ethernet/Wi-Fi and native WWAN (`IF_TYPE_WWANPP` 243 / `IF_TYPE_WWANPP2` 244).
The same hardware, operational/admin/media status, identity, source and route
checks apply to every admitted type; VPN/TUN exclusions remain in force. WWAN
uses its exact adapter's NLM cost, and unknown cost still requires consent.
Code support is verified by route-selection fixtures; this machine has no WWAN
hardware, so real cellular routing/NLM acceptance remains untested. Tethered
Ethernet/Wi-Fi also uses the NLM cost actually reported for that adapter.

`update.DownloadOptions` adds two hooks without changing the Dart/RPC API:

```go
Prepare func(context.Context, string) error // verified mirror -> physical route
RouteValid func(context.Context) error     // independent of metered consent
```

Prepare runs before the cost check and HTTP request. The helper supplies both
hooks and the existing `Network` callback. Route verification runs before reads,
after reads before cache writes, at socket creation and periodically every 250ms
while an HTTP read is blocked. NLM cost is refreshed with the bounded cache above.
A metered/unknown transition without consent cancels the request and retains
partial bytes. Route, source/interface/gateway/metric, connection or network-GUID
changes poison the current attempt and return `UPDATE_WAITING_NETWORK` even when
metered consent exists. The same attempt never silently switches interfaces or
mirrors to escape the pause. A subsequent UI-authorized/bounded retry may select a
fresh route; consent is never stored as permission for a different package.
Polling/native-call latency means this is not a zero-byte transition guarantee.

The route/cost pause error also survives the HTTP dial boundary, so the downloader
cannot misclassify it as a generic transfer failure and silently try another
mirror. No pause, metering observation or retry performs installation, service
maintenance, connection teardown or protection changes. Main/UI owns eligible
automatic check/download scheduling and manual-cancellation behavior.

D36/D37 verification on 2026-10-12:

- TDD negatives first failed for absent route hooks/classifier, then for a
  swallowed dial-time pause error; all pass after implementation. Fixed injections
  cover unrestricted/fixed/variable/expensive/unknown flags, exact adapter match,
  route/connection/network-profile changes, stale-cost rejection, package consent,
  blocked provider deadlines and no package request before route/cost approval.
- Actual local TLS fixtures verify automatic unmetered transfer, mid-transfer
  metered pause and physical-route pause even with consent, with resumable partial
  bytes retained. These fixtures use temporary files and ephemeral manifest keys.
- `go test -count=1 ./cmdupdate ./update` and targeted `go vet` pass.
  `go test -race -count=1 ./cmdupdate ./update` passes with the existing signed
  compiler fixture and read-only NLM probes enabled.
- Both read-only Windows smoke probes passed. The destination-specific probe
  selected and reverified the physical route for numeric `1.1.1.1`, matched its
  adapter GUID to NLM and observed flags `0x1` (unrestricted). It did **not** dial
  that address, resolve a release host or alter a route/interface. The separate
  hardware-adapter probe also matched NLM. No real metered-network switching or
  installed signed-helper/WFP transfer was performed.
- Full `go test -p 1 -tags with_gvisor ./...` passed the updater, entrydns,
  serviceipc, winservice, winprotect and other existing packages, but the concurrent
  new `protocolconfig` package is still blocked. Its first run lacked `Config`,
  `TLS`, `SOCKS5` and other implementation types; after that source appeared, a
  second run failed on missing go.sum entries for its new sing-box protocol
  imports (including sing-anytls, sing-quic, sing-shadowsocks, ws and go-chi/render).
  That package, go.mod and go.sum were not edited by this task. Do not represent
  either run as a full-module pass.
- Euclid's exact adjacent Update TCP443/IPv4 direct rule is present in
  `tunengine/config.go`; packet-level interaction with the bound physical socket
  and installed WFP remains signed-runtime acceptance work.

Repeat the optional, read-only local probes from `Windows/native/core`:

```powershell
$env:BLUEGRASS_NLM_SMOKE = '1'
& 'D:\Program Files\Go\bin\go.exe' test ./cmdupdate -run 'TestReadOnly.*Smoke' -count=1 -v
```

No new release artifact or `native/bin` binary was produced for this change.
The compiled release configuration remains empty and `UPDATE_UNCONFIGURED`;
real feed/key/signature and signed protected-runtime acceptance remain required.

Main/UI owns the install sequence. The actual controller methods now supplied
and wired in main are:

```dart
Future<bool> AppController.prepareForUpdate();
Future<bool> AppController.handoffUpdate();
```

Call `prepareForUpdate` **only after explicit install confirmation**, then call
helper `launch` only when preparation returns true. Preparation must invalidate reconnect races and close the
business gate while preserving TUN/WFP and protection intent. Never implement it
with Disconnect. Return false for unsafe/unknown pause state; true only when the
session is safely paused or authoritatively idle. Download, readiness, later,
dismissal and app restart never call this method. After a verified
`launchRequested` response, call `handoffUpdate`; only a true result authorizes
the platform exit. Do not call Disconnect during this sequence. The inspected
`main.dart` binds these methods to `UpdatePresentation`. UI/main/controller
wiring is outside this task's writes; its runtime acceptance remains separate.

### Authenticated control DNS

Dirac's service APIs are implemented and the helper uses them directly:

```go
client, err := serviceipc.Dial(ctx, installedRoot, trustedPublisher, update.TrustedPublisher)
// Exchange one frame, no Hello and no controller/session lease:
{"id":"<32-lowercase-hex>","command":"ResolveUpdateHost","host":"<compiled-host>"}
// Response: matching id, ok, and addresses; or a safe error.
```

Service authentication is the separate `AuthenticateUpdateHelper` /
`ValidateUpdatePeer` role: exact installed Update executable, same trusted signer,
signed GUI parent, same OS SID/session. The service owns physical DNS and limits
requests to `cmdupdate.TrustedHosts()`. The helper accepts 1-8 public IPv4 answers,
dials only those numeric addresses on TCP443 and preserves the original hostname
for TLS validation. Private/reserved/IP-literal hosts and missing resolver fail
closed. There is no system DNS fallback, helper DNS permission, svchost exemption
or broad networking bypass. `Config.DialContext` is the library hook used for this
fixed trusted transport; it does not bypass manifest/URL/TLS checks.

No compile-time control DNS dependency remains outstanding. Signed-install pipe
authentication, physical DNS routing, WFP enforcement and revocation behavior
under active protection still require external runtime acceptance; no live pipe
or firewall operation was executed by this task.

### Helper and unified-build requirements

`Windows/scripts/build-update.ps1` builds only the real Update command into a
unique `.artifacts/update/` directory, compiles `resource.rc` with windres and
embeds explicit `asInvoker`. The current generated
`cmd/bluegrass-update/resource_windows_amd64.syso` is ready for local Go builds.
The main-owned `build-native.ps1` must regenerate that resource during clean
builds, as it does for Repair; Diagnostics also requires an explicit `asInvoker`
manifest. Do not substitute or rename another executable.

The installer required inventory and native provenance checks now include all
five actual `native/bin` executables: Native, Service, Repair, Update, Diagnostics.
All product PE files need the same trusted signer in production preflight. The
final compile-evidence snapshot must follow the unified GUI/native build. The
earlier 28-file candidate below is historical evidence, not the new helper bundle.

Checkpoint verification on 2026-10-12:

- Focused `go test -count=1 ./update ./cmdupdate ./cmd/bluegrass-update` and
  `go vet` for those packages: pass.
- Unified-source `go test -p 1 -tags with_gvisor ./...`: pass, including all five
  command packages and the current service/control-DNS integration. Targeted
  `go vet ./update ./cmdupdate ./cmd/bluegrass-update ./serviceipc ./winservice`:
  pass. These commands compile/test source, not a signed installed service.
- `go test -race -count=1 ./update ./cmdupdate`: pass, with the real signed Inno
  fixture and independently calculated signer DER SHA256. State tests use only
  temporary directories, DPAPI fixtures and ephemeral manifest keys.
- The new machine-floor test first failed to compile without `prepareApply`,
  then passed for durable-write failure and higher-machine-sequence rejection.
- Dart adapter: all 9 process fixture tests pass; targeted analyze reports no
  issues. Tests cover consent, exact executable arguments, opaque IDs, progress,
  response binding, output bounds, duplicate finals and private-child timeout.
- Real Update binary built with `with_gvisor`:
  `.artifacts/update/62f5ca4f7a0143f28ac1ab3ee6161e68/BlueGrass.Update.exe`, SHA256
  `4a74946663cdbd073f4221c546f6aa9f8d5dc265ddfae1a56ca3e9f2b9fbc804`.
  `verify-update-helper.ps1` extracted its `asInvoker` resource and exercised only
  `--rpc` check: exit 0, explicit `UPDATE_UNCONFIGURED`, no stderr. Its JSON
  evidence includes actual Go command metadata. No apply/UAC/setup was run.
- Installer preflight passes including individual missing Update/Diagnostics
  rejection. Temporary fake bytes appear only in `-ValidateOnly` tests, never in
  a compile-evidence payload.
- Repeated isolated ablations pass: removing Ed25519 verification makes the
  wrong-key test fail; removing SHA256 comparison makes corrupted-package
  readiness fail. Shared source remains unchanged by the ablation script.

## Scope and implementation order

Implements design addendum sections 15-16 and ADR 0013/0036/0037/0044.
Use stdlib Go, Ed25519, a bounded canonical JSON profile, HTTPS host allowlists,
SHA-256, and Windows Authenticode with the installed product's signer certificate.
No signing
keys, certificates, trust-store changes, or automatic installation are created.

1. Test and implement signed manifest parsing, applicability, freshness and rollback.
2. Test and implement resumable bounded download, network policy and revalidation.
3. Implement and negatively test native WinVerifyTrust and publisher verification.
4. Produce machine installer source/build validation and a separate repair artifact.
5. Run package tests, go vet, and installer preflight/compile checks if available.

## Integration contract

Import `bluegrass/native/core/update`.

- `NewChecker(Config) (*Checker, error)` takes one/two embedded release public keys,
  the trusted product signer certificate fingerprint, approved HTTPS hosts, installed version/build,
  architecture, OS version, channel, and installation ID.
- `(*Checker).Check(ctx, manifestURL, Floor) (Result, error)` fetches a bounded
  manifest. `Verify(raw, Floor)` performs the same checks on a cached manifest.
- `Result` contains the verified sequence/digest, minimum-version decision and
  an opaque `Candidate`. Persist the returned floor in protected machine state
  before downloading; never advance the installed version before installation.
- `Download(ctx, candidate, cacheDir, DownloadOptions)` only prepares a verified
  package. Network cost comes from an actual-route callback. Unknown cost waits;
  consent is the exact candidate ID. Cancel the context on user pause/cancel and
  do not automatically restart a user-paused task.
- `Revalidate(ctx, candidate, path)` rechecks hash, size, Authenticode and pinned
  publisher. Call it after restart and immediately before explicit installation.
- These APIs never stop connections, change protection, spawn an installer, or
  prompt for installation. Main owns D-44 banner/badge state and explicit consent.

```go
func TrustedPublisher(path string) (string, error)
func VerifyPublisher(path, expected string) error
```

`TrustedPublisher` calls WinVerifyTrust with Authenticode policy, revocation
checking and MD2/MD4 disabled, then extracts the actual signer certificate from
the verified provider state. The returned identity is the SHA-256 of its DER,
64 lowercase hex characters. It is **not** a display name, subject or SPKI pin.
`VerifyPublisher` compares that identity exactly, in constant time. Native DLLs
load from System32 (kernel32 is a KnownDLL); no shell/PowerShell helper is used.
Revocation retrieval may use Windows networking and may fail offline. The native
WinVerifyTrust call is synchronous and cannot be interrupted by the Go context;
the transfer deadlines are not a hard bound on Windows trust-provider latency.

Main derives `Config.Publisher` from its own installed, trusted signed binary;
there is no default publisher identity and no manifest-supplied publisher trust.
Certificate renewal changes this identity and needs a separately reviewed release
migration; the manifest cannot replace the trusted signer. Service caller checks
must additionally establish the exact KnownFolder ProgramFiles/BlueGrass path,
immutable ACLs, client PID, the adjacent Native broker, its GUI parent and signer.
The broker must reciprocally verify service PID/path, SYSTEM token and signer.
This package checks the file signature, not process identity or ancestry.

`Floor` is caller-persisted state, partitioned by platform/architecture/channel:

- `Sequence` and `ManifestSHA256` advance atomically after accepted verification,
  before downloading, even if no candidate applies. Identical sequence and digest
  may be reused for restart/resumption; lower sequence or a conflicting digest fails.
- `Version`/`Build` are installed high-water marks. They only advance after actual
  successful installation. Version and build must both increase for an update.
  A checker also keeps its in-process high-water mark and invalidates candidates
  when a newer manifest is accepted. Durable restart protection requires main's
  ACL-protected store; the library does not silently create/reset one.
- On verification/fetch/storage error, keep the last valid state. Do not clear a
  minimum-version restriction or accept a new download after a failed floor write.
- `Result.Unsupported` is the UPD-004 decision for blocking **new** connections.
  It never tears down an existing connection. Main enforces the decision.

## Signed manifest schema v1

Envelope has exactly `key_id`, `payload`, `signature` (standard padded base64).
The signed bytes are UTF-8 `BlueGrass update manifest v1\n` + key ID + `\n` +
`CanonicalJSON(payload)`. Key IDs are 1-32 ASCII letters/digits/underscore/hyphen.
Only keys embedded by the trusted application are accepted.

CanonicalJSON is a documented restricted profile, **not general RFC 8785**:
UTF-8 only; reject duplicate keys at every depth, unpaired UTF-16 surrogates,
trailing data, non-integer/negative/exponent numbers and integers above 2^53-1.
Keys sort as Go UTF-8 strings; strings use Go encoding/json escaping (including
HTML escaping and U+2028/U+2029); no insignificant whitespace. For example:
`{"z":[2,true],"a":"<x>"}` becomes
`{"a":"\u003cx\u003e","z":[2,true]}`. Producers can call the exported helper.
Limit: 256 KiB for the complete envelope, depth 24, 16,384 value nodes.

Payload fields (all required): `schema` = 1; positive monotonic `sequence`;
`published` and `expires` as UTC RFC3339 `...Z` without fractional seconds;
`releases` with 1-32 entries. Publication may be at most five minutes ahead;
expiry must be after publication and now, and at most 30 days after publication.

Each release requires every field below (null/missing/unknown fields fail):

| Field | Contract |
| --- | --- |
| `version`, `minimum_version` | Three numeric semver components, optional semver prerelease; minimum cannot exceed release. No build metadata or leading numeric zeros. |
| `build` | Positive monotonic integer; independent of semver; both must exceed installed floors. |
| `channel` | `stable` or `beta`. |
| `platform`, `arch` | Known platforms `windows`, `windows7`, `android`; architectures `amd64`, `arm64`, `arm`, `universal`. This checker only selects main Windows amd64/arm64. |
| `minimum_os` | Three numeric OS components; must not exceed configured current OS. |
| `package_id` | Only for Android and exactly `com.bluegrass.client`; absent/empty for Windows. |
| `urls` | 1-4 absolute HTTPS URLs, exact approved host including optional port; no userinfo, query, fragment or backslash. Every mirror must pass. Redirects forbidden. |
| `sha256`, `size` | 64 lowercase hex; positive size <=512 MiB. |
| `force`, `rollout` | Boolean; percentage integer 0-100. Force does not authorize install or bypass rollout/network/identity policy. |
| `notes`, `published` | Notes <=16 KiB; canonical UTC publication no later than manifest publication. |

Field names are exact and case-sensitive at every object level. Go's normal
case-insensitive JSON field matching is explicitly rejected, including aliases
alongside valid fields. Optional `package_id` also cannot be null.

Exactly one release per platform/architecture/channel is allowed, including
nonmatching entries. This prevents ambiguous minimum-version decisions.
Rollout is SHA-256 of installation ID, interpreted as a full big-endian integer
modulo 100, compared `< rollout`. Literal vector: `test-installation` gives 98.
Common website and app producers must use this same schema and canonicalization.
Generic remote configuration from section 15.1 is not implemented by this package.

## Download and cache behavior

- `Candidate` fields are opaque. Its ID binds all release metadata, URLs, version,
  build and hash. `Package()` returns a copy; unverified candidates cannot download.
- Use an existing absolute, private cache directory with no untrusted writers.
  Filenames are `<candidateID>.part`, `.exe`, `.lock`; no server filename is used.
  Reject links/nonregular files; Go rooted filesystem operations confine writes.
  Windows exclusive delete-on-close cache locks also release on process death.
- One request per mirror per invocation (at most four), no uncontrolled retry
  loop; 15-minute overall and 30-second body-read idle limits. Reinvoke under
  main's bounded retry policy. Requests do not inherit cookies or credentials;
  TLS 1.2+ and system trust; no environment proxy is automatically used.
- Resume requests bind to the same candidate and offset. Validate exact
  Content-Range/length/total, reject unsolicited 206 and content encoding. A 200
  response to Range truncates and restarts. Disk writes never exceed signed size.
  Interrupted partial files remain resumable. Hash/signature failures remove them.
- `Prepare` may establish the verified route for each signed mirror; `RouteValid`
  is checked independently of metered consent and pauses on any route change.
  `Network` must report actual underlying download-route cost promptly, honor its
  context, and be concurrency-safe. Only `NetworkUnmetered` starts automatically.
  Unknown, cellular, metered, callback errors and absent capability wait unless
  `ConsentPackageID` exactly equals the candidate ID. Poll every 250 ms and check
  before reads; changes cancel in-flight HTTP. This is not a zero-byte switch guarantee.
- `ErrWaitingNetwork` permits main to resume after a suitable network change.
  User pause/cancel uses context cancellation and must not be automatically resumed.
  Progress exposes signed total and known remaining bytes. No background execution
  after app exit, automatic installation, service operation or modal prompt is implied.
- Readying and restart reuse both check full size/hash and publisher. Windows holds
  a read handle denying write/delete sharing across hash and signature checks.
  Revalidate again before install; main must stage into an ACL-protected elevated
  location and prevent substitution between validation and process launch.

## Exact service CLI contract (main integration responsibility)

The CLI below was supplied by main during integration. `BlueGrass.Native.exe`
remains the per-user broker. Exit 0 means the operation is durably complete;
nonzero aborts setup. CLI operations must have bounded timeouts. No tokens appear
in arguments. Service/source integration is maintained outside this task's scope.

| Command | Required behavior |
| --- | --- |
| `BlueGrass.Service.exe run` | SCM dispatcher for `BlueGrassService`; LocalSystem, automatic startup; reconcile durable protection before accepting IPC; no automatic business forwarding. |
| `BlueGrass.Service.exe maintenance` | Elevated caller; durably snapshot logical session/protection intent, pause forwarding while preserving WFP. Acknowledge pause before setup stops the service through SCM. Failure retains protection. |
| `BlueGrass.Service.exe install` | Verify its own signed executable at the canonical ProgramFiles/BlueGrass path. Create/update SCM registration with quoted `"...\BlueGrass.Service.exe" run`, auto start and failure recovery. Do not start an existing maintenance session or reset settings/protection. |
| `BlueGrass.Service.exe status` | Machine-readable JSON status. Available for main/diagnostics; not parsed heuristically by setup. |
| `BlueGrass.Service.exe start` | Start the registered service without resuming business forwarding. The installer currently uses native SCM start/status polling instead. |
| `BlueGrass.Service.exe uninstall` | Stop, `tunengine.RepairAdapter` for the exact owned GUID, `winprotect.Repair`, then delete SCM registration. Explicit uninstall only. Cleanup failure returns nonzero so installer retains files/repair tools. Original-user token/proxy cleanup is the installer user phase below. |
| `BlueGrass.Repair.exe` (no arguments) | Independent repair of owned WFP/TUN and current-user proxy; works with service/UI missing. Explicit user repair action only; never invoked during upgrade. |

Setup stops/starts the service through SCM APIs and checks status for up to 30s.
After restart, main must verify health, eligibility and original exit before
allowing business forwarding. SCM RUNNING is not proof of those checks.

The installer must require a separately built `BlueGrass.Repair.exe`. Copying or
renaming the native host is not a valid independent repair implementation.

## Installer build and release gates

Scoped implementation choice: Inno Setup 7.1.0 (x64 compiler). A single template
supports fixed per-machine installation, maintenance gating and explicit uninstall;
it can compile locally with a verified portable compiler. WiX was not installed.
The central ADR remains main's documentation integration responsibility because
this task cannot write `docs/adr`. The template currently targets native x64 and
Windows 10 build 17763 or newer; the release S4 OS/architecture matrix still needs
validation. ARM64 checker support does not assert an ARM64 installer exists.

- Fixed 64-bit ProgramFiles/BlueGrass. Directory-selection UI disabled, previous
  alternate paths ignored, `/DIR` overrides rejected before install. No HKCU
  startup entry or GUI/connection autostart is created.
- Before maintenance and after copying, reject reparse points and set the tree's
  protected owner/group/DACL: Administrators owner/group, SYSTEM and Administrators
  full control, Users read/execute. No Authenticated Users write grant remains.
- Existing service signer must match the release certificate before maintenance.
  Maintenance -> SCM stop -> copy -> signer check -> service `install` -> SCM start.
  Upgrade never invokes uninstall or repair. Errors retain protection and present
  recovery instructions; file locks or missing owned binaries fail closed.
- Start menu has BlueGrass, independent repair and original-user uninstall.
  The pinned `sing-tun` module embeds the amd64 Wintun DLL in
  `internal/wintun/dll_windows_amd64.go` and loads those bytes in memory. No second
  loose `wintun.dll` is required or accepted by this payload inventory. Actual
  driver availability, licensing and TUN operation still require release testing.
  No user data or token directory is included in the installer payload.
- `build-installer.ps1` accepts only an exact allowlisted inventory, rejects
  unexpected files/reparse points, copies a fresh snapshot and verifies its hashes.
  Existing live-test bindings and unknown assets cause rejection. It does not
  mutate the original build bundle. All product executables must share a trusted
  certificate in production preflight; other PE files must have valid signatures.
- `BlueGrass.SetupTrust.exe` is a small, statically built installer helper using
  this package's VerifyPublisher. `BlueGrass.UserCleanup.exe` implements the
  original-user phase described below. Release pipelines must sign both helpers.
  A renamed service/host binary is never substituted for independent repair.
- Production compilation now accepts explicitly pre-signed payload/helpers and an
  existing release certificate selector, verified Microsoft SignTool and approved
  HTTPS timestamp endpoint. The build signs/verifies Setup and Inno's generated
  uninstaller before embedding, and preserves evidence of the signed intermediate
  bytes. It never re-signs vendor DLLs or modifies the pre-signed helpers.
  Setup still unconditionally refuses installation in `-UnsignedCandidate` mode;
  service independently rejects unsigned installation. Merely signing a candidate
  does not remove its install refusal. Actual production signing remains unrun
  because no release signing identity is available locally.
- Uninstall invokes the exact service command and does not recursively erase
  arbitrary ProgramData/user profiles. Current-user token/proxy cleanup has scoped
  source/tests; complete lifecycle acceptance remains pending. Current machine
  state files, stale per-user notification/autostart registration, and cleanup of
  other users' owned stores need explicit lifecycle handling before release; this
  candidate does not claim complete section 16 uninstall acceptance.

### Notification identity for GUI integration

Confirmed against `Windows/app/windows/runner/desktop_notifications.h`:

- AUMID: `com.bluegrass.client`.
- Toast activator CLSID: `{9221CCD9-CFE9-404B-BBB4-CDA9CA8BB484}`.
- Common Start Menu link: `Programs\BlueGrass.lnk`, target exactly the installed
  `BlueGrass.exe`, with both AppUserModelID and ToastActivatorCLSID properties.
- HKLM Classes CLSID LocalServer32: `"<ProgramFiles>\BlueGrass\BlueGrass.exe" --toast-activated`.
  The machine AppUserModelId registration uses the same CLSID.
- The UI's per-user registration must preserve the same identity. Update-ready
  notices remain in-app banner/badge state under ADR 0044; this registration does
  not authorize a system toast or modal installer prompt for update readiness.

### Original-user uninstall phase

Inno cannot use `ExecAsOriginalUser` during uninstallation. Therefore ARP and the
Uninstall shortcut launch the installed `BlueGrass.UserCleanup.exe --uninstall`
with an `asInvoker` manifest. This helper refuses an elevated token, checks its
fixed ProgramFiles path, verifies its own signer and the same signer on
`unins000.exe`, and requires the current user's GUI/broker to be closed.

After explicit confirmation, it deletes only `session_v1.dpapi` and its `.tmp`
record under KnownFolder LocalAppData/BlueGrass/client. A separate choice also
removes direct `.dpapi`/`.dpapi.tmp` preference/device records. Directory traversal,
linked records and recursive deletion are refused. It never enumerates another
user's profile. Proxy restoration uses the actual `winproxy` ownership journal and
exclusive `proxy.lock`; ownership lost to another application relinquishes the
obsolete journal rather than overwriting that application's settings.

The wrapper then uses native ShellExecute `runas` for the signed uninstaller with
`/USERPHASE=complete`. The uninstaller refuses entry without that marker and only
then invokes signed service `uninstall`. The marker coordinates this workflow; it
is not authenticated proof of cleanup or an all-user cleanup mechanism. The
wrapper does not wait for machine uninstall success. Cancellation of UAC or later
machine failure leaves the explicitly approved user cleanup performed. The
original-user/admin transition and concurrent restart races require signed runtime
acceptance; no helper was executed against a real user profile during this task.

From repository root, PowerShell 7:

```powershell
./Windows/installer/prepare-compiler.ps1
./Windows/installer/test-build-installer.ps1
./Windows/installer/ablate-update.ps1
# Snapshot allowlisted real GUI/native build outputs; compile only, never install:
./Windows/installer/compile-actual-bundle.ps1 -CompileEvidenceOnly -Version 0.1.0-beta.1 -Build 1
./Windows/installer/verify-actual-candidate.ps1 -ArtifactDirectory <returned-output-directory>
# Real bundle preflight (no compile/install):
./Windows/scripts/build-installer.ps1 -Version 0.1.0 -Build 1 -ValidateOnly
# Candidate with a clean, complete bundle (still cannot install):
./Windows/scripts/build-installer.ps1 -Bundle <absolute-clean-bundle> -Version 0.1.0 -Build 1 -UnsignedCandidate
```

### Production signing entry (pre-signed payload workflow)

The unconditional production-mode throw has been removed. `build-installer.ps1`
accepts `-CertificateThumbprint`, `-CertificateStore CurrentUser|LocalMachine`
(default CurrentUser), `-SignTool` and `-TimestampUrl`. These are build inputs,
not runtime publisher configuration. The certificate thumbprint is the exact
40-hex SHA1 store selector; runtime identity remains certificate DER SHA256.
There is no PFX/password input, key generation/import/export or trust-store write.

Prepare a clean allowlisted bundle first. **Every BlueGrass executable, including
SetupTrust and UserCleanup, must already be signed and timestamped with this same
existing product certificate.** Compile and embed all PE manifests before signing.
Production mode preserves these bytes; only candidate mode rebuilds the two
installer helpers. Third-party PE files require a valid trusted signature and
retain their original publisher. Microsoft/vendor payloads are never re-signed by
this script. The separate Repair output is copied from the signed repair input.

PowerShell 7 example, with values supplied by release engineering:

```powershell
$release = @{
  Bundle = $cleanSignedBundle
  Version = $releaseVersion
  Build = $releaseBuild
  CertificateThumbprint = $existingCodeSigningThumbprint
  CertificateStore = 'CurrentUser'
  SignTool = 'C:\Program Files (x86)\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe'
  TimestampUrl = $approvedHttpsRfc3161Endpoint
}
# Read-only artifact/certificate preflight; does not sign or compile:
./Windows/scripts/build-installer.ps1 @release -ValidateOnly
# Builds, signs and verifies Setup/uninstaller; never installs or publishes:
./Windows/scripts/build-installer.ps1 @release
```

Before signing, the script opens only the selected existing `My` store read-only,
requires exactly one match with `HasPrivateKey`, current validity and explicit
Code Signing EKU, then validates its chain with online revocation and code-signing
application policy. Revocation/chain failure stops the build; offline acceptance
is not silently substituted. The selected cert DER fingerprint must match every
product executable. The absolute local `signtool.exe` must have a valid Microsoft
signature. Timestamp URLs must be plain HTTPS endpoints with no credentials,
query, fragment or command/Inno macros; there is no invented default endpoint.

Inno receives an explicit `bluegrass` signing callback; IDE sign tools are
disabled. `SignedUninstaller=yes` signs uninstall and temporary setup self-copies.
The callback directly invokes SignTool with `/sha1`, `/s My`, optional `/sm`,
`/fd SHA256 /tr <endpoint> /td SHA256` using a native argument array from the
PowerShell callback; no `cmd /c`, expression evaluation, password argument or
automatic certificate selection is used. It checks `signtool verify /pa /all /tw /sha1` and
Windows Authenticode status, timestamp presence and exact certificate DER SHA256.
Warnings/nonzero exit, missing timestamp or signer mismatch fail the build.
Candidate mode rejects signing arguments and still uses `--no-signing`.

All signing targets must be within the unique artifact directory, with no
reparse traversal or alternate data streams. Inno temporary output is confined
there. Signed uninstall/self-copy files are verified **before** Inno embeds them;
the callback records exact signed byte copies and JSON receipts. Final Setup must
match a verified receipt by SHA256, and a verified uninstaller receipt must exist.
Setup, all intermediate snapshots and independent Repair are checked again after
compilation. Successful signing sets `SigningVerified=true` and
`InstallDisabled=false`; `Distributable=false` / `ReleaseAcceptanceRequired=true`
remain explicit until the separate Beta runtime acceptance is complete.

Signing tests added on 2026-10-12:

- `installer/test-release-signing.ps1`: pass. Rejects candidate/signing mixing,
  partial/malformed selectors, unsafe timestamp URLs and PATH tool lookup;
  checks the absent cert and missing private-key gates, exact SHA256/RFC3161
  argument boundaries, literal-dollar callback escaping and target confinement.
  Positive timestamped verification uses the existing official signed Inno
  fixture, also copied to an Inno-style `uninst.e32.tmp`; no key is used.
  Wrong publisher/unsigned files fail. Real Inno preprocessing confirms separate
  signed production and install-disabled candidate branches.
- TDD first failed for missing signing parameters, then exposed Inno's command-line
  `"0"` string being truthy under a direct `#if`; explicit `Int(UnsignedCandidate)`
  fixed the actual branch selection before the tests passed.
- `installer/test-signing-compiler.ps1 -ActualArtifactDirectory <actual-snapshot>`:
  pass using the previous actual-binary snapshot (not placeholder payload).
  The real production compiler generated `signed-uninstaller/uninst.e32.tmp`,
  invoked the real callback within its isolated directory, reached the deliberately
  absent certificate gate and aborted before any signing operation. Evidence:
  `.artifacts/signing-compiler-rejection/465511929dab432ba1e4bdf2ab460880/`.
  This is expected-failure evidence, not a release or final unified-build package.
- Existing installer payload preflight regression tests: pass. Both local My
  stores still have zero code-signing certificates. No real signature creation,
  timestamp request, install, trust-store modification or publication was run.

Actual private-key provider access, RFC3161 server success and final signed
installer/uninstaller execution remain untested until the real release identity
and signed payload are provided. The build entry is implemented; those are
environment/acceptance dependencies rather than another required code path.

Compiler source: official GitHub `jrsoftware/issrc`, tag `is-7_1_0`, asset
`innosetup-7.1.0-x64.exe`; SHA-256
`0362a383ed217d4c4239b5933866dd96d3eb2102737da92f80f6057a4b40df2f`.
Both archive and ISCC Authenticode were valid. Preparation uses `/PORTABLE=1
/CURRENTUSER` and disables associations/tasks/shortcuts; no global tool install,
uninstall registration or trust-store write. The official API asset endpoint was
needed after direct GitHub download failed on this workstation.

## Verification evidence and remaining blockers

Initial read-only inspection on 2026-10-12: Go 1.27.2 at
`D:\Program Files\Go\bin\go.exe`; no Inno Setup/WiX command or installation found
in common C:/D: program directories or uninstall registry; zero code-signing
certificates in CurrentUser/My and LocalMachine/My (metadata inspection only).
The existing Flutter Release bundle contains `BlueGrass.test-binding.json`; its
contents were not read or packaged. Direct production preflight rejects this dirty
bundle. The explicit compile-evidence script copies only allowlisted real files
and replaces the three native entries from `native/bin`. It does not claim that
filtering this older GUI build makes it a production build. A fresh integrated
production-entry build is required. Final read-only inspection again found zero
code-signing certificates and no current elevated token.

TDD red runs recorded undefined update API, then behavior regressions for a lost
installed floor, an incorrect minimum-version result and ambiguous target entries.
Final strict-schema regression tests also first failed on `SCHEMA`, `VERSION`,
`PACKAGE_ID`, envelope `KEY_ID` aliases and optional null, then passed after exact
field-name validation was added.
They were followed by passing update tests. Tests use temporary files, TLS fixture
servers and ephemeral Ed25519 keys only. No signing identity was generated.

Confirmed on 2026-10-12:

- `go test ./update`: pass; `go vet ./update`: pass after correcting native-memory
  access reported by vet. Includes canonical/schema/applicability/rollback,
  truncated resumable HTTP, wrong ranges/encoding/redirects/size/hash/publisher,
  metered/unknown/cellular consent, mid-request cancellation and cache tampering.
- `go test -race -count=1 ./update`: pass with the signed fixture enabled. Includes
  simultaneous-write/delete rejection during file verification, exclusive cache
  locking, and hash + signature verification on the same handle.
- `go test -p 1 ./...`: pass for the native module, including current service and
  repair entry points. An earlier parallel run had Windows executable file-lock
  errors and two out-of-scope tunengine repair-test failures; both repair tests
  passed in the subsequent/current shared-source runs. No repair code was edited
  in this task. `go vet ../../installer/trustcheck/main.go`: pass. File-list `go test`
  and `go vet` for the usercleanup helper: pass (only temporary records/fake proxy
  boundary; no real token/proxy cleanup).
- Native WinVerifyTrust: unsigned/missing files rejected; official signed compiler
  copied to a temporary fixture verified; returned DER fingerprint matched
  PowerShell's independent Authenticode certificate; wrong fingerprint rejected.
- Installer behavioral preflight tests: pass for absent required files, test/private
  files, unexpected data files, unsigned production input and malformed versions.
- Inno 7.1.0 compiled the complete template/Pascal code with **actual binary
  payloads**, including freshly compiled SetupTrust/UserCleanup helpers. No setup,
  service, repair or usercleanup executable was run. Final output is
  `Windows/.artifacts/installers/0.1.0-beta.1-1-decb75fbf89d477e82a01f1eb05336e3/`.
  Installer: `BlueGrass-0.1.0-beta.1-Windows-x64-unsigned-candidate.exe`;
  SHA-256 `f9592c79a801bbf61e2633cdd5ecf02033a3679e2998b23780832af9bb0c0033`.
  Independent `BlueGrass-Repair-unsigned-candidate.exe` SHA-256:
  `fa17c7457c93aa1c488d47bb1624867c6d30a445a9604269d459a40711043249`.
  The repair file is byte-identical to the actual `native/bin/BlueGrass.Repair.exe`.
- `verify-actual-candidate.ps1`: pass. All **28** payload sizes/hashes matched;
  all three native entries matched actual build files. SDK `mt.exe` extracted and
  verified Repair `requireAdministrator` and UserCleanup `asInvoker` manifests
  without executing either file. `verification.json` records native Go build
  metadata; `actual-build-origins.json` records absolute inputs/timestamps/hashes;
  `payload-inventory.json` records every packaged file. No placeholder payloads
  are used in this final candidate. Temporary preflight tests still use harmless
  fixture bytes, exclusively with `-ValidateOnly`.
- Isolated ablation baseline: pass. Removing Ed25519 verification from a temporary
  package copy made `TestValidEncodingWrongManifestSignatureRejected` fail with
  `wrong signing key accepted`; removing the SHA-256 comparison separately made
  `TestDownloadRejectsBadResponsesAndPublisher/hash` fail with
  `invalid artifact became ready`. The script requires these exact failure
  assertions, restores/removes its own temporary copy, and never changes shared
  production source. Both mutants were detected on the final run.

Earlier structural fixture and superseded candidate directories are retained
locally. Only the final directory and hashes above are current evidence; old
`0.0.0-beta.0` outputs contain dummy fixture bytes and must never be distributed.

Snapshot freshness is intentionally explicit: GUI `BlueGrass.exe` was last built
2026-10-11 20:50:30 +08; actual Native/Service/Repair inputs were built on
2026-10-12 at 13:10:49, 13:11:03 and 13:11:06 +08 respectively. Native Go build
metadata has `CGO_ENABLED=0`, `GOARCH=amd64`, `GOOS=windows` and no `with_gvisor`
tag. Main has since added `-tags with_gvisor` to `build-native.ps1`; the next
integrated rebuild and fresh candidate snapshot must include that tag. This task
did not edit or invoke the shared build script or overwrite `native/bin`.

The signed fixture probe is opt-in through `BLUEGRASS_SIGNED_FIXTURE` and
`BLUEGRASS_SIGNED_FIXTURE_CERT_SHA256` (independently obtained certificate DER
digest). It copies the source to a temporary fixture; normal test runs without
these variables report that one probe as skipped. No private key is needed.

Beta remains blocked on real signing/release identities and approved manifest
hosting/keys; integrated automatic scheduling and actual metered-transition acceptance;
a fresh integrated GUI/native build with `with_gvisor` and complete
signed payloads; actual execution of the implemented signing pipeline; full owned
machine/user data and registration cleanup; elevation, ACL,
install/upgrade/uninstall/failure recovery tests on the S4 Windows matrix.
No current elevated token is available.
The helper now supplies per-user and independent machine floors, and main/UI has
implemented its adapter integration. Signed UI ready-banner/consent behavior,
reboot persistence, real metered transitions and preservation/revalidation of
protected sessions still require main/platform acceptance.

No local source check or successful compile is represented as production runtime
validation. No changes were made to main app/controller/runner/nativehost,
build.ps1, go.mod, signing stores, real network configuration or remote deployment.

## Offline startup minimum-version checkpoint (2026-10-12)

The helper now exposes `startup_policy` through the same exact adjacent
`BlueGrass.Update.exe --rpc` process boundary. Request fields are exactly `v`,
`id`, `op`; even false consent or empty candidate fields are rejected. A successful
reply contains `startupPolicy`, never `snapshot` or progress. It grants no package,
candidate, download, launch, service or connection authority.

The existing Dart adapter adds:

```dart
Future<UpdateStartupPolicy> readStartupPolicy();
enum UpdateStartupPolicyState { noHistory, verified }
// UpdateStartupPolicy: state, minimumVersion, unsupported, manifestExpired.
// Both UpdateClient constructors: startupPolicyTimeout = Duration(seconds: 8).
```

`noHistory` denotes a missing per-user persisted record, not an empty/corrupt
record. `verified` rechecks the full bounded canonical manifest schema, embedded
Ed25519 key/signature and exact nonzero persisted sequence + payload digest. Only
expiry is relaxed for this restriction-only read; publication time, validity
duration, package metadata, HTTPS host allowlist and in-process rollback checks
remain enforced. Minimum-version policy is independent of rollout and candidate
OS eligibility. Normal check/download/launch still reject expired manifests.
The policy read does not advance a floor, write state, inspect cached packages or
hand off an installer. It cannot distinguish a deliberately deleted user cache
from a first run; it does not claim hardware-backed monotonic persistence.

DPAPI/schema/signature/floor errors return `UPDATE_STATE_INVALID`. Missing compiled
release configuration returns `UPDATE_UNCONFIGURED` before installation/state
access. Installation trust failures return `UPDATE_TRUST_FAILED`; busy, timeout
and cancellation remain explicit. UI/main owns startup ordering and must not
reinterpret a verification failure as an authenticated permission to connect.

The offline Windows opening path retains fixed ProgramFiles location, pinned
files, immutable ACL checks, unelevated-token policy and helper/GUI same signer.
It does not initialize NLM, service DNS or network routing. New
`update.TrustedPublisherOffline(path string) (string, error)` uses
`WTD_CACHE_ONLY_URL_RETRIEVAL` (0x1000, checked against installed SDK WinTrust.h)
in addition to the existing revocation and weak-digest rejection flags. Missing
cached trust fails without online fallback. Existing `TrustedPublisher` and
`VerifyPublisher` semantics used by the service are unchanged.

Current source verification (Go 1.27.2; temporary fixtures/ephemeral keys only):

- `go test -mod=readonly -tags with_gvisor -race ./update ./cmdupdate -count=1`:
  passed. Includes expired history after real per-user DPAPI save/reopen, signature
  and exact-floor rejection, malformed nested floor schema, unconfigured RPC,
  action-field rejection and unsigned offline Authenticode rejection.
- `go test -mod=readonly -tags with_gvisor -race ./tunengine -run TestLocalOnly
  -count=1 -v`: all 14 independent local-only runtime tests passed.
- `go test -mod=readonly -tags with_gvisor ./tunengine ./cmd/bluegrass-update
  ./winservice -count=1`: passed; Update command compiled and has no direct tests.
- `go vet -mod=readonly -tags with_gvisor ./update ./cmdupdate ./tunengine
  ./cmd/bluegrass-update`: passed.
- Dart `test/update_client_test.dart`: 22 passed, including subprocess policy
  response validation, expired restriction, corrupt/absent/config distinctions,
  mixed authority/progress rejection, timeout, busy and cancellation. Analysis of
  the adapter, tests and subprocess fixture reports no issues.
- Isolated signature/hash ablations still detect both missing checks. Production
  source is untouched; deliberately failing mutants return nonzero by design.

Logs are under `Windows/.artifacts/verification/update-startup-*` and
`localonly-final-race.log`. No installer/native-bin snapshot was rebuilt in this
checkpoint. The historical candidate above remains unsigned, stale relative to
these sources, install-disabled, and not a distributable Beta. A real configured,
signed installation, cached-chain availability, elevation/upgrade and actual
TUN/WFP runtime behavior still need the main task's platform acceptance.
