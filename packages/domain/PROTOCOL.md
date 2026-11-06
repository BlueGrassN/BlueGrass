# Version 1 protocol integration

The authority is `core/protocolconfig/config.go`. This document
describes the additive Dart boundary and shared schemas implemented on
2026-10-12. It does not establish deployed API data or live protocol acceptance.

## Public API and native payload

```dart
final config = ProtocolConfig.fromJson(decodedObject);
final configFromWire = ProtocolConfig.parse(rawJson);
final legacy = ProtocolConfig.legacySocks(username: username, password: password);

config.version; // 1
config.type; // Validated protocol name.
config.toJson(); // Detached Map<String, dynamic>; contains credentials.
config == configFromWire; // Canonical content equality.
```

`NodeRecord` adds nullable `ProtocolConfig? protocolConfig`. Its existing const
constructor and legacy fields remain usable. `ConnectionProfile.protocolConfig`
exposes the same nullable value after `prepareProfile`; it never synthesizes
options from a non-SOCKS name or converts absent legacy config into a typed field.

Main's existing native profile serializer can add:

```dart
if (profile.protocolConfig != null)
  'protocolConfig': profile.protocolConfig!.toJson(),
```

This is a map entry inside the existing profile object. Omit the field when null.
The API's optional node field is `protocol_config`, with the same inner schema.
The current SOCKS server may omit it. No populated protocol options, TLS identity,
UUID, transport, fixed exit or source mapping have been fabricated server-side.

`toJson()` creates a fresh nested object; neither input/output mutation can change
the validated config. Field order and omitted versus empty optional auth/ALPN/flow
do not affect equality. ALPN order and all actual credential/TLS/option changes
do affect equality. Main can compare `old.protocolConfig != fresh.protocolConfig`
alongside entry and legacy auth comparisons. Expected-exit metadata is excluded
from active configuration comparisons and never requires pausing. `hashCode`
is for in-process collections, not a durable fingerprint or server revision.
The domain reducer performs this comparison too: a config change clears verified
exit and enters pausing while emitting the existing applyResourceUpdate effect;
same-config expiry updates retain their previous behavior pending native ack.

## Validation and legacy binding

Exactly one named options section is permitted with `version: 1` and `type`.
Unknown fields at any level, extra sections, nulls, wrong types, duplicate raw
keys (including escaped-equivalent keys), unsupported values and configs over
16 KiB are rejected. Format exceptions contain fixed codes without source text.
`ProtocolConfig.toString()` contains only type/version. Do not log `toJson()`.

| Type | Section and permitted options | TLS |
| --- | --- | --- |
| socks5 | socks5: optional paired username/password, max 255 UTF-8 bytes each | Forbidden |
| shadowsocks | shadowsocks: explicit method/password; three AEAD and three SS2022 methods | Forbidden |
| vmess | vmess: uuid, aes-128-gcm or chacha20-poly1305 security, tcp transport | Optional |
| vless | vless: uuid, tcp transport, optional empty or xtls-rprx-vision flow | Required |
| trojan | trojan: password, tcp transport | Required |
| http | http: optional paired username/password | Forbidden |
| https | http: optional paired username/password | Required; ALPN only http/1.1 |
| hysteria2 | hysteria2: password, optional salamander obfs with password | Required; ALPN only h3 |
| tuic | tuic: uuid/password, cubic/new_reno/bbr, native/quic UDP relay | Required |
| anytls | anytls: password | Required |

Exact enums and structural requirements are in
`../../contracts/domain/protocol.schema.json`. Secrets otherwise permit at most
1024 UTF-8 bytes and exclude NUL/CR/LF. SS2022 requires canonical padded base64
of exactly 16 or 32 decoded bytes, according to method. HTTP usernames exclude
colon. TLS takes serverName plus optional unique printable-ASCII ALPN entries
(at most 8). Its certificate-identity policy matches native and is separate from
observed public-egress validation; private IPv4 TLS identities are permitted.

`prepareProfile` returns ELIG-007 when the protocol config is absent for a
non-SOCKS node, mismatched or conflicts with legacy authentication. Explicit
configs permit empty legacy auth. Nonempty legacy auth is accepted only for
SOCKS when both fields equal its typed credentials. Legacy SOCKS with no explicit
config is validated through `legacySocks`; one-sided or malformed auth fails.
Forwarding, expiry and endpoint checks remain separate gates. Expected exit is
optional metadata and does not affect profile eligibility or compare to actual IP.
The data source rejects invalid protocol DTOs as `ApiFailure('invalid_response')`
without invalidating an otherwise live account or disclosing the payload.

## Shared checks and integration boundary

`../../contracts/fixtures/protocol/config-v1.json` is reused by Dart domain,
data-source list/detail tests and Python schema validation. It contains 45 raw
cases: 15 accepted, 30 rejected. The schemas require explicit matching config
for non-SOCKS nodes/profiles; existing legacy fixtures still pass. Run Python via
`py -3.14 Windows/contracts/test_contracts.py` from repository root. Custom
formats/raw checks are required as described in `../../contracts/README.md`.

The native Config source was checked against the same corpus by copying it to
an isolated temporary directory, changing only its package declaration to main,
then running a standard-library Go harness through Config.UnmarshalJSON. This
does not modify native sources or require engine module dependencies. The checked
source SHA-256 was
`0F07BB9159F86DD1DB740FD9820A40A38104B018FADCE548EED0F336A3076EF6`.

Final local verification on 2026-10-12:

| Check | Result |
| --- | --- |
| Domain `dart test --reporter expanded` | 129 passed |
| Domain `dart analyze` | No issues |
| Data source `dart test --reporter expanded` | 165 passed, including existing time/session/transport tests |
| Data source `dart analyze` | No issues |
| `py -3.14 Windows/contracts/test_contracts.py` | 12 test methods passed, including all 45 shared protocol cases and legacy fixtures |
| Isolated native Config corpus | 45 cases, 0 mismatches |

The reducer refresh regression first failed by remaining connected after a
typed credential change, then passed after adding the config comparison. Three
additional shared cases first failed Python validation: UUID/ALPN trailing
newlines were accepted, and a JSON surrogate escape was incorrectly rejected.
Schema end matching and UTF-8 byte counting were corrected; Dart and native
already handled those cases as specified by the shared corpus.

One wrapper difference remains for main/Euclid coordination: the current native
`engine.Profile.EffectiveProtocol()` prioritizes explicit options even when its
legacy Auth conflicts. `engine/protocol_test.go` explicitly asserts this
precedence. Dart and the shared schemas reject such conflicts under the requested
contract. Accepted Dart payloads use unambiguous credentials, but full Profile
rejection parity has not been established. Native files are outside this task's
write scope and were not changed.

Parsing/schema equality is not engine connectivity, server credential acceptance,
UDP support or TUN/protection evidence. No live protocol probes, account operations,
isolated database tests, deployment or commits are part of this implementation.
