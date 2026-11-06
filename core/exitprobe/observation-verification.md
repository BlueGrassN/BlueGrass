# Exit discovery after connection — native verification

Date: 2026-10-12. This correction supersedes the former expected-exit prerequisite and mismatch-revocation behavior in Local, TUN and diagnostics. The source changes are confined to `localengine`, `tunengine`, `exitprobe`, `diagnostics`, and their tests/documentation. No control, engine.Profile, nativehost, winservice, GUI, domain, module or shared build-script files were edited by this task.

## Integration contract

- A complete, valid prepared native engine can open its business gate **before** any exit probe. Local requires a live prepared outbound/session and confirmed cleanup; TUN additionally checks its captured physical network generation, creates the native interface with the gate closed, and opens only after interface readiness. Expected-IP metadata is not consulted.
- `ProbeExit(context.Context) (engine.ProbeResult, error)` retains its signature. It uses the selected outbound, accepts the first valid public **IPv4** observation, and records it only for the still-current session. A later different valid IPv4 replaces that observation and preserves forwarding. IPv6 is outside D33 display scope: it is an invalid detector response and permits bounded fallback, never a reason to close the gate.
- `VerifiedIPv4() string` returns the current open session's last observed public IPv4. It can be empty after connection. It is not an eligibility or forwarding prerequisite. Prepare/pause/stop retire the prior observation; detector failures, including IPv6-only responses, preserve the last successful observation in the same session.
- `exitprobe.Diagnostic(ctx, binding, expected string, probe)` retains its argument list; `expected` is ignored. Success is always `Status=Passed`, `Code="observed_exit"`, with the current `ObservedIP`. No expected comparison or mismatch code remains. The obsolete `DiagnosticMismatch` helper was removed after confirming no remaining caller in the module.
- `diagnostics.Request.ExpectedExitIP` remains optional ignored wire metadata for compatibility. Its absence, invalid address text or difference from the observation neither skips the probe nor invalidates the request. Binding, selected channel, trusted endpoint, TLS, deadline and public-address requirements remain.
- Local/TUN `Diagnose` commits a successful observation under captured generation/session checks, without holding engine locks across network work. Failure or changed address never retires a session. Stale/cancelled reports discard observations and cannot modify a replacement.
- TUN distinguishes **observed physical generation change** from a cancelled probe or an unavailable snapshot. An actual changed generation retires the captured session; detector/caller failure alone does not. Opening a gate still requires a valid current network snapshot. Local continues to rely on the host's Stop/Prepare lifecycle for physical-network invalidation.
- FakeIP mapping namespaces no longer include `Profile.ExpectedExitIPv4`; resource identity, protocol, LAN policy, physical network and pool remain. Expected-only metadata changes do not discard mappings. Existing on-disk namespaces computed by the earlier structure are different and follow the existing namespace-change reset path once; no old mappings are migrated into a different namespace.

No new runtime abstraction was introduced. The two internal `expected` fields, comparisons, revocation paths, cache-key input and obsolete mismatch helper were removed. No automatic probe is started inside either engine; the controller owns best-effort post-connect invocation.

## Red/green evidence

Before implementation, the new `TestObservation*` tests compiled and failed behaviorally in all four packages:

| Case | Observed red behavior | Green behavior |
| --- | --- | --- |
| Local/TUN Prepare with empty or malformed legacy expected metadata | `UNSUPPORTED_PROFILE` | Prepared successfully |
| Local/TUN open before probe | `EXIT_NOT_VERIFIED` | Opened without calling a detector or fabricating an observation |
| Changed valid address on an established connection | Forwarding/session retired | Public IPv4 observations update while active TCP remains usable; TUN's tracked UDP remains open |
| FakeIP expected-only metadata change | Namespace changed | Namespace stable; real network generation still changes it |
| Shared diagnostic without expected | `skipped/no_active_binding` | `passed/observed_exit` |
| Standalone diagnostics without expected | `skipped/expected_exit_missing` | Actual local TLS fixture produces `passed/observed_exit` |

Existing regressions were updated to the corrected contract rather than deleted. They still check failed Prepare, explicit pause/stop, teardown errors, terminal Close, conflict validation, previous-session result rejection and cancellation. Added cases cover caller cancellation before/during detection, unavailable network snapshots, malformed detector responses, and actual physical generation changes during both ProbeExit and Diagnose.

The 10-protocol Local and TUN controller integration matrices now omit expected metadata, block the fake detector response, and assert that the controller is already connected with its gate open and observation empty. Releasing the detector then updates the observation without creating a second connection. Only their engine-owned test files changed; they consume the concurrently updated controller implementation.

