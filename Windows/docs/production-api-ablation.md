# Production API migration: isolated behavioral ablation

Verified 2026-10-12 (Asia/Hong_Kong), using Dart 3.13.5 on Windows. Both requested experiments completed **baseline pass → compiled behavioral failure → byte-for-byte restoration → pass**. No shared production implementation or existing test was edited.

## Reproduction and isolation

Run from the repository root:

```powershell
& ./Windows/scripts/ablate-production-api.ps1
```

The script creates a timestamp/GUID directory under ignored `Windows/.artifacts/ablation/`, copies the app's `lib`, `test`, `assets`, pubspec and lockfile, and both domain/data_source packages' `lib`, `test`, pubspec and lockfile. Dependencies resolve offline. It verifies that the app, domain and data_source package URIs point into this copy, checks unique mutation anchors, and restores original isolated bytes in `finally`.

Final evidence directory:

```text
Windows/.artifacts/ablation/production-api-20261012-185100-b7595467/
```

- `results.json`: completed=true; two cases with command arguments, working directories, exit codes, durations and source hashes.
- `commands.json`: dependency setup and all six test invocations.
- `shared-hashes.json`: **120 shared input files**, before/after SHA-256; **zero changes**. A separate post-run hash comparison also found zero changes.
- `*-mutation.json`: exact removed text; `*-baseline.log`, `*-ablated.log`, `*-restored.log`: complete test output.

The app test calls the cached Dart executable and `flutter_tools.snapshot` directly with `--suppress-analytics test --no-pub`; the data-source test uses Dart directly. This avoids Flutter batch bootstrap work. The Flutter tool still uses its normal cache-preparation lock: the host-pin mutant log records one startup-lock wait, and that invocation completed in 4.790 seconds. The installed tool releases the cache lock after preparation and before executing tests; the script does not manually acquire or retain it. No SDK/tool installation or lock-file manipulation was performed.

## Results

Each phase selects exactly one existing test using an anchored `--name` expression and `--reporter expanded`.

| Isolated removal | Existing exact test | Baseline | Mutant | Restored |
| --- | --- | --- | --- | --- |
| URI host comparison in `ControlHttpTransport.send` | `retired API origin is rejected in both network modes` | 1 passed; exit 0 | 1 runtime assertion failed; exit 1 | 1 passed; exit 0 |
| Both `_restoreSession` explicit environment mismatch and `_stored` environment predicate | `production never sends former environment credentials: valid_access` | 1 passed; exit 0 | 1 runtime assertion failed; exit 1 | 1 passed; exit 0 |

App test arguments after the cached Flutter snapshot:

```text
--suppress-analytics test --no-pub test/control_http_transport_test.dart --name "^retired API origin is rejected in both network modes$" --reporter expanded
```

Data-source arguments after the cached Dart executable:

```text
--suppress-analytics test test/production_origin_login_test.dart --name "^production never sends former environment credentials: valid_access$" --reporter expanded
```

`commands.json` preserves the actual regular-expression-escaped argument arrays without shell rendering ambiguity.

### 1. Retired API host pin

Removed only `uri.host != 'client-api.sunip.cc' ||` from the isolated `app/lib/control_http_transport.dart`. Scheme, port, user-info, fragment, path, build-origin and remaining transport checks stayed intact.

Actual mutant output:

```text
Expected: throws <Instance of 'ApiFailure'> with `code`: 'insecure_endpoint'
  Actual: <Instance of 'Future<ApiResponse>'>
   Which: emitted <Instance of 'ApiResponse'>
test\control_http_transport_test.dart 51:13  main.<fn>
00:00 +0 -1: Some tests failed.
```

This is a compiled, executed assertion failure: the fake unprotected transport accepted the retired-origin request instead of the guard rejecting it. Baseline/restored runs cover both network modes. The mutant fails on the first loop iteration (`protectionActive=false`), so this experiment does **not** separately demonstrate the protected native path leaking a request. **Retain the host pin.**

### 2. Former environment session

Removed both guards from the isolated `packages/data_source/lib/src/session_coordinator.dart`: the `_restoreSession` check that throws `environment_mismatch`, and the `data['environment'] != _environment ||` clause in `_stored`. Version/device/token validation remained intact.

Actual mutant output:

```text
Expected: throws <Instance of 'ApiFailure'> with `code`: 'environment_mismatch'
  Actual: <Instance of 'Future<Customer?>'>
   Which: emitted <Instance of 'Customer'>
test\production_origin_login_test.dart 163:9  main.<fn>
00:00 +0 -1: Some tests failed.
```

The production-configured coordinator accepted the former environment's valid-access session. The test fails at restoration, before its subsequent protected-request assertions, so the observation is cross-environment session acceptance, not an actual production credential transmission. Baseline/restored runs verify rejection, unauthenticated follow-up, no fake production requests before explicit login, and a fresh production session after explicit login. **Retain environment isolation.** This joint removal does not establish that each guard is individually necessary in every call path.

Both mutants changed the isolated source hash. Restored hashes exactly equal the original shared snapshot:

| Target | Original = restored SHA-256 |
| --- | --- |
| `app/lib/control_http_transport.dart` | `F7C75CD0F61264180E2273F60F82906087901C214E48B065E4DB7949F0ECC38D` |
| `packages/data_source/lib/src/session_coordinator.dart` | `3A812B0B90E68B507B4733273C064B2AA4C593BCE38C748C846771949A4F8AEE` |

The script rejects compilation/loading/setup failures, missing selected tests, and unrelated nonzero exits. A mutant must report the selected name, expected error code, `Expected`/`Actual`/`Which: emitted`, a runtime stack position in that test, and exactly `+0 -1` with `[E]`. Two earlier harness runs rejected valid failures because Flutter and Dart abbreviate matcher stack frames differently. Those incomplete runs remain in separate ignored directories and are not counted as completed experiments; their isolated mutations were restored and shared hashes were unchanged. The final run used the target-test stack location and completed all six phases.

## Abstraction assessment and scope

No new runtime abstraction is needed for server-side login IP collection. Current `SessionCoordinator.login` (`lib/src/session_coordinator.dart:209–214`) builds only the existing username/password/device DTO. `IoHttpTransport.send` (`lib/src/http_transport.dart:85–95`) serializes the original `request.body` and forwards its existing headers; `ControlHttpTransport` serializes that same DTO at `app/lib/control_http_transport.dart:83`. No client IP discovery provider, claimed-IP DTO field, forwarding-header decorator or additional login endpoint is warranted by this migration. This assignment introduced only experiment/report files, so there is no new runtime abstraction to remove.

The existing `real login wire sends only login DTO, no claimed IP or forwarding headers` regression was inspected as supporting source evidence; it was not rerun as part of these two selected cases. Server recording of login IP is outside these experiments.

All selected tests use fake transports/mocked channels and synthetic sessions. There were no production/auth requests, system-proxy changes, service/WFP/TUN operations, or port 10808 use. These results establish the two local behavioral protections, not live production migration acceptance or desktop build/signing acceptance.
