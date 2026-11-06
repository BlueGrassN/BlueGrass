# macOS development build

## User decisions
- Develop on Windows and compile with GitHub Actions; the user tests downloaded artifacts on a Mac.
- Keep UI unified across BlueGrass clients. The current Windows Flutter desktop UI is the desktop reference.
- No Apple Developer account or certificates are currently available; deliver a development test build first.

## Approach
Reuse `Windows/app` as the desktop presentation package from a thin `Mac/app` Flutter launcher. Share pages, navigation, themes, components, account/resource controllers, and Dart domain/data packages. Adapt platform labels, capabilities, helper executable paths, and native method-channel implementation. This prevents independent copies of the UI from drifting. Copying all screens or reimplementing them in SwiftUI would add divergence and maintenance without helping the user's requirement.

Reuse the pinned Go/Xray implementation and its private JSON-lines native-host protocol. Add a macOS system-proxy backend with explicit ownership, rollback, recovery, and bounded operations. Use Keychain for application records, native macOS dialogs and lifecycle hooks, and a macOS login-item implementation. Never store login passwords, account tokens, or node secrets in argv or diagnostic output.

## Deliverable
- A real macOS desktop app: API login, resource lists/refresh, shared connection UI, Xray local/system proxy, traffic/exit reporting, settings, diagnostics, and correct native lifecycle.
- GitHub Actions workflow building the app and its Go helpers for Apple Silicon and Intel, packaging `.app` into downloadable ZIP artifacts with build metadata and checksums.
- Development signing only (ad hoc), clearly described in the artifact instructions. No claims of notarization, App Store approval, or Mac device acceptance.
- TUN capability is explicitly unavailable in this build: there is no signed Network Extension, and the UI must not imply it can enable one. Preserve the documented Network Extension architecture for subsequent signed builds.
- Windows updater integration is disabled on macOS; manual replacement of the development app is explicit. No accidental Windows update download or execution.

## Implementation calls
- Use the installed shared toolchain versions (Flutter 3.47.7 / Dart 3.13.5, Go 1.27.2) unless the CI environment demonstrates a compatibility issue.
- Build macOS 13+ for arm64 and x86_64, subject to the pinned Flutter SDK's minimum supported version.
- Use the private existing repository `BlueGrassN/BlueGrass`, branch `codex/macos-client`; do not merge into main automatically.
- Work in an isolated worktree. Carry only the required current uncommitted client sources into it; preserve the original workspace.

## Verification
Run shared Flutter analysis/tests, Mac-specific contract/UI tests, Go host/proxy unit tests and cross-compilation checks on Windows. Run real macOS compilation, native tests, package checks, and smoke checks on Actions. Clearly separate those results from user-owned Mac UI/network testing. Validate restoration, external settings changes, native failure, launch/exit, missing helper, and unsupported TUN behavior. Record the workflow run, artifact links, toolchains, architecture, and SHA-256.