An initial follow-up rejected only IPv6 documentation addresses. Review correctly identified that accepting any IPv6 exit observation exceeded current D33 and wire/UI scope. That extension was removed: shared and standalone diagnostic parsers now use the existing public-IPv4 predicate, with no IPv6 success branch or separate IPv6-prefix policy. Historical checks below precede this final review correction; its focused results are recorded separately.

## Executed checks

Working directory: `E:\workspace\APP\core`. Toolchain: `go1.27.2 windows/amd64`; cached sing-box remains pinned to `v1.14.2`. No module edits or upgrades.

```text
go test -mod=readonly -tags with_gvisor ./localengine ./tunengine ./exitprobe ./diagnostics -run '^TestObservation' -count=1 -timeout=90s
go test -mod=readonly -tags with_gvisor ./localengine ./tunengine ./exitprobe ./diagnostics -count=1 -timeout=120s
go test -mod=readonly -tags with_gvisor -race -json ./localengine ./tunengine ./exitprobe ./diagnostics -count=1 -timeout=180s
go vet -mod=readonly -tags with_gvisor ./localengine ./tunengine ./exitprobe ./diagnostics
go build -mod=readonly -tags with_gvisor ./localengine ./tunengine ./exitprobe ./diagnostics
```

All passed after the observation implementation and controller-test correction. The race run used `CGO_ENABLED=1` and `CC=C:/msys64/ucrt64/bin/gcc.exe`; package results were Local 1.350s, TUN 4.680s, exitprobe 6.880s and diagnostics 4.070s, exit code 0. This is race evidence, not a performance benchmark.

After the final IPv6 documentation-address validation edit, the focused regression was rerun and passed:

```text
go test -mod=readonly -tags with_gvisor ./exitprobe -run '^TestObservationDocumentationIPv6IsNotPublicEvidence$' -count=1
ok bluegrass/native/core/exitprobe 0.044s
```

The package-wide race/vet/build results precede the subsequent IPv4-only review correction; they were not redundantly rerun while main started integrated `check.ps1`. Main's integrated result is separate evidence. A source scan found no `ExpectedExitIPv4`, `EXIT_NOT_VERIFIED`, `DiagnosticMismatch`, `exit_mismatch`, `expected_exit_match` or `expected_exit_missing` use in these packages' non-test Go source. The remaining `Request.ExpectedExitIP` field is explicitly documented and ignored.

Tests used mock native TUN lifecycles, in-memory streams and loopback TLS/protocol fixtures. No live proxy endpoint, production server, native TUN creation, WFP mutation, OS proxy change or port 10808 operation was performed. This task makes no claim about installed-service or production runtime acceptance.

## D33 review correction: IPv4-only observation

The final contract is IPv4-only. `ValidObservedAddress` now delegates directly to `gate.PublicIPv4`; standalone `diagnostics.parseExit` uses the same existing predicate. The temporary IPv6 acceptance and documentation-prefix exceptions were removed. No domain, wire, controller, nativehost or UI IPv6 extension was made. Diagnostic and engine method signatures are unchanged.

Before this simplification, all five `TestIPv4Only*` regressions compiled and failed: Local/TUN accepted IPv6, the shared helper treated it as valid, JSON/plain IPv6 stopped fallback before the third endpoint, and standalone diagnostics published IPv6 as `passed/observed_exit` after one attempt.

After the change, the focused race run passed **21 top-level tests, 40 tests including subtests**, exit code 0:

```text
go test -mod=readonly -tags with_gvisor -race -json ./localengine ./tunengine ./exitprobe ./diagnostics -run '^Test(IPv4Only|Observation|ProbeFirstValidIPv4|DiagnosticUsesExisting|LocalChangedObservation|LocalDiagnosticObservation|TunDiagnoseChanged|ChangedObservationAndDetector|ProxyExitActual)' -count=1 -timeout=90s
```

Package results: Local 1.112s, TUN 1.140s, exitprobe 1.159s, diagnostics 1.266s. No full integrated suite was repeated.

The regressions verify that JSON and plain-text IPv6 responses continue bounded fallback until a public IPv4 is found; IPv6-only diagnostics fail without publishing an address. Local/TUN reject the IPv6 observation while preserving the prior IPv4, open gate and an actively usable TCP stream; TUN's tracked UDP remains open. Changed public IPv4 acceptance, no-expected/no-probe gate activation, stale/cancelled observation rejection and real physical-network invalidation remain covered. Poincare's nativehost diagnostic-snapshot publication correction is outside this task's edits and evidence.
