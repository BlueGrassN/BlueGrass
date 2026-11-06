# macOS administrator TUN implementation plan

**Goal:** Add usable administrator-authorized TUN to the existing Mac development package and produce verified GitHub artifacts for both architectures.

**Spec:** ../specs/2026-11-05-macos-admin-tun-design.md

## Work allocation

1. [x] Darwin Xray adapter, physical network enumeration, cache and kernel tests (`tunengine`; platform worker). Windows tests and Darwin cross-compilation pass; kernel execution is tracked in step 5.
2. [x] Safe authorization/bootstrap, private IPC, broker and root worker command (`mactunipc`, nativehost broker, command; authorization worker). Deadline/recovery cancellation, ACL validation and durable recovery discovery integrated and locally verified; real bootstrap execution tracked in step 5.
3. [x] PF/routes/DNS ownership, recovery and Xray composition (`mactun`; network worker). Persistent cache, ownership masking and Windows unit/race/vet verified; kernel execution is tracked in step 5.
4. [x] Shared UI capability/API transport, nativehost composition/protection labels, build/sign/hash order, CI and package tests (lead). Flutter 436/436, analysis, package 11/11 and shared asset checks pass.
5. [x] Independent security/lifecycle review, fixes, Windows regressions and real macOS CI, artifact verification and testing instructions. Both architectures passed run `42144670941`; downloaded `0.1.0-dev+9` packages verified. Interactive authorization and full networking acceptance remain user Mac tests, as planned.

## Review focus

- A cancelled/late authorization or reused PID must not gain a live privileged session.
- Root helpers must not execute user-provided commands, paths or unchecked binaries; credentials stay off argv/logs/disk.
- No connection success until the adapter, routes, resolver and protection are real and owned.
- Failures and other writers cannot erase needed recovery state or overwrite external settings.
- API traffic keeps original TLS validation during TUN; UDP/DNS and route cleanup need actual kernel evidence.

Existing worktree/branch `codex/macos-client` is already isolated. Preserve the previously successful system-proxy build and Windows behavior. User authorization already covers this development, password-based TUN design and GitHub builds; proceed through implementation and verification.

## Integration record

- Native root composition and system-proxy startup recovery wired; manager ownership errors map to fixed `OWNER_MISMATCH`, other initialization failures to `RECOVERY_REQUIRED` with the original Hello ID.
- Shared UI now enables administrator TUN and uses PF status on Mac, preserving Windows WFP status. Four width/theme scenarios and platform suite: 21 passed.
- The production Mac API transport is wired to native protected resolution. Startup recovery tests prove no bootstrap/direct fallback on a missing pin or unknown recovery; admin TUN suite: 4 passed.
- A regression test first reproduced stale connected state after a platform network fault, then passed after snapshot synchronization. Full Windows Go suite passed with the approved Wintun fixture copied locally from the original checkout (not packaged or added to Git).
- Go nativehost/mactun/mactunipc/tunengine race tests and vet passed. Both Darwin architectures pass worker/broker cross-build and vet; Windows native targets pass.
- Full Flutter regression: 436/436; analysis clean. Targeted unknown-recovery regression suite: 41/41. After refreshing the TLS test certificate and adding one trust smoke test, all 20 transport tests pass; the full suite was not rerun after that fixture-only change. TLS cancellation and all important native review findings are resolved.
- macOS CI uses Node.js 24 Actions, including checkout/setup-go/upload-artifact and the Flutter action's nested cache; both completed logs have no Node.js 20 deprecation warnings. Native utun/PF tests, final packaged root bootstrap and Xcode platform contracts passed on both real macOS runner architectures. Downloaded ZIP checksums/CRC, Mach-O architectures, worker hashes and shared assets passed. Final build and artifact evidence is recorded in [macOS TUN build verification](../../development/macos-tun-build-verification.md); previous system-proxy artifacts do not include TUN.
