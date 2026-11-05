# Frozen native protocol contract v1

Accepted for the Windows native implementation on 2026-10-12. This implements
ADR 0003's protocol list with constrained parameter combinations; actual resource
availability and per-protocol network acceptance are separate requirements.

## Profile and controller integration

`engine.Profile` contains the optional field:

```go
ProtocolConfig *protocolconfig.Config `json:"protocolConfig,omitempty"`
```

`Profile.EffectiveProtocol() (protocolconfig.Config, error)` validates explicit
configuration and requires `Config.Type == Profile.Protocol`. Explicit parameters
are the only credential source when present. Legacy `Auth` must then be empty,
except for an exact username/password copy of explicit SOCKS5 authentication.
This rejection is centralized in `EffectiveProtocol`, covering nativehost,
direct controller callers and both engines' Prepare/PrepareProtocol methods.
With no config, only `socks5` is
accepted and legacy `Auth` is converted to the typed SOCKS5 configuration. Missing
non-SOCKS parameters are rejected, never inferred from username/password.

The normal `Engine.Prepare(ctx, profile)` path consumes this field in both Local
and Tun. `PrepareProtocol(ctx, profile, config)` remains available for typed native
callers. Every Prepare retires its predecessor before returning a validation
failure. Controller `DecodeRequest` accepts the optional exact field, retains the
required `auth` object, rejects explicit null, validates protocol eligibility,
detaches nested pointers and includes the entire configuration in request replay
digests. The same exact exit/expiry gate applies to all protocols.

For wire integration, send `auth: {username:"", password:""}` for typed resources.
Conflicting duplicate legacy credentials are rejected at the shared Profile
boundary. `protocolConfig` is camelCase on the native Profile. Mapping API
`protocol_config` and Dart `NodeRecord.protocolConfig` is owned by their adapters.

The config does not contain the entry, resource key, expected exit, mode, expiry,
identity or entitlement. Those remain in Profile and must be associated by the
authenticated host/controller. It must not be logged or placed in diagnostics,
FakeIP persistence or protection journals.

## Exact schema

Top-level keys: required `version:1`, required `type`, optional `tls`, and exactly
one matching protocol object. The canonical types are `socks5`, `shadowsocks`,
`vmess`, `vless`, `trojan`, `http`, `https`, `hysteria2`, `tuic`, `anytls`.
`https` uses the `http` object. Aliases are not accepted.

| Object | Fields and constraints |
| --- | --- |
| `tls` | Required `serverName`, optional `alpn` string array; original certificate identity is explicit even after numeric entry resolution |
| `socks5` | Optional paired `username`/`password`; both absent/empty for no auth; each at most 255 bytes |
| `shadowsocks` | Required `method`, `password`; methods below |
| `vmess` | Required `uuid`, `security` (`aes-128-gcm` or `chacha20-poly1305`), `transport:"tcp"`; alterId fixed zero |
| `vless` | Required `uuid`, `transport:"tcp"`; optional `flow` empty or `xtls-rprx-vision`; packet encoding fixed XUDP |
| `trojan` | Required `password`, `transport:"tcp"` |
| `http` | Optional paired `username`/`password`; usernames cannot contain `:`; CONNECT only |
| `hysteria2` | Required `password`; optional `obfs:{type:"salamander",password}`; one fixed entry, no hopping/realm/STUN |
| `tuic` | Required `uuid`, `password`, `congestionControl` (`cubic`, `new_reno`, `bbr`), `udpRelayMode` (`native`, `quic`); 0-RTT and UDP-over-stream option disabled |
| `anytls` | Required `password`; idle check 15 s, idle timeout 30 s, no minimum idle pool |

SS methods: `aes-128-gcm`, `aes-256-gcm`, `chacha20-ietf-poly1305`,
`2022-blake3-aes-128-gcm`, `2022-blake3-aes-256-gcm`,
`2022-blake3-chacha20-poly1305`. SS2022 accepts one strict standard-Base64 key
of 16 bytes for AES128 or 32 bytes for AES256/ChaCha; no chained keys or plugins.

