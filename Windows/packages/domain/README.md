# bluegrass_domain

Pure Dart rules for BlueGrass on Windows. No Flutter, HTTP, filesystem, platform
notification or network-control dependency exists in `lib/`.

Import `package:bluegrass_domain/bluegrass_domain.dart`. Public constructors and
integration semantics are documented in `../../contracts/README.md`.

```dart
final result = prepareProfile(node, effectiveNowUtc);
switch (result) {
  case Ready(:final profile):
    // Supply to the controlled native connection boundary. This is not yet connected.
    useProfile(profile);
  case Rejected(:final error):
    showError(error.code, error.message);
}
```

`NodeRecord.protocolConfig` is an optional immutable `ProtocolConfig`. Create
one with `ProtocolConfig.fromJson(value)` or `ProtocolConfig.parse(rawJson)`;
non-SOCKS profile preparation requires it. `toJson()` returns the closed native
version 1 shape, and `==`/`hashCode` compare canonical configuration content.
See [PROTOCOL.md](PROTOCOL.md) for legacy compatibility, strict validation,
safe native serialization and integration evidence.

`ConnectionProfile.expectedExitIpv4` is nullable source metadata only. Profile
preparation does not require or compare an expected exit; entry, protocol,
credentials, resource identity, forwarding eligibility and expiry checks remain.
A matching native connected confirmation may omit `verifiedExitIpv4` when its
probe has no result. Present observations must be public IPv4; missing observations
clear older IPs instead of substituting metadata. Metadata-only updates do not pause.
See [exit observation contract](../../contracts/EXIT_OBSERVATION.md) for wire and
shared fixture changes that supersede the earlier expected-exit gate.

Do not print profiles, raw nodes, IPC requests or credentials. `ProtocolConfig`
prints only its validated type/version; its `toJson()` output contains secrets.
Other credential-bearing model objects retain opaque string representations.

Run with the project's Flutter SDK Dart:

```powershell
& E:\tools\flutter\bin\dart.bat pub get
& E:\tools\flutter\bin\dart.bat analyze
& E:\tools\flutter\bin\dart.bat test
python tool/ablate.py
```

Tests read shared vectors from `../../contracts/fixtures`. No live endpoints or
customer accounts are needed. Source rules and tests are retained; local TDD
logs and isolated ablation outputs are ignored. `IMPLEMENTATION.md` and
`ABLATION.md` record performed verification and known scope limits.
