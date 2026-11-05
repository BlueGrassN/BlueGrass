# macOS system proxy backend

The development build uses the existing pinned Go/Xray engine, private nativehost
JSON-lines protocol, and HTTP listener `127.0.0.1:12808`. Launch
`Contents/MacOS/BlueGrass.Native --system-proxy` with private stdin/stdout pipes.
`BlueGrass.ProxySettings` must be an executable regular file beside that host in
`Contents/MacOS`; the bundle also contains `BlueGrass.Diagnostics` as specified by
the desktop build contract. The helper is not a daemon, privileged executable,
shell, or publicly reachable control service. It uses public SystemConfiguration,
Authorization Services, and ordinary login Keychain APIs. No Apple Developer
certificate, private entitlement, Network Extension, or root command is used.

## Build contract

Run on the native architecture's macOS runner, from the repository root:

```sh
swift test --package-path Mac/native/ProxySettings
swift build --package-path Mac/native/ProxySettings -c release --arch arm64
swift build --package-path Mac/native/ProxySettings -c release --arch arm64 --show-bin-path
```

The product is named **`BlueGrass.ProxySettings`**. Copy that exact file from the
reported directory into `BlueGrass.app/Contents/MacOS/BlueGrass.ProxySettings`.
Use `x86_64` instead of `arm64` on the Intel runner. The package requires macOS 13+
and Swift tools 5.9+. CI's `macos-15`/arm64 and `macos-15-intel`/x86_64 matrix may
build natively; there are no third-party Swift dependencies. Apply the same ad hoc
development signing policy to this executable as to the rest of the bundle. Keep
the helper executable and not writable by other users (normal `0755` mode).

