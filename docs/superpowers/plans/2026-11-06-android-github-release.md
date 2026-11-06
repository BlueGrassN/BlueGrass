# Android local and GitHub Release build

**Goal:** Keep a local ARM64 APK build and automatically build and publish an APK after pushes to main.

**Architecture:** Reuse the Windows PowerShell native builder on windows-2025, with a shared release script for local and hosted builds. The build job has read-only repository permissions; a separate publishing job creates an immutable per-run Android Alpha prerelease from verified artifacts. PR builds never receive signing secrets or publish releases.

**Constraints:** Preserve ARM64-only compressed native libraries, Xray pins, application identity, routing and traffic behavior. Checkout the complete repository: Android depends on Windows/packages and Windows/native/core. No Studio/emulator launch, commit or push is needed to implement and validate this change.

**Decisions:** Pushes to main and manual runs on main publish; PRs validate. Use Flutter 3.47.7, Go 1.27.2, JDK 17, SDK/build-tools 36 and NDK 28.2.13676358. Hosted release builds require a persistent keystore supplied using four Actions secrets; never silently use a fresh hosted debug key for a Release. Local builds without signing configuration continue to use the existing debug key. CI versionCode is 100000 + run_number; local builds default to pubspec's versionCode, with an override available. Every release contains APK, SHA256SUMS and metadata.

## Implementation and validation

- [x] Add environment-based Gradle signing, rejecting incomplete configuration and enforcing configured signing for publication.
- [x] Add a shared local/CI build script: fresh native build, locked Dart dependencies, analysis, Flutter/Go tests, optimized APK, signature/ABI/alignment checks and named distribution files.
- [x] Add main-push, PR and manual Actions workflow; stable signing preflight, temp-key cleanup, artifact transfer and publication only after success.
- [x] Document repository structure, local command, secrets, versioning, signing compatibility and download location.
- [x] Run PowerShell/workflow validation, actual local build/package checks and focused independent review. Clearly distinguish local success from a future hosted run.

## Review focus

Fresh checkout without AAR/local.properties/Gradle wrapper; PR secrets isolation; fixed certificate and monotonic versionCode; failed/stale artifacts cannot be published; reruns/concurrent pushes must never overwrite another build's assets or tag.

## Review results

- Preserve upstream license bytes through Git attributes: reproduced 10 checksum failures after a Windows clone, then verified all 10 hashes through Git checkout filters with the fix under core.autocrlf=true.
- Export the producing build's artifact name as a job output; retrying only failed publication consumes that output rather than the new attempt's nonexistent artifact.
- Unique main-run concurrency groups preserve pending pushes; PR-number groups still cancel superseded checks.
- Go readonly test/build and locked dependency diff replace the extra `go mod verify` command, which incorrectly sought an archive for the repository's locally replaced module. Ordinary go.sum download verification remains enabled.
- Local APK metadata marks uncommitted source as dirty, and publication requires a clean source checkout.
