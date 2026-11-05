# macOS native Flutter host

The host lives in `Mac/app/macos/Runner` and implements `bluegrass/platform` for the shared desktop Dart application. It uses public AppKit, Security, UserNotifications, and ServiceManagement APIs. The development product is `BlueGrass.app/Contents/MacOS/BlueGrass`, module `BlueGrass`, bundle identifier `com.sunipip.bluegrass.macos`, minimum macOS 13.0. Signing is manual/ad hoc with no development team. App Sandbox and hardened runtime are disabled for this development build; the separate Go/system-proxy helpers retain responsibility for networking and proxy ownership.

**Verification status:** implementation and project structure were inspected on Windows. Xcode, Swift/Apple SDK compilation, RunnerTests execution, real Keychain access, notification authorization/delivery, login-item registration, and Mac UI acceptance remain unverified until macOS CI/device testing. A Windows structural check is not Apple compilation evidence.

## Existing channel contracts

| Method | Arguments | Result / behavior |
| --- | --- | --- |
| `readRecord` | `{key: String}` | `String?`; missing item is null, unavailable/locked/corrupt storage is an error. |
| `writeRecord` | `{key: String, value: String}` | Null after the complete UTF-8 record has been replaced in Keychain. |
| `deleteRecord` | `{key: String}` | Null; missing item is an idempotent success. |
| `openUrl` | `{url: String}` | Null after NSWorkspace accepts opening an HTTPS URL; other schemes, credentials, control characters, empty hosts, and invalid ports are rejected. No shell execution. |
| `setWindowChrome` | `{caption: int, text: int, dark: bool}` | Null after applying the appearance and RGB background/title-bar fill. `caption` and `text` must be unsigned 32-bit ARGB values. AppKit controls accessible native title text; the validated Windows text color is not separately forced. Dart computes light/dark when the system theme changes. |
| `setTrayState` | `{label: String}` | Null; updates the menu-bar status line and tooltip. No parsing translated labels to infer connection state. |
| `getAutostart` | None | True only for `SMAppService.mainApp.status == .enabled`; registration requiring approval is not reported as enabled. |
| `setAutostart` | `{enabled: bool}` | Null only after the requested state is observed. Errors include `startup_approval_required` and `startup_failed`. |
| `submitExpiryNotification` | `{id: String, title: String, body: String}` | Bool; true means the OS accepted a local notification request, not that a banner was displayed/read. Denial, API failure, queue saturation, or cancellation returns false. |
| `cancelExpiryNotification` | `{id: String}` | Null after requesting removal of the owned pending/delivered notification and queued activation. Idempotent. Queue saturation is an explicit `busy` error. |
| `takeExpiryNotificationActivation` | None | Pops a retained notification ID or null. The in-memory queue retains at most 32 distinct IDs, dropping the oldest on overflow. |
| `exit` | None | Dart's explicit cleanup acknowledgement; replies null and schedules `NSApp.terminate`. The host never infers cleanup success from an `exitRequested` callback returning. |

The native menu sends `toggleConnection` without arguments to use the shared controller. Quit from the menu bar, Cmd-Q, the Dock, or the OS sends `exitRequested` without arguments. The existing Dart handler awaits `controller.disconnect()` and sends `exit` only on success. Failed/missing Dart handling leaves the app running and the window accessible; a later Quit can retry. There is no timeout-based force exit. An in-flight Quit is deduplicated. OS logout/shutdown can therefore be cancelled while cleanup is unavailable; externally forcing termination cannot be intercepted.

The red Close button and Cmd-W hide the window; they do not disconnect or destroy the Flutter engine. Open BlueGrass in the status menu or click the regular Dock icon to reopen. The Dock icon remains available if the user hides the menu-bar item. The menu icon uses the application's BlueGrass icon. The asset catalog contains an exact byte copy of `Mac/assets/branding/app-icon-1024.png` in the macOS 512pt/2x slot; no replacement artwork was generated.