From `Windows/native/core`, with Go 1.27.2 and the existing go.mod/go.sum pins:

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o BlueGrass.Native ./cmd/bluegrass-native
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o BlueGrass.Native ./cmd/bluegrass-native
```

The existing command accepts `--system-proxy`; no new native-host CLI option is
required. `nativehost.RepairCurrentUserProxy()` is also implemented on Darwin.
The assigned scope does not add a separate repair executable or command flag.
An existing live host can perform explicit repair through `Disconnect` or
`ReleaseProtection`; those commands use the same manager. A new system-proxy host
recovers an interrupted transaction before opening its local listener.

## Ownership, apply, and restore

The Go host holds nonblocking `flock` on
`~/Library/Application Support/BlueGrass/network/proxy.lock` throughout recovery,
IPC, and disconnect/EOF cleanup. Its private directories must belong to the
current user and have mode `0700`; lock files are regular, singly linked `0600`
files opened with `O_NOFOLLOW` and `O_CLOEXEC`. Running as root is rejected. The
Swift child holds a separate `proxy-settings.lock` so a lingering child cannot
race a replacement host's journal operations. Kernel locks release on process
exit. These are per-user locks; macOS network preferences themselves are system
settings. Fast user switching is not an accepted multi-user isolation guarantee.

1. Read all existing proxy protocols and the enabled services in the current
   network location. Record the selected service IDs and the relevant proxy keys.
   Refuse application when no eligible proxy service is available.
2. Before changing any preferences, durably save a versioned `prepared` journal
   in the current user's ordinary login Keychain. Its service is
   `org.bluegrass.proxy-settings.recovery.v1`, account `uid:<numeric uid>`.
   Original settings, including potentially private PAC URLs, never go into a
   plaintext recovery file.
3. Request the `system.preferences.network` Authorization Services right when a
   write is needed. The user may see macOS authorization and Keychain dialogs.
   Cancellation or failure aborts the connection. Authorization stays in the
   helper's memory for its process lifetime; no password or externalized
   authorization is passed from the app.
4. Acquire `SCPreferencesLock(..., false)`, re-read and compare the complete
   observed proxy snapshot, and stage only changed proxy keys. Commit/apply the
   preferences and read them back before marking the journal `applied`.
5. Enable HTTP and HTTPS proxies at `127.0.0.1:12808`, disable PAC, autodiscovery,
   and system SOCKS selection, and translate the shared bypass list into
   `ExceptionsList` and `ExcludeSimpleHostnames`. Keep PAC URLs, authentication,
   unrelated protocol values, service enablement, and service selection intact.
6. If applying settings, verifying them, or committing the applied journal fails,
   immediately attempt the same ownership-checked restoration used by recovery.
   Never delete a backup when restoration, notification, verification, or journal
   deletion fails. A transport timeout terminates the helper and leaves the
   durable journal for the next recovery attempt.

Restoration uses coherent ownership groups: HTTP host/port/enable, HTTPS
host/port/enable, SOCKS host/port/enable, PAC enable/URL, autodiscovery, and bypass.
A group still matching its applied snapshot returns to the original values,
including removing originally absent keys. Already restored groups are left
alone. An external HTTP replacement therefore preserves that endpoint and its
enable flag while independently owned HTTPS is restored. A bypass-only change
preserves the external bypass group while restoring both owned endpoints.

Selection modes also interact across groups. When a manual group differs from
both the original and applied snapshots, restoring old PAC/autodiscovery/SOCKS
selection could override the external endpoint. Those selection groups remain
unchanged in that case: an external manual proxy never reactivates obsolete PAC.
After readback, a separate endpoint check refuses journal deletion if an
unresolved HTTP/HTTPS host+port still matches the BlueGrass listener (unless its
whole group is already exactly original). Restore and Recover return
`RECOVERY_REQUIRED`, preserve the journal, and restore any other independently
owned endpoints. Relinquish has the same guard, even if an external enable value
made full-group ownership ambiguous. Disconnect, Close, and repair cannot
acknowledge this state as successfully cleaned up. Once the endpoint is restored
or replaced externally, recovery can safely finish and relinquish the record.
The preferences lock protects compare-and-set from cooperating settings writers;
a stale comparison is rejected before any write. Unrelated proxy fields are
copied from the current native configuration while staging changes.

Ownership loss maps to the existing `winproxy.ErrOwnershipLost`. The host's
existing repair/relinquish path can acknowledge that external settings were
preserved. A backup cannot be relinquished while an unresolved BlueGrass endpoint
remains, or another changed group still requires restoration (excluding selection
groups deliberately preserved to protect external manual policy). Identical values written by another process cannot be
distinguished from BlueGrass ownership, the same equality convention used by the
Windows manager. Corrupt or unreadable journals require recovery; they never
establish an applied/connected claim.

## IPC, failures, and capability limits

The helper accepts only `read`, `load`, `save`, `delete`, and proxy-only
compare-and-set (`cas`) requests on inherited pipes. No executable paths,
arbitrary preference domains, commands, node credentials, or account tokens are
accepted. Request and response frames are bounded to 1 MiB and journal data to
512 KiB. The whole Go Manager transaction has one monotonic deadline, covering
mutex waiting, every helper call, verification, rollback, and helper teardown:

| Transaction | Total budget | Native work before teardown reserve |
| --- | --- | --- |
| Apply / startup Recover | 25 seconds | 24 seconds |
| State | 30 seconds | 29 seconds, each frame capped at 24 seconds |
| Restore / Relinquish | 3 seconds | 2 seconds |

One second is reserved for SIGKILL and reaping the exact helper child. A timed-out
RPC closes the dedicated cancellation pipe, kills the child, and waits for its
exit within that reserve before returning the recovery error. Successive RPCs
receive only the remaining transaction time; they cannot renew it. State has a
separate budget so polling can wait behind Apply's interactive authorization
without marking a legitimate connection as requiring recovery. A regression
holds Apply beyond three seconds and verifies that State waits and then succeeds.

Use the lead's **90-second Mac NativeClient request timeout**, retaining the
5-second shutdown timeout and Windows's 45-second request policy. The 90-second
allowance covers cumulative host/controller and network work as well as Manager
operations; it is not a renewed budget for each helper request. Cleanup may be
interrupted if an engine operation still owns the host when the app's shutdown
deadline expires; this retains the recovery journal and makes no restoration
claim. Dart policy is outside this backend's write scope.

Stdout contains JSON envelopes only; stderr is discarded. Only fixed existing
safe codes cross the helper boundary. No underlying OS error strings or sensitive
settings are logged. The minimal child environment contains a fixed PATH, the
validated private lock directory, and its expected parent PID; no application
credentials are inherited.

The helper receives a dedicated cancellation pipe on inherited FD 3. Only the Go
host owns its writer, marked CLOEXEC for other children; the kernel closes it on
unexpected host exit, independently of Go deferred cleanup. Swift installs a pipe
EOF source, a parent process-exit source, and a 50 ms monotonic watchdog on an
independent queue **before** initializing authorization or processing requests.
EOF, parent exit, or deadline expiry latches cancellation and calls `_exit(1)`,
including while the protocol thread is blocked in Authorization or Keychain.
Synchronous parent/deadline checks cover source-registration races and run after
every Authorization/Keychain wait, before staging preferences, immediately before
CommitChanges, and again before ApplyChanges. A late authorization result cannot
begin a new preferences mutation after cancellation or host death. If a commit
was already in progress at interruption, the durable journal remains for recovery.

The shared nativehost closes the local forwarding gate when proxy application
fails, and pauses forwarding when a status check detects lost ownership. Status
checks also detect changes to the enabled service set. There is no background
network observer or proxy watchdog in this backend: the app should keep polling
the existing `GetStatus` command, and the first such check detects a changed
network location or competing proxy writer. Reconnect after repair/disconnect.

Pause retains the proxy setting while closing the local forwarding gate.
Disconnect, release, and orderly IPC EOF restore owned settings. Forced
termination may leave the journal and preferences behind until the next host
startup or repair; there is no persistent restoration daemon. If the Keychain is
locked, access is denied, the helper is missing, or authorization times out,
connection application fails and recovery information is retained where possible.
An ad hoc signed replacement may require renewed Keychain consent.

System proxy preferences affect applications that honor those settings. This is
**not TUN, a VPN, a firewall kill switch, or persistent VPN protection**. The
Darwin host never advertises `tun` or `persistent_protection`. Readback confirms
stored preferences and the framework's apply acknowledgement; it does not prove
every application follows them. Managed network profiles, cross-user sessions,
GUI authorization behavior, sleep/wake, and actual traffic/restoration still
require Mac device acceptance.

## Verification

On Windows, `go test ./macproxy ./nativehost ./winproxy` exercises ownership,
competing fields and read/write races, partial writes, journal commit and rollback
failures, restart recovery, malformed records/IPC, bounded helper failure, and the
existing fail-closed engine gate. `go test -race ./macproxy` and `go vet ./macproxy`
also run there. Darwin `nativehost` tests cover filesystem locks/symlink rejection
and capability exclusion; cross-compile them with `go test -c ./nativehost`, then
run them on macOS. They do not write native proxy preferences.

The Swift tests exercise pure proxy patch policy, removal of originally absent
keys, stale snapshot rejection, strict command parsing, and safe error envelopes.
Additional tests cover late authorization after parent death, late Keychain after
deadline expiry, latched cancellation, cancellation-pipe EOF while authorization
is blocked, and watchdog termination. Darwin Go transport tests launch a real
test helper subprocess and verify that a timeout kills and reaps it before
returning. These process tests use temporary files and pipes, never native proxy
preferences.
**Swift tests never instantiate the real preferences or Keychain adapters**, ask
for authorization, or modify the CI runner's network. Swift is unavailable on the
Windows implementation machine; compile/test results must come from the macOS
matrix before claiming native acceptance.

After the main agent restored `Windows/contracts/`, the complete CI Go target
set passed on Windows:

```sh
go test ./nativehost ./macproxy ./localengine ./protocolconfig ./routingpolicy ./control ./entrydns ./diagnostics
```

The full Windows Go suite was also attempted before that restoration. Its six
missing-fixture control failures (`TestSharedControlRequests`,
`TestSharedInvalidMessages`, `TestSharedEligibility`, `TestSharedMonotonicTime`,
`TestSharedConnectionGenerations`, `TestSharedCounterValidation`) are resolved by
the restored fixtures and the passing command above. The unrelated
`tunengine.TestWintunApprovedDriverMatchesRuntimePin` reported
`WINTUN_PATH_INVALID`; that target is outside the macOS CI selection.

API references: [SCPreferences](https://developer.apple.com/documentation/systemconfiguration/scpreferences-ft8),
[SCPreferencesLock](https://developer.apple.com/documentation/systemconfiguration/scpreferenceslock(_:_:)),
and [SCNetworkServiceGetServiceID](https://developer.apple.com/documentation/systemconfiguration/scnetworkservicegetserviceid(_:)).
