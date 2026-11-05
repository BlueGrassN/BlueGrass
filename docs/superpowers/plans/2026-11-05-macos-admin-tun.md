# macOS administrator TUN implementation plan

**Goal:** Add usable administrator-authorized TUN to the existing Mac development package and produce verified GitHub artifacts for both architectures.

**Spec:** ../specs/2026-11-05-macos-admin-tun-design.md

## Work allocation

1. [x] Darwin Xray adapter, physical network enumeration, cache and kernel tests (`tunengine`; platform worker). Windows tests and Darwin cross-compilation pass; kernel execution is tracked in step 5.
2. [x] Safe authorization/bootstrap, private IPC, broker and root worker command (`mactunipc`, nativehost broker, command; authorization worker). Deadline/recovery cancellation, ACL validation and durable recovery discovery integrated and locally verified; real bootstrap execution tracked in step 5.
3. [x] PF/routes/DNS ownership, recovery and Xray composition (`mactun`; network worker). Persistent cache, ownership masking and Windows unit/race/vet verified; kernel execution is tracked in step 5.
4. [x] Shared UI capability/API transport, nativehost composition/protection labels, build/sign/hash order, CI and package tests (lead). Flutter 436/436, analysis, package 11/11 and shared asset checks pass.
5. [ ] Independent security/lifecycle review, fixes, Windows regressions and real macOS CI, artifact verification and testing instructions.

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
- The production Mac API transport is wired to native protected resolution. Startup recovery test proves no bootstrap/direct fallback on a missing pin; admin TUN suite: 3 passed.
- A regression test first reproduced stale connected state after a platform network fault, then passed after snapshot synchronization. Full Windows Go suite passed with the approved Wintun fixture copied locally from the original checkout (not packaged or added to Git).
- Go nativehost/mactun/mactunipc/tunengine race tests and vet passed. Darwin arm64 worker/broker build and vet passed; amd64 follow-up is in progress.
- Final Flutter regression: 436/436; analysis clean. Targeted unknown-recovery regression suite: 41/41. TLS cancellation and all native review findings fixed; Windows native targets and both Darwin architectures cross-compile. Real macOS kernel/bootstrap/build and artifact verification remain. Previous system-proxy artifacts do not include TUN.