Notification clicks send `expiryNotificationAvailable` after placing an ID in the activation queue. Dart's startup drain also recovers clicks received before its handler is installed. Only the default click action on this app's owned identifier prefix is accepted. The existing shared coordinator still validates the account and resource ledger before navigation. Notifications use stable IDs; updates replace the same pending/delivered notice. Authorization/add/cancel operations are serialized and bounded to 64. Cancelling while authorization is pending invalidates that submission before publication. A cancellation racing an already submitted OS request removes it again when the add completion arrives. Authorization is requested only on submit, never on host initialization. No push entitlement, APNs registration, or network request is involved.

## Additional integration contracts

These explicit methods have no current Windows Dart caller. The launcher/shared platform adapter can use them without a new plugin:

| Method | Arguments | Result |
| --- | --- | --- |
| `saveFile` (alias `exportFile`) | `{suggestedName: String, content: String}` | Native `NSSavePanel`; UTF-8 export returns the selected absolute path only after writing, or null on user cancellation. |
| `showWindow` | None | Null after bringing the window forward. |
| `getAutostartStatus` | None | `enabled`, `notRegistered`, `requiresApproval`, `notFound`, or `unknown`. |
| `getPlatformCapabilities` | None | Implemented API flags: `keychain`, `nativeSaveDialog`, `menuBar`, `loginItemAPI`, `localNotificationAPI`; false for `tun`, `automaticUpdater`, and `controlHttp`. API availability does not imply OS permission. |

The save dialog accepts a single suggested leaf name up to 200 UTF-8 bytes, with no slashes, colon, dot/dot-dot, or control characters. Content is limited to 8 MiB. Only one dialog/export is active at a time; other calls receive `busy`. The name's known extension constrains the panel's content type. Exports are written in a private temporary directory beside the chosen destination, restricted to mode 0600, then renamed atomically over the destination. A failed write/rename preserves the old destination and removes the temporary directory. Replacing a symlink replaces the link itself without overwriting its target. Paths/content are not logged. The shared crash-report UI currently writes to its own temporary export directory; wire its export callback to `saveFile` to use this dialog. Treat a null result as cancellation.

The Windows-only `controlHttpConfigure`, `controlHttpSend`, and `controlHttpCancel` calls return `unsupported_capability`, as do explicit `installUpdate` and `enableTun` requests. Unknown methods return Flutter's not-implemented result. **The macOS launcher/controller must select its supported control-plane transport, disable the Windows update client/helper, and gate TUN.** Do not run the unchanged Windows transport bootstrap on macOS: it unconditionally invokes `controlHttpConfigure`. No native transport or updater is silently substituted here. No Dart, Go, `Mac/native`, CI, plan, or pubspec files are changed by this host implementation.

## Storage and errors

Records are generic-password items in the current user's login Keychain, scoped by service `com.sunipip.bluegrass.macos.records` and account equal to the validated key. Keys use the shared Windows grammar `[A-Za-z0-9_-]{1,100}`. Values are bounded to 1 MiB of UTF-8; empty strings are valid. Items are non-synchronizing, with unlocked/this-device-only accessibility on insertion; there is no plaintext fallback, access group, or iCloud synchronization. This development build deliberately uses the ordinary login Keychain rather than requiring a signed Data Protection Keychain access group. Keychain ACL access can vary across replacement ad-hoc builds; denied/locked access is an error rather than a claim of a restored session. Authentication UI is disabled for record operations; the app must surface storage failure rather than hang waiting for authorization.

One serial worker handles record operations; at most 32 are outstanding. Writes use `SecItemUpdate`, add only if missing, and retry an update if another process wins the insertion race. The implementation never deletes the old record to replace it. Delete cannot target another service or arbitrary path. Failed updates preserve the previously complete record. All Flutter replies return on the main queue.

