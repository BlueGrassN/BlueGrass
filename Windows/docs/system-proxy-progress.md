# Windows system proxy continuation (2026-10-12)

The user authorized Windows client development, ablation and unrestricted local
testing after removing their other proxy. Existing uncommitted work stays in
`feature/windows-client`; all implementation and new evidence stay in `Windows/`.

Design authority: accepted ADR 0045 (default system proxy), ADR 0031 (mode-scoped
LAN behavior), and the existing Windows connection plan. This continues that
approved implementation; it does not introduce a new product design.

## Plan and ledger

1. Verify direct API/login and the user-specified exit without an API proxy.
2. Integrate the existing WinINET transaction manager with the private native
   host. Apply only after verified connection; retain a closed local proxy on
   pause/expiry; restore on explicit disconnect and parent EOF. A process lock
   excludes competing BlueGrass writers. Recover durable owned state on restart.
3. Display actual system-proxy state in Flutter, require restoration in shutdown
   acknowledgements, and remove outdated claims that settings never change.
4. Test real settings, Windows-default HTTP requests, disconnect, parent EOF,
   native forced termination/restart, negative exit, and ownership loss.
5. Run isolated ablations, full checks, package both dev and test builds and
   record exact scope/results. No merge, push or modification of other services.

Ruling: retain the local-only native invocation for diagnostics and old tests;
the Flutter production launcher explicitly requests system-proxy mode. This
avoids making ordinary unit tests or low-level probes change Windows settings.

Ruling: native forced termination cannot execute cleanup. The encrypted durable
record permits ownership-checked recovery at the next native start. This is not
a reboot protection or zero-leakage claim; no privileged watchdog is invented.

Ruling: the current system-proxy LAN option uses WinINET exceptions. Full routing,
DNS interception, TUN/WFP, UDP and IPv6 remain separate acceptance work.

Pre-flight: the host translates controller state to IPC; Flutter previously
required systemProxyApplied=false. Both ends and shutdown tests must change
together. The manager must recover a prepared record when no write occurred.

- Step 1 complete: direct API login, unique resource 11134, verified exit
  63.161.180.219, wrong-exit rejection, traffic, pause, disconnect and logout all
  passed. Evidence: `.artifacts/verification/direct-baseline-20261012.log`.
- Initial OS observation: ProxyEnable=0, remembered server 127.0.0.1:10808;
  xray still listens on 10808. The test never controls that process.

- Steps 2-3 complete: current-user transaction integrated with private host and
  verified system-proxy UI state. Durable record, per-user writer lock, startup
  recovery, explicit disconnect and EOF cleanup are implemented.
- Step 4 native live verification complete: default Windows request observed
  the supplied IP; pause, disconnect, EOF, forced native termination/restart and
  external ownership loss all passed with exact final restoration.
- Step 5 ablation complete: three isolated mutations fail behavioral assertions;
  restored nativehost/winproxy suites pass. Full checks and packaging tracked in
  `system-proxy-verification.md`.

Final review: independent reviewer identified failed-transport reuse, missing GUI
startup recovery, stuck obsolete ownership, lost disconnect action on expiry,
and premature idle on terminal auth failure. All five entered one fix pass with
subprocess/transaction regressions and another complete live native test.

Ruling: explicit Disconnect may relinquish an obsolete ownership record only
after confirming current settings differ from Applied. It must preserve those
settings and report preserved, not restored. Automatic EOF cleanup still returns
ownership loss. Startup exposes Hello/recovery instead of exiting forever.

Ruling: resource/session initialization continues after a reported recovery
failure, but login/connection remains blocked until network release is confirmed.
This lets later recovery retain the account-specific exit binding.

- Final checks complete: domain 73, data source 70, app/IPC 107, contracts 8,
  Go race/vet, Windows DPAPI/fixture 2, live Windows connection 1. Real desktop
  checks wait for startup recovery; one intervening login failure lacked a
  detailed cause, followed by successful direct-adapter and full desktop runs.
- Packaging complete: both release ZIPs rebuilt and checked for required runtime
  files, absence of account/session files, correct binding mode and exact native
  binary hash. Final original proxy settings restored, recovery record absent,
  no remaining native process. Hashes and evidence are in the verification report.
