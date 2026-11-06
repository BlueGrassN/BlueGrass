# Android FlClash-style frontend

User authorized implementation directly: align Android frontend with latest FlClash. Reference checked 2026-11-03: FlClash v0.9.2, commit 7fd417fb9a9a277c6a9339a8ac87f907df7c25f7. Official release, snapshot and UI sources were inspected. Implement the visual structure in the existing Flutter app using our own widgets; retain BlueGrass identity and existing Xray/controller/native APIs.

## Design

- Rounded, tonal mobile surfaces: 34px dashboard cards, 20–24px inset surfaces, grouped settings, 16px page margins, soft blue accent in light/dark themes.
- Floating pill dock: dashboard, proxies, settings, diagnostics. Preserve page state, accessibility, safe insets and existing navigation keys. Use actual product destinations rather than unsupported subscription-editor pages.
- Dashboard: status/actual exit/power hero, selected vs active resource identity, resource details, routing selector, diagnostic action. No invented speeds, delay, connection counts or IP quality.
- Proxies: compact selectable cards, selected indicator, search, resource scopes/pagination/details and protected active-resource semantics.
- Settings: grouped rounded cards with account, appearance, routing and rule auto-update; preserve all actions/errors/polling. Login and diagnostics use matching surfaces.
- No core/API/account/credential changes. ARM64-only compressed Release packaging remains mandatory. Preserve old APK. No commits or publication.

## Implementation

1. Main: theme, shared UI primitives, shell/dock and home. Verify primary connection controls are visible on a 390x844 viewport and all destinations are reachable.
2. Parallel bounded task: resources and login styling; retain queries, selection, resource details and all auth safeguards.
3. Parallel bounded task: settings, diagnostics and rules card styling; retain model lifecycle and status/error semantics.
4. Run Flutter analyze and full tests. Exercise narrow screens/large text, switching, service-event connection states, routing locks, update failures and identity isolation. Produce and inspect rendered light/dark previews from the real widgets with explicitly synthetic fixture data.
5. Independently review changed UI for behavior regressions. Build normal ARM64 Release; check ABI/version/signature/alignment and artifact size/hash. Current x86 emulator cannot run the ARM64-only native library; do not change distribution ABI or silently claim device acceptance. Keep Android Studio/emulator closed as requested.

## Review focus

Status must come from native events; pending selection cannot inherit active exit. Overlay dock must not hide last controls. Big text and keyboard must remain usable. Settings polling must stop when inactive/disposed. Rule updater, routing defaults, persistence and controller guards remain effective.

## Execution record

Implementation completed across theme/shell/dashboard, compact resources/login, and settings/diagnostics/rules display. Independent review found three interaction regressions; each was reproduced and fixed: cancel during resource preparation, SnackBar overlapping navigation, and last-page controls behind a tall safe-area/dock combination. Final review has no remaining findings. Detailed commands, rendered previews and artifact verification are in Android/docs/flclash-ui-verification-20261103.md. Existing untracked implementation required preserving local sources; edits stayed in place with a pre-change copy under Android/.artifacts/flclash-ui-20261103/lib-before. No commits or publication.
