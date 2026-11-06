# Windows Beta completion ledger

Current connection behavior is defined by [observed exit flow](observed-exit-flow.md):
valid nodes connect without an expected IP, then report an advisory observation.
The [current verification](observed-exit-verification.md) supersedes prior exit-gate
and test-binding requirements. Production migration and login-IP results remain in
[production API verification](production-api-verification.md). The earlier
[2026-10-12 integrated verification](beta-final-verification.md) preserves the
pre-migration checkpoint. Entries below preserve the implementation history;
old pending items are superseded by the newer reports.

Authority: accepted product ADRs D-01–D-19 and D-23–D-48, main design and
addendum in `docs/superpowers/specs/`. User requests a complete Beta, all Windows
client code in `Windows/`, followed by ablation. On 2026-10-12 the user explicitly
deferred SMS login: retain the capability/interface reservation, do not require
SMS implementation or acceptance for this Beta. The later explicit user correction
also removes fixed expected-exit authorization and comparison requirements.

Existing branch `feature/windows-client` and dirty work are retained. The prior
system-proxy development ZIPs are historical evidence, not complete Beta builds.

| Requirement | Current implementation/evidence | Remaining gate |
| --- | --- | --- |
| Password/session/DPAPI | implemented; production login, resource pagination and all-four-node desktop connection regression passed | maintain regression |
| SMS | capability reserved | explicitly deferred by user |
| System proxy/default mode/recovery | implemented, real WinINET and failure tests | maintain regression; full LAN semantics |
| TUN TCP/UDP/DNS/IPv6/LAN | sing-box TUN, exact UDP relay permissions, FakeIP cache, LAN policy and independent stopped-state local DNS implemented; typed protocol construction tested; GUI physical HTTPS verified against production | signed elevated packet tests; real remote protocol matrix |
| WFP persistent protection/service IPC | native WFP, authenticated installed broker/GUI/service chain, durable v2 logical-session and teardown recovery journal implemented | real failure/reboot/network change tests |
| Independent repair | exact adapter/cache/WFP/user-proxy cleanup implemented | real installed cleanup under service failure |
| Exit observation/all-owned resources | connect first, observe actual exit through selected node; production resource and expiry endpoints verified; conservative partial snapshot contract | final ambiguous ownership cases and complete summary coverage; no expected-exit prerequisite |
| Startup/auto-connect/reminders/details | preferences, recent-success history, exact-resource auto-connect, notification activation and DPAPI persistence implemented | real installed startup/OS notification lifecycle; complete authoritative summary contract |
| Updates | strict Ed25519 manifests, publisher verification, bounded resume downloader, physical-route cost checks, background download, protected pause/handoff and durable logical-session recovery implemented | real signed feed, metered network changes and installed upgrade |
| Install/upgrade/uninstall | Inno real-payload candidate + production signing entry, five native helpers, ACL/service lifecycle implemented | real publisher identity, signed binaries, elevated install/upgrade/repair/uninstall |
| Diagnostics/privacy/license | bounded helper, real HTTPS/DNS/TCP/selected-channel probes, exact-context asynchronous UI/service integration, opt-in bounded crash queue, sanitized export and license implemented | configured crash receiver and real UDP endpoint acceptance |
| Acceptance | existing system-proxy evidence | TUN failures/reboot/upgrade, OS matrix/performance, ablation |

## Execution

1. Complete native protection, service and private broker, with tests for
   denied callers, bounded input, ownership, durable intent and release ordering.
2. In parallel, bounded work on TUN engine, UI/lifecycle, updater/installer and
   source-grounded API/data-source expansion. Each has disjoint file ownership.
3. Integrate contracts and user-visible actions; run targeted tests then suites.
4. Exercise real Windows operations only with explicit rollback/repair available;
   preserve the existing listener at 127.0.0.1:10808.
5. Run isolated ablations, review, compile/package and verify release gates.

## Findings and decisions

- Current shell token is medium integrity; Administrators is deny-only. This is
  not an elevated WFP/service installation test environment. A real elevated
  run will be needed for machine operations, rather than treating mocks as proof.
- No code-signing certificate was returned by the CurrentUser/LocalMachine My
  code-signing certificate inventory. A production trust root/private key is
  not generated or trusted merely to make signing checks pass.
- Preserve inherited-pipe system-proxy host for current-user registry ownership;
  privileged service must never write LocalSystem's HKCU as the user's proxy.
- Windows implementation additions stay in `Windows/`; API server changes stay
  in its existing `client-api/` component and require separate deployment proof.
- Service/engine/protection tests must distinguish safe compilation/unit results
  from actual administrator, packet capture, reboot and multi-user evidence.

Progress entries and exact evidence are appended as implementation proceeds.

## 2026-10-12 integrated checkpoint

- Prior integrated check passed domain 73, data-source 98, app 184 and contract 8
  tests, plus native race/vet. `beta-full-check.log` records that earlier source
  snapshot; later changes need a new full check. The earlier Flutter Release
  and unsigned noninstallable installer candidate are similarly not current.
- Three further compiled behavioral ablations passed for caller trust, retaining
  protection on host close, and monotonic server-clock calibration. See
  `.artifacts/ablation/beta-20261012-141933/`. Together with the earlier 17 and
  updater signature/hash pair, 22 experiments have run; this is not elevated OS
  or production security acceptance.