Errors contain fixed codes and fixed messages only, with no record keys, payloads, URLs, filesystem paths, Foundation errors, or OS error descriptions. No native diagnostic logging is added. Bounds apply at the native boundary even if Dart validates first. Boolean values do not accept numeric coercion; colors do not accept booleans, floats, negative numbers, or values beyond UInt32.

`SMAppService.mainApp` operates on this app only; no LaunchAgent plist or other application's startup setting is modified. Registration may require user approval under System Settings → General → Login Items, and ad-hoc/replaced/moved apps may fail registration. A `requiresApproval` result remains registered/pending, but `setAutostart(true)` returns `startup_approval_required` and `getAutostart` remains false. `getAutostartStatus` exposes the pending state. Disabling unregisters only this app asynchronously and verifies the resulting status. Calls are not made at startup merely to reconcile preferences. Install the app at a stable location before testing login behavior.

## CI invocation and test isolation

From `Mac/app`, first run `flutter pub get` and the normal Flutter macOS build to generate Flutter settings and plugin package references. The build may use `FLUTTER_XCODE_CODE_SIGNING_ALLOWED=NO` and `FLUTTER_XCODE_ARCHS=$arch`, followed by the packaging script's explicit ad-hoc signing of helpers/frameworks/app and strict verification. Those build-only environment overrides do not have to be applied to the test invocation.

Run on macOS with Xcode selected:

```sh
xcodebuild test \
  -workspace macos/Runner.xcworkspace \
  -scheme Runner \
  -configuration Debug \
  -destination 'platform=macOS' \
  -derivedDataPath build/native-tests \
  CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= \
  MACOSX_DEPLOYMENT_TARGET=13.0 -quiet
```

The provided CI invocation is correct. `RunnerTests` is an **unhosted logic-test bundle**, with empty `TEST_HOST` and `BUNDLE_LOADER` in all three configurations. It directly compiles `PlatformContracts.swift`, `KeychainRecordStore.swift`, `ExpiryNotifications.swift`, and `AtomicFileExport.swift`. The scheme builds Runner as a dependency to check host compilation but **does not launch `BlueGrass.app` for tests**. Thus there is no shared Dart startup, saved auto-connect setting, proxy/network change, startup registration, live Keychain access, notification authorization prompt, or cleanup-gated Flutter process for XCTest to terminate. No test-only bypass is added to production quit handling. Runner's product references and scheme buildable names are `BlueGrass.app`, and its nib classes resolve to the `BlueGrass` Swift module.

Tests exercise malformed/oversized arguments, strict wire types, URL schemes and credentials, failed-disconnect retry and cleanup acknowledgement, bounded activation queues, atomic Keychain update failure and insertion races through a controlled backend, corrupt stored bytes, actual temporary-file export/symlink replacement/permissions/failure cleanup, and notification denial/cancellation during authorization through a controlled backend. Temporary export tests use XCTest-owned UUID directories and clean them up. OS permissions/login-item success and a real Flutter disconnect are device acceptance checks, not unit-test claims.

For interactive Mac acceptance, launch the packaged development app separately after XCTest finishes: verify record persistence/replacement, system/light/dark appearance, Close/Open/Dock reopen, tray connect/disconnect, failed-disconnect Quit retry, and successful Dart cleanup before Quit. Test notification denial and a real click after opening the installed app; verify login-item registration/status/disable at a stable app path. Confirm TUN/updater are disabled and the launcher uses the intended transport. Keep CI compilation/test results separate from those Mac UI/network acceptance results.

API references: [SMAppService registration](https://developer.apple.com/documentation/servicemanagement/smappservice/register()), [asynchronous unregister completion](https://developer.apple.com/documentation/servicemanagement/smappservice/unregister(completionhandler:)), [notification authorization](https://developer.apple.com/documentation/usernotifications/unusernotificationcenter/requestauthorization(options:completionhandler:)).
