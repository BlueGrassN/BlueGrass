# macOS build dependency refresh implementation plan

> **For agentic workers:** Use superpowers:executing-plans to implement this bounded maintenance task inline.

**Goal:** Update every dependency reported as outdated by macOS build 42144670941 to its latest stable release, then deliver verified replacement packages.

**Architecture:** Upgrade the four existing pub dependency resolutions and pin CI to Flutter 3.47.9. Flutter still pins material_color_utilities and test_api; use identical, exact overrides in both desktop application roots, with existing full regressions and actual macOS CI as compatibility gates.

**Tech stack:** Flutter 3.47.9 stable, Dart, existing Go/Xcode macOS packaging and Node.js 24 Actions.

**Spec:** User request in this chat: update all dependencies advertised as newer in GitHub compilation. Preserve shared Windows/Mac UI and administrator-password TUN.

## Constraints and review focus

- Use latest stable package versions verified against pub.dev on 2026-11-06; no prerelease channel.
- Do not edit the shared installed Flutter SDK; use a separate 3.47.9 checkout for local validation.
- Keep Windows and Mac root overrides identical because dependency overrides are root-scoped.
- Verify real resolved versions with pub outdated and lockfiles; do not hide update warnings.
- Verify SDK-pinned test internals through complete Flutter regressions, themes/layout through existing platform tests, and artifact hashes/signatures through existing packaging checks.
- Preserve previous build+9 packages until replacements are verified. Branch push/build is already authorized; do not merge main.

## Task 1: Resolve, validate and deliver dependency updates

**Files:** `.github/workflows/macos-dev.yml`, `Windows/app/pubspec.yaml`, `Mac/app/pubspec.yaml`, and `pubspec.lock` in those roots plus `Windows/packages/domain` and `Windows/packages/data_source`. Record evidence in `docs/development/`.

- [x] Extract 14 outdated names from the completed CI log; verify registry versions and Flutter stable tag.
- [x] Prepare isolated Flutter 3.47.9; add exact overrides material_color_utilities 0.13.1 and test_api 0.7.15 to both applications; upgrade all four lockfiles.
- [x] Run pub outdated in all four roots. Every reported current version equals latest stable, including overrides.
- [x] Run both app analyzers, full Windows shared Flutter tests, Mac launcher tests, domain/data_source Dart suites and package Python tests. Analysis clean; 437/3/164/179/11 tests passed, respectively.
- [x] Review the dependency/configuration diff independently; no material findings. Added a CI check after packaging to prove all four resolved lockfiles match the reviewed commit.
- [x] Commit/push, follow both macOS CI jobs through package upload, verify logs have no available/newer dependency messages, download and verify both new archives. Run `42148986910` succeeded for source `7e2221e02f5279724724719c1d03d2ed255dfbb9`; both `0.1.0-dev+10` archives verified.
- [x] Record exact versions, successful run, hashes, artifact links and remaining actual-device acceptance in [the verification record](../../development/macos-dependency-refresh-20261106.md); deliver the final documentation commit and report results.

## Evidence and decisions

- Baseline: `6c20f5744b260c7a8b0e0b36cf1c287cc63d71a0`; clean feature worktree.
- Flutter stable ref and tag 3.47.9 both resolve to `c197ca5199e9ee5661e865596c9c07294dfab51c`.
- Ruling: two SDK-pinned dependencies need explicit root overrides to satisfy the user's all-latest request. Upstream changelogs show a hue calculation fix and additive test APIs; full tests and dual-architecture CI remain required before acceptance.
- New implementation tests are unnecessary for a dependency-only refresh; use existing meaningful regression and packaging coverage.
- Final verification: all local regressions and both CI architectures passed. Neither completed log contains dependency/SDK update notices or Node.js 20 warnings. CI confirmed committed lockfile equality; downloaded package metadata, hashes, architectures and shared assets match. Full user-device networking/interactive authorization acceptance remains separate.
