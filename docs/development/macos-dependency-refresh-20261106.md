# macOS dependency refresh — 2026-11-06

The 14 packages advertised as newer in [build 42144670941](https://github.com/BlueGrassN/BlueGrass/actions/runs/42144670941) are updated to the latest stable versions reported by pub.dev on 2026-11-06. Four lockfiles cover the Windows/shared application, Mac launcher, domain and data source packages.

| Package | Previous version in affected lockfiles | Updated version |
| --- | --- | --- |
| _fe_analyzer_shared | 109.0.0 | 111.0.0 |
| analyzer | 14.5.0 | 14.7.0 |
| coverage | 1.15.1 | 1.16.0 |
| crypto | 3.0.7 | 3.0.8 |
| frontend_server_client | 4.1.0 | 4.2.0 |
| http_multi_server | 3.2.2 | 3.2.3 |
| material_color_utilities | 0.13.0 | 0.13.1 |
| meta | 1.18.3 | 1.19.0 |
| shelf | 1.4.2 | 1.4.3 |
| shelf_static | 1.1.3 | 1.2.0 |
| test_api | 0.7.12 | 0.7.15 |
| vector_math | 2.4.0 | 2.4.4 |
| vm_service | 15.3.0 | 15.4.0 |
| web | 1.1.1 | 1.1.2 |

The coverage upgrade also adds its current transitive dependencies `cli_util 0.6.0` and `ffi 2.2.0` to the two pure Dart test resolutions.

CI now pins [Flutter 3.47.9 stable](https://github.com/flutter/flutter/blob/3.47.9/CHANGELOG.md), tag/stable revision `c197ca5199e9ee5661e865596c9c07294dfab51c`, with Dart 3.13.7. Local validation uses a separate SDK checkout at `E:/tools/flutter-3.47.9`; the original installed SDK is preserved. Actions continue to use Node.js 24.

## Flutter's pinned packages

Flutter 3.47.9 still pins `material_color_utilities 0.13.0` and `test_api 0.7.12`. Both application roots explicitly override these to 0.13.1 and 0.7.15. Overrides in a path dependency are not inherited by the consuming root, so Windows and Mac declare identical exact versions. Remove these overrides when the SDK resolves the required versions normally; revalidate them on SDK upgrades.

Upstream [material color changes](https://pub.dev/packages/material_color_utilities/changelog) contain a hue calculation correction and documentation fix. The [test API changes](https://pub.dev/packages/test_api/changelog) add compiler/runtime support and backend exports. Existing complete application tests and real macOS CI are the compatibility gates. The Flutter framework's normal pin is intentionally overridden; passing project tests does not mean upstream Flutter has certified that combination.

## Local validation

- `flutter pub outdated --json --show-all` in all four roots: every reported current version equals latest stable (Windows app 37 packages; Mac 29; domain 49; data source 50). The two overrides are included in this check.
- Both Flutter applications and both Dart packages: analysis clean.
- Shared Flutter application: **437/437**; Mac launcher: **3/3**.
- Domain: **164/164**; data source: **179/179**.
- Python package/sign/hash checks: **11/11**.
- Shared root dependency versions agree; lockfiles retain hosted archive SHA-256 values. UI, theme/layout and administrator TUN regression tests are included in the existing suites.

## GitHub build and replacement artifacts

Independent review found no material issues. CI now checks all four lockfiles after package compilation with `git diff --exit-code`, proving resolution/build did not silently change the committed dependency graph. Dual-architecture execution is pending; previous verified build+9 artifacts remain available until replacement archives are verified.

The [Mac testing instructions](../../Mac/docs/TESTING.md) still apply: interactive administrator authorization, actual resource networking, global route/DNS takeover and recovery require the user's Mac. Hosted isolated kernel/bootstrap checks do not replace full-device acceptance.
