# Administrator-authorized TUN development build

This development path uses macOS's system administrator-password dialog and
Darwin utun. No Apple signing certificate is required. The password is handled
by the system, never by Flutter, the broker, JSON IPC, logs, or application
storage. A development signature may be applied before computing the worker
hash. This does not install a LaunchDaemon or a login/startup service.

## Packaging contract

1. Build `cmd/bluegrass-tunnel` for the target architecture as
   `BlueGrass.Tunnel`, and apply its final development signature first.
2. Calculate SHA-256 of those exact signed bytes.
3. Build `cmd/bluegrass-native` as `BlueGrass.Native` with:

   ```text
   -ldflags "-X bluegrass/native/core/mactunipc.ExpectedWorkerSHA256=<lowercase-sha256>"
   ```

4. Put `BlueGrass.Tunnel` beside `BlueGrass.Native` in `Contents/MacOS`. Sign
   the remaining application without modifying or re-signing the worker.
5. Run `BlueGrass.Native --tun-worker-info`. Its entire stdout is the following
   JSON object followed by one newline (an uninjected build prints an empty
   hash):

   ```json
   {"expectedWorkerSHA256":"<lowercase-sha256>","protocolVersion":1}
   ```

The info option is print-only: it does not read configuration, start a host,
request authorization, install anything, or touch system networking. Verify
its hash against the packaged worker after all signing and packaging steps.
An empty or malformed embedded digest disables TUN authorization.

## Installation and process lifetime

`BlueGrass.Native --tun` runs a fixed AppleScript using `/usr/bin/osascript` and
`do shell script ... with administrator privileges`. Shell syntax and commands
are fixed; the bundled absolute path is shell-quoted and process identifiers
are validated unsigned decimal arguments. Profiles and node credentials never
appear in argv. Only the system displays or receives the password.

The root shell checks directory ownership, modes, symlinks and ACL absence, verifies the
source hash, copies into a root-owned temporary file, verifies its hash, and
installs it with mode `0700` at:

```text
/Library/PrivilegedHelperTools/BlueGrass.Tunnel-<sha256>
```

It verifies the final file again before executing it. Existing unsafe or
incorrectly hashed destinations are rejected. The installed worker also checks
its own content-addressed path, root ownership and digest before opening IPC.
The installer rejects ACLs before copying and again before execution using fixed
`/bin/ls -ldne` and a streaming, bounded-line filter. A success record is accepted
only if ls completed successfully; both the permission suffix and additional ACL
rows are checked, including ACLs obscured by an xattr `@` suffix. The Go boundary
walks root directories top down with `openat(O_NOFOLLOW)`, checks each descriptor's
mode/owner and bounded `fgetattrlist(ATTR_CMN_EXTENDED_SECURITY)` result, and fails
closed on ACLs, malformed/unsupported metadata or ignored filesystem ownership.
Regular files and the socket node are also checked. Protected paths with even
restrictive nonempty ACLs are rejected; no existing ACL is silently repaired.
The only worker options are the numeric authorized UID, PID and kernel birth
time. There is no command, configuration, profile, state-path or socket-path
option. Installation retains only the executable and recovery metadata.

The worker acquires an exclusive root-owned lock, accepts one authorized
broker, then exits after that session. The listener allows connection attempts
but authorization uses Darwin `LOCAL_PEERCRED`, `LOCAL_PEERPID` and
`kern.proc.pid` process start seconds/microseconds, not JSON identity claims.
The parent and socket remain root-owned; a different user, an unrelated process
under the same user, and a reused PID cannot take the session. The broker also
checks the socket's root ownership, protected parents, root peer and fixed
protocol-ready frame.

