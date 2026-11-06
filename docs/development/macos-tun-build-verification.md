# macOS administrator TUN build verification — 2026-11-06

The later [dependency refresh](macos-dependency-refresh-20261106.md) provides verified build **0.1.0-dev+10** with Flutter 3.47.9 and current packages. This entry retains the original build+9 implementation evidence.

The user requests a development package with both system proxy and full administrator-authorized TUN before obtaining Apple certificates. UI shares the existing Windows Flutter package. This supersedes the system-proxy-only build recorded in `macos-build-verification.md`.

## Implementation and development checks

- Fixed Xray Darwin utun backend, administrator bootstrap with a hash-pinned root helper, authenticated/bounded IPC, root-owned PF/routes/DNS/cache and recovery journal.
- Same shared pages/navigation/themes/components; Mac PF protection labels and administrator authorization/error copy.
- API HTTPS remains in Dart with original hostname/certificate verification and protected numeric address pins. No account token is passed to the root worker.
- Whole Windows Go module tests pass with the approved local Wintun fixture. Targeted race/vet checks pass; Darwin cross-compilation is a compile check only.
- Shared Flutter analysis clean; full application regression suite: 436/436. This includes Mac platform UI (21), administrator startup/recovery (4), and numeric HTTPS/TLS/cancellation (19) tests. A later CI compatibility fix regenerates the loopback TLS fixture with a one-year leaf and adds a direct trust smoke test: all 20 transport tests pass afterward. The full suite was not rerun after adding that fixture test. Production certificate and hostname validation remain strict.
- Thin Mac launcher analysis and all three tests pass. Package/sign/hash tests: 11/11; canonical shared asset check passes.

## Independent review fixes

Independent review identified installer ACL validation, reboot-persistent recovery discovery, fake-IP reuse after explicit disconnect, unused-interface DHCP handling, and DNS-only link-change recovery. The fixes reject uncertain/writable ACLs, use a durable root-owned marker, retain validated fake-IP allocation highwaters when clearing mappings, skip unused interfaces without usable IPv4, and rebuild paused bootstrap after verified DNS-only changes. The reviewed TLS/socket adapter and sign/hash packaging have no remaining important findings.

An additional integration regression first reproduced ordinary bootstrap after an unconfirmed Mac recovery. The Mac controller now blocks bootstrap and ordinary account traffic until recovery is known, even if no valid broker snapshot has established the previous mode. Its targeted suite (41 tests) and the final full Flutter suite pass. Normal system-proxy and Windows paths keep their existing transport behavior.

## macOS CI

Source commit: `dceb419ea6804a95891fc2ce01d8f5ab63bae3d9` on `codex/macos-client`.

[GitHub Actions run 42144670941](https://github.com/BlueGrassN/BlueGrass/actions/runs/42144670941), package version **0.1.0-dev+9**, minimum macOS **13.0**. Both jobs completed successfully: ARM64 `127653140241` and Intel `127653140453`. Both downloaded application packages passed offline verification.

The macOS workflow uses native Node.js 24 Actions: checkout v7.0.1 (`3d3c42e5aac5ba805825da76410c181273ba90b1`), setup-go v7.0.0 (`b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`), and upload-artifact v7.0.2 (`cf430e030ddbb5b0abf93d22962f4752f3646cd9`). The pinned Flutter action v2.23.0 is composite; its nested cache v5 also uses Node.js 24. Upstream action definitions were checked, and neither completed job log contains a Node.js 20 deprecation warning. No runtime-forcing or insecure Node.js opt-out flag is used.

Real macOS validation covers:

- Shared Dart business/UI/HTTPS tests, Mac launcher tests, analysis and package tests; native Go lifecycle tests and IPC race tests.
- Actual root kernel execution, without skips: `TestDarwinKernelProtectedCache`, `TestDarwinKernelUtunXrayTCPUDPDNSAndCleanup`, `TestDarwinKernelIsolatedAnchor`, `TestDarwinKernelOwnedTestHostRoute`, and `TestDarwinKernelReferenceRead`.
- Flutter/Xcode release builds, 12 Swift proxy-helper tests, Runner platform contract tests, and strict ad-hoc code-signature checks.
- Final packaged root bootstrap: writable-ACL rejection, durable intent without runtime, and stale-runtime recovery. These tests invoke the fixed installer through `sudo -n`, use the final signed worker and embedded SHA-256, and exchange authenticated Hello/Disconnect without connecting a resource.

Runtime communication is under `/Library/Application Support/BlueGrass/Run`, private state under `/Library/Application Support/BlueGrass/Tunnel`, and the durable recovery marker at `/Library/Application Support/BlueGrass/tun-intent.json`. CI confirmed that the system `/private/var/run` is group writable; the application uses its own root-owned directory and preserves strict owner/mode/ACL checks.

## Downloaded artifacts

| Architecture | GitHub artifact | Application ZIP | Bytes | SHA-256 |
| --- | --- | --- | ---: | --- |
| Apple Silicon / arm64 | [13469039208](https://github.com/BlueGrassN/BlueGrass/actions/runs/42144670941/artifacts/13469039208) | `BlueGrass-macOS-arm64-0.1.0-dev+9.zip` | 36,161,009 | `8cf046dc80b35287f145545d0b084bfc38de7942e943936ba632c01e27c91cbc` |
| Intel / x86_64 | [13470012903](https://github.com/BlueGrassN/BlueGrass/actions/runs/42144670941/artifacts/13470012903) | `BlueGrass-macOS-x86_64-0.1.0-dev+9.zip` | 39,844,171 | `0997c2cf577c77d058fb20b27ac515ddc9a019a3722aa0e2fe750dc753763e06` |

Downloads are kept locally under `Mac/dist/downloads-tun/<architecture>/`. The older `Mac/dist/downloads/` packages contain only system proxy support and are not this TUN build. GitHub artifacts have 14-day retention.

Offline verification checks the archive checksum against both `BUILD.json` and `SHA256SUMS.txt`, ZIP CRCs, bundle identity/version/minimum OS, executable permissions, Mach-O CPU/signature load commands, worker digest and the digest embedded in `BlueGrass.Native`, native source revision, and canonical shared brand assets. macOS CI additionally executes the native worker-info command and verifies signatures after extracting the final ZIP. Windows inspection does not itself execute or cryptographically validate Mach-O signatures.

Signed TUN worker SHA-256:

- arm64: `7769294def44f5fa6db9a8ae46a22370f4a6170322283e264b790064206d59c8`
- x86_64: `cbd11c9b0311366a81bcccc64bcc0e407f6a208f772258b6b4d033ebe620e35b`

Both packages use ad-hoc signing, without Developer ID or notarization. The administrator-authorized native TUN development mode does not require the user's Apple Developer certificate.

## User device acceptance

See [Mac testing instructions](../../Mac/docs/TESTING.md). Password-dialog UX/cancellation, active PF/global-route/DNS takeover with an actual resource, account API during TUN, TCP/UDP/DNS/IPv6/LAN policy, disconnect and crash recovery, network switch and sleep/wake require the user's Mac. Hosted runner tests deliberately use isolated routes/anchors and do not disrupt the runner's control connection. They do not replace interactive authorization or full-device networking acceptance.
