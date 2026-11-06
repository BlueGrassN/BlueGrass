# macOS development build verification — 2026-11-05

## Scope

User-approved workflow: develop on Windows, compile with GitHub Actions, and download the resulting application for user testing on a Mac. UI is shared directly with the Windows Flutter desktop package. The user has no Apple Developer certificate; this is an ad-hoc signed system-proxy development package. TUN/Network Extension, notarization, App Store distribution, and Mac device acceptance are not claimed.

Implementation is isolated in branch `codex/macos-client` of private repository `BlueGrassN/BlueGrass`. The original `E:/workspace/APP` working copy is preserved. Previously untracked shared desktop/native sources were imported into the build branch because the Mac build depends on them.

## Local verification

- Flutter 3.47.7 / Dart 3.13.5; Go 1.27.2; Windows host.
- Shared desktop Flutter analysis passed and initial complete suite passed 407 tests. After adding the Mac-specific timeout policy, the 142 relevant native/transport/platform regression tests passed; Windows retains its 45-second default, Mac uses 90 seconds, and explicit caller overrides remain honored.
- Thin Mac app analysis passed; all three production-launcher, license and canonical-asset tests passed.
- Shared data-source package passed all 179 tests after correcting four old assertions: valid TUIC/AnyTLS metadata remains displayable, while connection preparation rejects these unsupported protocols with `ELIG-008` as required by ADR 0049.
- Go nativehost, macproxy, localengine, protocolconfig, routingpolicy, control, entrydns and diagnostics test targets passed. Relevant proxy/host race and vet checks passed. Host and Darwin test binaries cross-compiled for arm64 and amd64.
- Packaging validation tests passed; metadata/plist/XML parsed; copied brand assets match canonical shared bytes. No token/private-key markers or prohibited credential file paths were found in staged sources.
- Two shared-UI previews were rendered and inspected in light/dark mode using synthetic fixtures and macOS platform policy on Windows. These previews substitute a Windows Chinese font and do not establish real Mac rendering or networking acceptance.

## Review findings resolved

- Proxy restoration groups dependent fields, preserves externally changed endpoints/exclusions, restores independently owned endpoints, and does not reactivate obsolete PAC over a newly selected external proxy. Remaining ambiguous loopback endpoints retain the journal and return `RECOVERY_REQUIRED`; Disconnect/Close/repair cannot falsely acknowledge cleanup.
- Whole-operation proxy budgets include lock waiting and teardown: Apply/Recover 25 seconds, State 30 seconds, cleanup 3 seconds. A cancellation pipe and parent-death/watchdog checks stop the Swift helper after host death or deadline expiry, including checks after interactive authorization/Keychain waits and before mutations. The Mac Dart timeout provides additional margin. Regression tests and independent targeted review cover these fixes.

## GitHub verification

Initial run [41983956836](https://github.com/BlueGrassN/BlueGrass/actions/runs/41983956836) exposed the four obsolete protocol assertions before native application compilation. It was stopped, the assertions were corrected, and a new build was started.

Verified code revision: `f0e981d19d42c4009d4c5fe4cb4b228633e62c02`.
Successful run: [41986960439](https://github.com/BlueGrassN/BlueGrass/actions/runs/41986960439).

Both `macos-15` (arm64) and `macos-15-intel` (x86_64) completed successfully. Each passed analysis, shared business/platform/launcher tests, the selected Go targets, real Flutter/Xcode application compilation, 12 Swift helper tests, release-helper compilation, `lipo` architecture validation, ad-hoc code signing, `codesign --verify --deep --strict`, Xcode RunnerTests, and artifact upload. The RunnerTests target contains 13 unhosted tests; it does not launch the production Flutter app or mutate the runner's network settings.

The preceding run compiled the ARM app and helper successfully, then exposed an incorrect argument order in the packaging script's `lipo -verify_arch` invocation. The final revision fixes that invocation; the complete pipeline was rerun on both architectures.

## Verified packages

Version: `0.1.0-dev+3`. Minimum macOS: 13.0. Archives downloaded from the successful run were independently checked on Windows: manifest SHA-256, ZIP CRC, bundle identifier/version, executable permission bits, CPU architecture, code-signature load commands, and canonical brand asset bytes. All four executable programs are present: BlueGrass, BlueGrass.Native, BlueGrass.Diagnostics, BlueGrass.ProxySettings. Their Mach-O minimum OS is 13.0; FlutterMacOS/App frameworks require 12.0. No Windows executables or signing credentials are packaged.

| Architecture | Application ZIP bytes | SHA-256 | GitHub artifact |
| --- | ---: | --- | --- |
| Apple Silicon / arm64 | 24,031,177 | `a2546e611dbab4a731892a39c5f515ef65c6bcc452f9733c34df18707af00338` | [Download](https://github.com/BlueGrassN/BlueGrass/actions/runs/41986960439/artifacts/13396147805) |
| Intel / x86_64 | 26,471,813 | `36d4e0db7df9f6aaa19d233d6b51d78f11ad6b5c381599675f46830f9ea5d228` | [Download](https://github.com/BlueGrassN/BlueGrass/actions/runs/41986960439/artifacts/13397382404) |

Local copies are under `Mac/dist/downloads/{arm64,x86_64}/` in this development worktree. Each contains the application ZIP, BUILD.json, SHA256SUMS.txt, TESTING.md and a local VERIFIED.json. GitHub artifacts have a 14-day retention period; the downloaded local copies remain available. The table hashes are for the inner application ZIP, not GitHub's outer artifact container.

## Device acceptance

The user will perform installation, actual login/network/proxy restoration, menu/window behavior, Keychain permissions, login-item registration, sleep/wake and network-change checks on a Mac. Follow [the test-package instructions](../../Mac/docs/TESTING.md). Forced process death and interactions with managed/external proxy configurations require device confirmation. No live production account credentials were used by this development session.
