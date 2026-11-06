# Shared BlueGrass client

`bluegrass_client` is the canonical Flutter implementation used by the Windows,
Android and macOS hosts. Add common features here rather than under a platform
host's `lib/` export wrappers. Domain and API contracts come from the sibling
`domain` and `data_source` packages; native networking comes from root `core/`.

Keep OS permissions, secure storage, Android VPN and desktop IPC behind platform
adapters. Share behavior and feature content without removing platform-only
recovery, update, notification or lifecycle requirements.

Use Flutter 3.47.9. Run `flutter pub get`, `flutter analyze` and `flutter test`
here, followed by the affected host's regression suite. Application assets and
platform signing remain part of each host bundle.

See [shared architecture](../../docs/development/shared-client-architecture.md)
and [release policy](../../docs/development/github-releases.md).

Desktop hosts import the public `main.dart` entry; Android imports `android.dart`.
Do not import another package's `lib/src` directly from host bootstrap code.