An unconnected late launch expires after five seconds and never invokes the
privileged host. Broker death is checked before activation and every 100 ms
during the session. GUI EOF cancels pending authorization and closes active
IPC. The dialog has a 70-second AppleScript limit and a 75-second process limit
(including installation and the ready handshake), inside the GUI's 120-second
startup budget. Broker authorization plus the initial Hello/recovery probe has an
115-second outer limit, leaving shutdown time before the GUI's 120-second Hello
deadline. Frames are length-prefixed little-endian JSON objects, limited to 64 KiB,
with strict duplicate-key/nesting validation. Each operation has one shared
100-second request/reply deadline, including reply delivery; it is never reset
after a slow Prepare. The 30-second idle timeout resumes after a reply, so quiet
sessions still need periodic status requests. The server watches EOF throughout
operations; a 60-second Prepare does not trigger the idle timer.

EOF, cancellation, transport failure and invalid frames close the privileged
host input. The host composition must promptly close its engine on stream
termination, including operations in flight. Its manager closes forwarding and
retains PF protection and the recovery journal. IPC never releases PF or deletes
the journal. If host cleanup is still blocked one second after transport closure,
the worker command exits; kernel descriptors close and durable protection/recovery
state remains. Every network mutation must therefore already have its manager
journal recorded. Explicit user Disconnect/Release belongs to the manager.

## Composition interfaces

`mactunipc` imports no `nativehost` code. It exports:

```go
const RuntimeDirectory = "/Library/Application Support/BlueGrass/Run"
const StateDirectory = "/Library/Application Support/BlueGrass/Tunnel"
const IntentPath = "/Library/Application Support/BlueGrass/tun-intent.json"

func Open(context.Context) (*Client, error)
func (*Client) Exchange([]byte) ([]byte, error)
func (*Client) Close() error
func ParseOwner([]string) (Identity, error)
func Serve(Identity, func(io.Reader, io.Writer, uint32) error) error
func ReadIntent() (*Intent, error)
```

`cmd/bluegrass-tunnel` calls `Serve` with the lead-owned
`nativehost.RunPrivilegedDarwinTunnel(io.Reader, io.Writer, ownerUID uint32)`.
The callback must construct and close the manager and run the existing host
schema/generation checks. No host callback is invoked before authentication.

The network manager owns atomic writes/removal of a root-owned, non-writable,
single-link regular intent marker. Its mode must allow the broker to read it
(normally `0644`); all fields are mandatory and unknown fields are rejected:

```json
{"version":1,"ownerUid":501,"active":true,"recoveryRequired":false}
```

Keep the marker present throughout partial setup, active protection and needed
recovery; remove it only after verified release. The marker contains no account
or node credentials. State/journal files live under the root-owned state
directory; the manager is responsible for their confidentiality and durability.
The marker's root-owned `0755` parent and `0644` marker survive reboot. Discovery
reads this durable path even when the runtime/socket directory is absent. The
installer creates/checks `/Library/Application Support`, then `BlueGrass`, then
`Run` (`0755`), then `Tunnel` (`0700`), retaining strict root/mode/ACL checks.
No `/var` symlink or daemon-group-writable `/private/var/run` ancestor is used.
`Run` is also durable: its socket and lock can remain after reboot or a crash.
The worker validates and exclusively locks the existing root-owned single-link
lock file before removing only a checked root-owned no-ACL socket and rebinding.
A live lock owner, symlink, foreign owner, unsafe mode/ACL or non-socket node is
rejected; the intent and journal are preserved. The lock file is reused and
contains no node credentials. Unsupported `Run/intent.json` blocks fallback with
`RECOVERY_REQUIRED` when no durable marker exists; it is never adopted or migrated
automatically. No shipped TUN versions require migration from the retired `/var`
location.

The lead adds `recoverDarwinTunnelIfPresent(input, output)` before initializing
system proxy in `nativehost/system_darwin.go`. An absent marker returns
`false, nil` without reading input. A valid marker requests authorization and
probes the root host's Hello. Active protection delegates the original stream
to that host. A verified inactive/disconnected host returns `false, nil` with
the original input untouched. Corruption, unsafe files, owner mismatch or
uncertain root state fails closed with `RECOVERY_REQUIRED` or another safe
startup error; it never silently starts system proxy. Once an intent is found,
recovery monitors kqueue EOF/error on the inherited stdin pipe without reading any
bytes, so GUI death cancels pending authorization and closes the probe client.
The monitor uses `EVFILT_READ` with `EV_CLEAR` and bounded 50 ms waits. It preserves the original input
even on verified inactive fallback. The production recovery source must expose
`SyscallConn` (normally `os.Stdin`); unsupported wrappers fail closed when intent
exists. The absent-intent path returns before installing a monitor or reading.

