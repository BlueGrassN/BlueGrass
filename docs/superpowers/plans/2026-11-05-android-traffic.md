# Android traffic statistics

Implement the requested upload/download rates and three totals (combined/upload/download) on the dashboard, using ADR0041's accepted per-connection proxy-business payload scope. Retain the current FlClash styling. No billing API or per-destination history.

1. Go: count successful payload reads/writes only on the business proxy dial path, excluding direct/DNS/probe/updater transports. Expose cumulative decimal counters via Session.Traffic(). Preserve partial-success accounting, concurrency and close synchronization.
2. Kotlin: aggregate per-core counters across automatic reconnections on the lifecycle worker. Bound samples to runtime/account/resource/request and a counter epoch; use elapsedRealtime. Reset on explicit disconnect/resource/account switch, and retain completeness/error state without changing VPN health. Add a separate read-only `traffic` method channel call.
3. Flutter: validate identity/sequence/decimal counters, derive rates using native monotonic time, and poll only while the dashboard is visible/resumed. First sample/gaps/failures have no invented rate; idle has zero. Display two speed tiles and exactly three cumulative metrics with responsive layouts.
4. Verify real byte-transfer paths in Go, native aggregation/state on host JVM, frontend channel/lifecycle/rate tests and light/dark rendered previews. Build the ARM64 AAR and compressed Release APK; record actual dependency/artifact evidence. Keep Studio/emulator closed per existing request and distinguish local tests from device acceptance.
