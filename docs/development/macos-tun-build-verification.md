# macOS administrator TUN build verification — 2026-11-05

The user requests a development package with both system proxy and full administrator-authorized TUN before obtaining Apple certificates. UI shares the existing Windows Flutter package. This supersedes the system-proxy-only build recorded in `macos-build-verification.md`.

## Implementation and development checks

- Fixed Xray Darwin utun backend, administrator bootstrap with a hash-pinned root helper, authenticated/bounded IPC, root-owned PF/routes/DNS/cache and recovery journal.
- Same shared pages/navigation/themes/components; Mac PF protection labels and administrator authorization/error copy.
- API HTTPS remains in Dart with original hostname/certificate verification and protected numeric address pins. No account token is passed to the root worker.
- Whole Windows Go module tests pass with the approved local Wintun fixture. Targeted race/vet checks pass; Darwin cross-compilation is a compile check only.
- Shared Flutter analysis clean; final full application regression suite: 436/436. This includes Mac platform UI (21), administrator startup/recovery (4), and numeric HTTPS/TLS/cancellation (19) tests. Thin Mac launcher analysis and all three tests pass. Package/sign/hash tests: 11/11; canonical shared asset check passes.

## Independent review fixes

Before the first TUN build, independent review identified installer ACL validation, reboot-persistent recovery discovery, fake-IP reuse after explicit disconnect, unused-interface DHCP handling, and DNS-only link-change recovery. The fixes now reject uncertain/writable ACLs, use a durable root-owned marker, retain validated fake-IP allocation highwaters when clearing mappings, skip unused interfaces without usable IPv4, and rebuild paused bootstrap after verified DNS-only changes. Regression tests pass; Darwin-specific ACL, kernel and installation execution remains a CI requirement. The reviewed TLS/socket adapter and sign/hash packaging have no remaining important findings.

An additional integration regression first reproduced ordinary bootstrap after an unconfirmed Mac recovery. The Mac controller now blocks bootstrap and ordinary account traffic until recovery is known, even if no valid broker snapshot has established the previous mode. Its targeted suite (41 tests) and the final full Flutter suite pass. Normal system-proxy and Windows paths keep their existing transport behavior.

## macOS CI

Pending: commit, run, native kernel/bootstrap execution and verified artifact links. No completed TUN package or real device acceptance is claimed by this entry yet.

## User device acceptance

See `Mac/docs/TESTING.md`. Password-dialog UX/cancellation, active PF/global-route/DNS takeover with an actual resource, account API during TUN, TCP/UDP/DNS/IPv6/LAN policy, disconnect and crash recovery, network switch and sleep/wake require the user's Mac. Hosted runner tests deliberately use isolated routes/anchors and do not disrupt the runner's control connection.
