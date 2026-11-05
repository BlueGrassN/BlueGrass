# macOS Client Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans; independent platform implementations may run in parallel with superpowers:dispatching-parallel-agents.

**Goal:** Deliver a GitHub-built Mac development package with the shared BlueGrass desktop UI and a real Xray system-proxy backend.

**Architecture:** A thin Mac Flutter app imports the existing desktop presentation package. Native Swift implements the shared method channel. The Go native host adds a Darwin proxy backend and retains existing IPC/session semantics.

**Tech Stack:** Flutter 3.47.7, Dart 3.13.5, Go 1.27.2, Swift/AppKit, GitHub Actions.

**Spec:** ../specs/2026-11-05-macos-client-design.md

## Global Constraints
- UI is shared with the existing Windows implementation; Windows behavior must remain covered by regression tests.
- Use only the pinned Xray core. No additional proxy engines.
- Development test package; no certificates, notarization, signed TUN, or Mac device acceptance claimed.
- Preserve current user work in E:/workspace/APP; implementation is isolated.
- Do not include credentials, test accounts, build caches, or unrelated service edits in commits or artifacts.

## Review Focus
- System proxy restoration after partial application, competing writers, process death, or user disconnect.
- Keychain errors and malformed input must fail clearly without returning fake success.
- Mac launcher must not launch `.exe` helpers or the Windows updater.
- UI uses the same pages in light/dark/narrow layouts and exposes accurate platform capabilities.
- CI includes all assets/helpers and executable permissions for each advertised architecture.

## Tasks
1. [ ] Add a Mac launcher importing the shared desktop UI; introduce narrowly scoped platform capabilities and add tests for labels, unsupported TUN, helper resolution, and update behavior. Run shared Flutter analysis and tests.
2. [ ] Add the Darwin Go system-proxy backend, ownership/recovery tests, native-host wiring, and Darwin helper compilation. Preserve existing native protocol contracts.
3. [ ] Implement Swift Keychain, preferences, window/menu lifecycle, login items, URL/file actions, notifications, and native contracts; add native macOS tests and build them in CI.
4. [ ] Add pinned-toolchain GitHub Actions, scripts for assets/helpers/signing/package metadata, and artifact test instructions. Validate locally and run on GitHub.
5. [ ] Review the integrated change, resolve build/test failures, verify downloadable artifacts, and record exact evidence and remaining device tests.
