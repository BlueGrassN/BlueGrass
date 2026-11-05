# Shared desktop UI

`Mac/app` is a thin launcher. Its `bluegrass_client` path dependency is
`../../Windows/app`; `lib/main.dart` invokes that package's production `main`.
Pages, navigation, components, controllers, resource/API code, theme colors and
design sizes remain in the existing package. There are no copied Mac screens and
no fixture imports in the launcher. Test fixtures are confined to tests and the
existing explicit Windows fixture entry point.

`Windows/app/lib/desktop_platform.dart` chooses macOS from Flutter's target
platform; tests override the target explicitly. The remaining desktop behavior
retains Windows defaults. macOS uses PingFang SC at the shared design sizes,
Keychain labels, macOS login-item wording and menu-bar window instructions.
Existing `WindowsSessionStore`, reminder, autostart and crash-store class names
are retained for compatibility. The native host must implement their shared
`bluegrass/platform` methods with actual Keychain and macOS facilities.

## Capabilities and transport

- macOS TUN switches stay visible, disabled and off, with the reason
  “开发版本暂不支持 TUN，请使用系统代理模式。” Restored TUN preferences normalize to
  system proxy in memory; the next ordinary settings save persists that mode.
  Programmatic preference updates, direct controller connection attempts and
  native-client TUN startup/requests are rejected before a helper starts.
- Neither the Windows updater nor its presentation/background lifecycle is
  created on macOS. Even an injected updater is ignored by the app, and direct
  `UpdateClient` calls reject every operation before spawning. About explains
  manual download and replacement of development builds.
- Production macOS API requests use `IoHttpTransport`, with normal certificate
  verification and an explicit DIRECT path by default. This avoids depending on
  the system proxy the app owns. The fixed anonymous HTTPS bootstrap also uses
  the existing bounded direct transport, validates/caches the API origin and
  retains the production fallback. macOS never invokes unsupported
  `controlHttpConfigure`, `controlHttpSend` or `controlHttpCancel`. Windows
  retains `ControlHttpTransport` and its protected socket path.
- `BlueGrass.Native` and `BlueGrass.Diagnostics` resolve beside the main
  executable in `Contents/MacOS`. Only macOS drops `.exe`; Windows retains its
  existing adjacent executable names and arguments.
- Crash summaries identify macOS and survive protected queue reload. Their
  explicit export invokes `saveFile` with `suggestedName: crash-reports.json`
  and the sanitized preview as `content`; a null result means user cancellation.
  macOS does not use the Windows temporary-directory export. The diagnostics
  page's existing clipboard action remains shared.
- The shared entry point registers Xray and sing network-utility licenses on
  both platforms and omits Wintun registration on macOS.

## Assets and build integration

Run from the repository root before testing or compiling the Mac app:

```text
python Mac/tools/sync_assets.py
python Mac/tools/sync_assets.py --check
```

The script uses only Python's standard library and works from any working
directory. It copies the canonical `Windows/app/assets/branding/**/*.png`
(including 4.0x variants), `LICENSE.Xray-core` and `LICENSE.sing` byte-for-byte to
`Mac/app/assets`. Generated copies are ignored by `Mac/app/.gitignore`.
`--check` never writes and fails on missing/differing copies. The Mac pubspec
declares the root asset keys that the shared UI loads.

There are **no required macOS Dart defines**. Use the production `lib/main.dart`
entry point. Optional shared metadata defines are `BLUEGRASS_VERSION` and
`BLUEGRASS_BUILD`; keep them consistent with the packaged helpers/metadata.
The default API is the real production API; do not replace it with a fixture
entry point or set `BLUEGRASS_API_URL` to a test endpoint. `BLUEGRASS_API_PROXY`
is the existing optional explicit local HTTP proxy override, not a build
requirement. Do not enable it in normal development artifacts.

The host/build owners must set the macOS deployment target to at least 13,
provide the shared method channel, place the executable Go helpers in
`Contents/MacOS`, and preserve executable permissions. This UI change does not
implement Swift, Go, CI, signing or packaging. Development builds need no Apple
Developer certificate; no notarization or device acceptance is implied.

## Verification

Using Flutter `E:/tools/flutter` on Windows:

```text
cd Windows/app
flutter analyze --no-pub
flutter test test/macos_platform_test.dart --no-pub --reporter expanded
flutter test --no-pub --reporter expanded
```

The Mac platform tests explicitly exercise macOS and Windows branches. They
cover production bootstrap/API login without native control HTTP, save-file
success/cancellation, helper adjacency/names, TUN guards, crash queue platform,
shared navigation in narrow/wide light/dark layouts, labels/fonts and updater
startup/resume exclusion. The complete existing Windows Flutter suite remains
the Windows regression gate; run it on Windows, where its process fixtures and
Windows helper expectations apply.

After asset synchronization, from `Mac/app`:

```text
flutter pub get
flutter analyze --no-pub
flutter test --no-pub --reporter expanded
```

The Mac app tests verify the actual production launcher and engine license
registration, shared app type identity, and every copied asset against its
canonical bytes. These Dart/widget checks run on Windows and macOS; they do
not prove native Keychain/login-item integration, real Mac network behavior or
an Xcode build. GitHub compilation and the user's Mac tests own those checks.

Verified on Windows on 2026-11-05 after the transport/export integration:

- `Windows/app`: analysis clean; full `flutter test --no-pub --reporter expanded`
  passed **407 tests** (1 minute 32 seconds), including the **15** tests in
  `test/macos_platform_test.dart` (also passed separately).
- `Mac/app`: analysis clean; `flutter test --no-pub --reporter expanded` passed
  **3 tests**.
- `python Mac/tools/sync_assets.py --check` passed. An isolated execution of
  `sync_assets` also verified missing-file detection, nested variants, exact
  bytes, non-writing check mode, drift detection and repair.
- A separate author review compared the modified shared sources with the
  original workspace. No independent reviewer or native Mac acceptance is
  claimed by this worker.

## Files changed by the shared-UI worker

- `Windows/app/lib`: `desktop_platform.dart`, `app_controller.dart`,
  `app_theme.dart`, `main.dart`, `native_client.dart`, `diagnostic_client.dart`,
  `platform_store.dart`, `user_lifecycle.dart`, `update_client.dart`,
  `crash_reports.dart`, `crash_report_settings.dart`,
  `pages/{login,home,settings,about}_page.dart`.
- `Windows/app/test/macos_platform_test.dart`.
- `Mac/app`: `lib/main.dart`, `test/widget_test.dart`, `pubspec.yaml`,
  `pubspec.lock`, `analysis_options.yaml`, `.gitignore`.
- `Mac/tools/sync_assets.py` and this document.

All changes are left uncommitted for the lead worker to integrate.