Safe broker startup errors are `AUTHORIZATION_CANCELLED`,
`AUTHORIZATION_TIMEOUT`, `PRIVILEGED_HELPER_INVALID`, `SERVICE_BUSY`,
`SERVICE_UNAVAILABLE`, and `RECOVERY_REQUIRED`. Errors are bound to the GUI's
actual Hello request ID; raw shell, AppleScript or filesystem error text is not
forwarded. Existing host command errors retain their existing wire schema.

## Validation

From `Windows/native/core`:

```sh
go test ./mactunipc
go test ./...
go vet ./mactunipc ./nativehost ./cmd/bluegrass-native
```

On a macOS runner also run `go test -race ./mactunipc` and build both worker
and broker. Darwin tests inspect real Unix peer credentials and sysctl birth
identity, reject unsafe paths, and execute quoting/shell syntax tests without
running installation or changing protected system paths. Portable tests cover
wrong UID/PID/reused PID, canceled/late approval, safe denial codes, corrupt
markers, metadata checks, frame bounds/truncation/JSON, deadlines, and stream
cleanup on EOF and owner death.

### Gated Darwin root-bootstrap CI smoke test

Use a disposable macOS runner under its ordinary, non-root UID, with
passwordless `sudo -n`. The test refuses any existing runtime or state directory
(including inactive/stale/other-user state), matching installed helper, or
nonempty product PF anchor. Do not run global TUN tests concurrently. It never
sends Connect and uses the actual final signed worker, not a substitute worker
mode. The sole authorization substitution is test-only `sudo -n` around the same
fixed installer command. Neither production executable reads a test bypass flag
or accepts additional worker options.

The lead must first build/sign the worker and build the broker with its hash.
Place byte-for-byte copies in a separate temporary fixture directory, with the
compiled test executable beside them. Do not add a test executable to an already
signed application bundle. From `Windows/native/core`, use:

```sh
fixture_dir="$(/usr/bin/mktemp -d "${RUNNER_TEMP:?}/bluegrass-bootstrap.XXXXXXXX")"
/bin/cp /absolute/package/Contents/MacOS/BlueGrass.Tunnel "$fixture_dir/BlueGrass.Tunnel"
/bin/cp /absolute/package/Contents/MacOS/BlueGrass.Native "$fixture_dir/BlueGrass.Native"
worker_sha="$(/usr/bin/shasum -a 256 < "$fixture_dir/BlueGrass.Tunnel" | /usr/bin/cut -c 1-64)"
go test -c \
  -ldflags "-X bluegrass/native/core/mactunipc.ExpectedWorkerSHA256=$worker_sha" \
  -o "$fixture_dir/mactunipc.test" ./mactunipc
BLUEGRASS_TEST_DARWIN_ROOT_BOOTSTRAP=1 "$fixture_dir/mactunipc.test" \
  -test.run '^TestDarwinRootBootstrapSmoke$' -test.v -test.timeout=7m
```

`BLUEGRASS_TEST_DARWIN_ROOT_BOOTSTRAP=1` is the only gate; it is read exclusively
from `_test.go`. The test checks the worker's signature and broker's exact hash
info, records the ordinary process's kernel UID/PID/birth identity, runs the real
installer under sudo, verifies the root socket peer and birth identity, and sends
Hello followed by explicit Disconnect. Both replies must report a verified
inactive/disconnected state. Closing IPC must remove the socket and end that
same root process within six seconds.

The existing bootstrap gate also runs these mandatory subtests before installing
the worker; no extra environment switch is required and failures never skip:

- `TestDarwinRootBootstrapSmoke/RootOwnedWritableACL`: root-owned `0755` fixtures
  retain their UID/mode while writable ACLs are added. Both installer validation
  and Go file/ancestor validation must reject them. Only new fixture ACLs change.