- Live system-proxy regression exposed timeout of both configured physical DNS
  servers. Read-only UDP/TCP queries to 1.1.1.1 and 223.5.5.5 succeeded. Auto DNS
  now uses these as explicit bounded backups; custom mode remains exact. The
  rebuilt native host passed actual live system-proxy regression: wrong exit
  rejected with CONN-005, correct exit 63.161.180.219, pause blocks traffic and
  retains proxy, disconnect/EOF/crash recovery restore exact settings, foreign
  changes preserved and final cleanup/logout confirmed. Evidence:
  `beta-live-system-proxy-dns-fixed.log`. Reserved 127.0.0.1:10808 was not changed.
- Main controller regression tests reproduced and fixed invalid-session handling
  in diagnostics/detail refresh, keeping an authenticated control connection while
  paused, and recognizing TUN protection after failed first preparation. The
  summary scan now uses 30 minutes +/- 5 minutes, independently of minute-level
  active resource checks, with a bounded 1/2/4 minute transient retry burst.
  `beta-auth-recovery-red.log`, `beta-summary-red.log` and
  `beta-auth-recovery-green.log` record the failing cases and 19 passing tests.
- Update resume marker integration is in progress: DPAPI resource identity/mode,
  confirmed handoff, later build check, fresh eligibility and exit proof. Native
  logical-session continuity still needs integration and must not be inferred
  from GUI marker persistence.

## Further integrated checks

- GUI recovery now persists a DPAPI marker containing only resource identity,
  mode, original logical ID and build. It marks confirmed handoff, requires a
  newer build, re-reads eligibility and verifies the exit before resuming. Same
  version restart cannot resume. Explicit disconnect removes the marker or
  durably replaces it with cancellation; failure of both is reported while
  network cleanup still runs. TUN records alone cannot recreate released native
  protection. Nativehost now obtains the logical ID from the service journal;
  GUI records never override service ownership.
- Reviewer found three independently reproduced state races: overlapping logout,
  stale update resume after failed marker deletion, and pending old-account
  notifications after invalidation. `controller-review-red.log` and
  `controller-review-green.log` record three failures then 18 passing related
  tests. Logout is serialized before its first await; auth invalidation retires
  reminder revisions synchronously without erasing durable reminder history.
- Selected-resource `Diagnose` is asynchronous, exact-context-bound and bounded.
  Flutter consumes only the same job generation; stale results are rejected.
  UDP remains skipped when its trusted endpoint is unconfigured, and UDP failure
  does not disconnect. Related real-child transport/controller tests passed;
  this does not provide real production UDP acceptance.
- D36 automatic eligible background download and D09 opt-in crash queue are
  integrated. Actual updater route/NLM costs are checked by the native helper;
  readonly interface/NLM smoke passed. Real metered-network switching remains
  untested. Crash upload remains disabled with no configured receiver; only
  confirmed abnormal native exit creates the fixed native-crash signal.
- Dart typed protocols, API optional `protocol_config`, closed schemas and the
  shared 45-vector matrix passed domain 129, data-source 165 and contract 12
  tests. App sends immutable typed configuration, preserves it through the
  authorized test exit binding, and pauses on configuration-only changes.
  There are no fabricated server non-SOCKS resources or real multi-protocol
  acceptance claims.
- Windows desktop integration passed two tests: real DPAPI confinement/atomic
  replace and the fixture UI login/confirmation/connection/theme/logout flow.
  Screenshots in `.artifacts/screenshots/beta-current/` were visually inspected.
- Two more isolated compiled controller ablations passed, total **24**. Evidence
  is `.artifacts/ablation/controller-20261012-153954/`.
- Remaining native work at this checkpoint: v2 journal/service completion and
  independent local-only DNS runtime while forwarding is stopped, followed by
  normal-module unified race/vet, builds and renewed live/installer verification.

The full Beta gate remains open. SMS is deferred by direct user instruction;
other local implementation gaps and real signing/service/network acceptance
above remain explicit. An unsigned development bundle is not a complete Beta.

## Latest integration

Latest correction on 2026-10-12: removed the required expected-exit field,
address-equality gate and test-binding loader/build path. Four production API
nodes with no expected IP now pass native proxy, actual WinINET lifecycle and
desktop connection/display checks. Detection failure leaves forwarding active;
diagnostic retry publishes only to the exact current session. Current tests:
domain 162, data-source 171, Flutter 332, contracts 13, Go race/vet and two
new isolated behavior ablations. See [current verification](observed-exit-verification.md)
for the current package and remaining signing/elevated-runtime limits.

The following bullets preserve the preceding checkpoint:

- Completed protected resource switching/retry, explicit contextless journal
  recovery, all-resources UI, full eligibility filtering and recent ordering.
- Minimum-version startup policy now precedes automatic connections/update
  resume and remains enforced for manual/native entry points. Native code
  revalidates signed cached authority; the GUI does not persist a trust bool.
- GUI physical HTTPS transport is integrated and passed actual API login and
  a separate Windows MethodChannel health read. Service tokens are not used to
  relay business request bodies; original hostname TLS validation stays in GUI.
- Unified check passed domain 129, data-source 162, Flutter 322, contracts 12,
  full native race/vet and two Windows DPAPI/desktop integration tests.
- Final live GUI script passed both targets using separate Flutter launcher
  sessions. First multi-target launch failure remains in its original log.
- 25 different behavioral ablations have been executed. Current detailed results,
  packaging and remaining real release gates are in the report linked above.