TLS is mandatory for VLESS, Trojan, HTTPS, Hysteria2, TUIC and AnyTLS; optional for
VMess and forbidden for other types. System trust and certificate/hostname
verification stay enabled. Minimum TLS 1.2; QUIC and Vision require 1.3. HTTPS ALPN
is only `http/1.1` (defaulted if omitted); Hysteria2 explicit ALPN is only `h3`.
TUIC's omitted ALPN defaults to `h3`, while explicit peer ALPN is preserved:
the pinned TUIC client does not supply the ALPN required by quic-go itself.
ALPN arrays have at most 8 unique printable-ASCII entries, each 1–255 bytes.
Server names must be a DNS name or IPv4 certificate identity, without wildcard,
URL, zone, control characters or path. No custom CA paths, insecure, SNI-disable,
uTLS, Reality, ECH, arbitrary transport, dialer, routing or native JSON is exposed.

UUIDs use the standard 36-character hyphenated form and cannot be all zero.
Credentials have at most 1024 bytes except SOCKS5's stricter bound, and reject
NUL/CR/LF. Decode is bounded to 16 KiB and rejects unknown/case-variant/duplicate
keys, explicit null, extra protocol objects and trailing content. Errors are
fixed codes and never include submitted values.

## Physical transport and UDP matrix

`Requirements(config)` returns `TransportRequirements{EntryTCP, EntryUDP,
UDPTransport}` only after validation. These are source-level transport needs,
not runtime claims. Permission is always for the exact resolved entry IP:port
and trusted service identity; no unrestricted service TCP or UDP bypass.

| Type | Entry TCP | Entry UDP | `UDPTransport` / business capability |
| --- | --- | --- | --- |
| SOCKS5 | yes | no | `authenticated-socks-relay`; separate callback before exact BND endpoint dial |
| SS | yes | yes | `entry-datagram` |
| VMess / VLESS / Trojan | yes | no | `over-tcp` |
| HTTP / HTTPS | yes | no | `none`; HTTP CONNECT cannot provide UDP |
| Hysteria2 / TUIC | no | yes | `quic`; protocol-required UDP is allowed before extra diagnostic UDP tests |
| AnyTLS | yes | no | `over-tcp` (UoT) |

SOCKS5 TUN registration is overridden by the existing guarded implementation,
which only permits an authenticated literal IPv4 relay on the entry's same IP,
after the current gate is verified and the host authorizes that endpoint. Other
protocols use the pinned constructors and fixed peer; they expose no arbitrary
relay endpoint. The system proxy remains an HTTP TCP gateway and does not capture
whole-host UDP, irrespective of the selected outbound's capabilities.

All native options bind the resolved public IPv4 entry, preserve explicit SNI,
disable arbitrary detours and use a 12-second connect timeout. TUN's router,
DNS interception before LAN, proxied public DNS and IPv6 rejection are unchanged.
Local closes native AnyTLS/QUIC session pools as well as application connections
on Block/Stop/replacement/mismatch/Close; Close is terminal and cleanup errors
remain fail-closed. AnyTLS's upstream idle-worker shutdown can lag Close by one
15-second sleep, but Close immediately cancels and closes its tracked sessions.

## Evidence and limits

Tests cover strict schema rejection, TLS identity, all supported cipher variants,
real pinned constructors and complete Box construction without native Start,
controller-to-Local and controller-to-TUN matrices, closed-before-proof behavior,
stale proof/session isolation, teardown and HTTP UDP rejection. Local controller
tests initialize actual outbounds and replace only external exit observations;
TUN controller tests replace the OS runtime lifecycle. Neither is a remote
handshake, UDP exit or elevated TUN acceptance test.

See `Windows/docs/tun-engine-verification.md` for command results, updater and
diagnostic evidence, dependency versions and the remaining release blockers.