- `TestDarwinRootBootstrapSmoke/DurableIntentWithoutRuntime`: temporarily moves
  only the witnessed fresh runtime directory aside, verifies durable discovery
  while runtime is absent, rejects a corrupt durable marker, and permits a fresh
  start only after both markers are absent. It restores the same runtime inode.
- `TestDarwinRootBootstrapSmoke/StaleRuntimeRecovery`: verifies that a held real
  runtime lock excludes another descriptor, then leaves the lock unlocked and a
  root-owned closed Unix socket on disk. The actual signed worker must reuse the
  lease, remove/rebind the stale socket, complete Hello/Disconnect, and exit with
  its socket removed. This exercises durable Run contents left across reboot.

These are root filesystem tests simulating a missing runtime directory and stale
post-reboot IPC entries, not an actual runner reboot. The regular
`go test ./mactunipc` gate additionally runs
`TestSecurityAttributesRejectACLAndUncertainMetadata`,
`TestDurableIntentDiscoveryDoesNotConsultRuntime`,
`TestAbsentDurableIntentStillRefusesLegacyAndUnsafeMarkers`, and
`TestInstallerACLFilterRequiresCleanCompleteListing`. The awk test is mandatory
on Darwin and only skips on other hosts without awk.
`TestRuntimeDiscoveryStaysUnderProtectedLibraryParent` checks absent-marker
discovery stays under the strict Library parent and the socket fits Darwin's
Unix address limit.

A separate test-only sudo fixture reserves fresh fixed directories, keeps a
root-owned ownership witness (UID/PID/birth, hash, directory device/inode), and
compares read-only before/after snapshots of IPv4/IPv6 routes, interfaces, DNS,
PF rules/NAT/product anchor, enabled state and enable references. Packet counters,
PF uptime and route-use/expiry metrics are excluded. Every subprocess has a
deadline and bounded output. Cleanup takes both worker/manager leases, rejects
intent/journal/socket/unexpected files, verifies directory identities and the
installed helper's hash, and removes only known fixture files and newly created
empty directories. It never recursively deletes or adopts existing state. Any
active/changed/uncertain state is retained and reported as a test failure.
The durable intent parent is witnessed even when it predated the test; cleanup
requires that same parent identity and refuses any remaining durable intent.
Existing parent contents are preserved and no marker is deleted by cleanup.

An inactive explicit Disconnect still runs persistent fake-IP cache clearing.
That operation can create/retain `StateDirectory/fakeip.db` as a valid empty DB.
The fixture accepts and removes this owned file only after checking root
ownership, exact `0600` mode, a regular single-link file, a 32 MiB size bound,
bounded read-only database locking and no remaining logical buckets/metadata.
It never opens the DB writable or clears it itself to make cleanup pass. A
populated, corrupt, unsafe or still-locked cache is retained and fails cleanup.

This smoke test does not display the system password dialog and is not automated
acceptance of its UX, cancellation, or credential handling. Those remain explicit
user Mac tests. It also does not establish active utun packet/PF/route/DNS
acceptance: the bootstrap session intentionally stays inactive.

The production exchange and server operation deadlines are 100 seconds, covering
the manager's 60-second Prepare, 20-second gate work and bounded cleanup. Dart's
request budget (including Hello) is 120 seconds; the lead owns that setting.
Authorization is limited to 75 seconds and the broker startup sequence is capped
at 115 seconds across authorization, the recovery probe and the actual GUI Hello.
The remaining startup budget is carried through recovery handoff. Client Close,
input EOF, owner death and timeout
close transport immediately; root cleanup retains its one-second exit grace.
The idle deadline stays 30 seconds and applies between replies and new requests,
not during active processing. No unbounded wait is introduced.

Windows execution and Darwin cross-compilation do not establish system-dialog,
root installation, actual utun, PF, route or resolver acceptance. The user must
verify those on a Mac with the final packaged architecture and signature/hash
order, including cancellation, broker termination during authorization and an
active session, restart recovery and explicit release. Do not remove the
durable recovery marker, state directory or helper executable as a way to release active
protection; use the manager's verified release path.
