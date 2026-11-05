# Native diagnostic integration contract (v1)

`bluegrass/native/core/diagnostics` performs bounded, opt-in observations. The
independent `cmd/bluegrass-diagnostics` accepts stdin JSON. Neither component
loads profiles/credentials, starts a proxy engine/service, grants eligibility,
changes WFP/TUN/system proxy/DNS, or uploads a report. Only explicitly configured
network probes generate traffic. There is no automatic probe endpoint.

## Package API

```go
runner := diagnostics.Runner{
    DirectDial:   controlledDirectDial, // optional; nil uses ordinary OS routing
    SelectedDial: currentSelectedDial,  // current authorized/gated resource
}
report := runner.Run(sessionContext, diagnostics.Request{
    APIBaseURL: "https://client-api.sunip.cc/api/client/v1",
    Binding: &diagnostics.Binding{
        Connected: true, Generation: connectionGeneration, ResourceID: resourceID,
    },
    ExitEndpoints:  trustedEndpoints,
    UDP:            trustedUDPEchoEndpoint, // nil until a real service is configured
})
```

- `DialFunc(context.Context, network, address) (net.Conn, error)` must honor
  cancellation/deadlines and provide `Close`/`SetDeadline`. For `udp`, one
  Write/Read must represent one datagram, not a byte stream. It must not retry
  through a direct transport. The host supplies the current authorized engine
  transport and any required relay permission; this package cannot grant it.
- An injected `SelectedDial` takes precedence over `Request.Proxy`. Otherwise
  the request can name an **already existing**, literal loopback `http_connect`
  or no-auth `socks5` gateway. No username/password fields exist. HTTP CONNECT
  provides TCP only; UDP is skipped. SOCKS5 must accept the no-auth method.
- `connected:true`, a nonzero generation and a nonempty opaque resource ID are
  required before any selected-path operation. They are caller assertions, not
  independent proof of session/ownership/expiry. Cancel the context on switch,
  pause, logout, expiry or reconnect. Compare generation AND resource identity
  before consuming a returned report; the runner does not own global state.
- A report carries the supplied generation and the lower-case hex of the first
  16 bytes of `SHA256(UTF8(resource_id))`. This is a correlation fingerprint, not
  an authentication token or anonymization guarantee. Pass only an opaque asset
  ID, never a subscription URL/account identifier/credential. Store report
  history under the app's separate account/source/environment namespace.
- Explicit cancellation sets `cancelled:true`, changes all results to
  `skipped/cancelled`, and removes observed IPs. Results arriving after their
  probe deadline cannot pass. A report is still stale if the host's generation
  changed just after completion: the host must perform its final binding check.
- All callbacks must obey the contract; Go cannot forcibly interrupt arbitrary
  caller code that ignores cancellation. Do not mutate request slices/pointers
  while `Run` is using them.

## Request shape

Field names are the same for the Go JSON tags and stdin. All sections are
optional; `{}` performs no network I/O and returns 17 skipped results.

| Field | Contract |
| --- | --- |
| `api_base_url` | HTTPS base such as the current production endpoint `https://client-api.sunip.cc/api/client/v1`; no userinfo, query or fragment. Appends `/health`. |
| `entry.host`, `entry.port` | Entry DNS name or literal IP, and decimal port **string**, e.g. `"443"`. TCP opens and closes without sending application data. |
| `entry.dns_mode` | `system` (default) or `custom`. `system` means the Go/OS resolver, not sing-box's automatic policy. |
| `entry.dns_servers` | Custom-only, 1–8 ordered `{ "address": "IP:port", "network": "udp" or "tcp" }` entries; bracket IPv6. Matches entrydns and user Preferences; no truncation or custom-to-system fallback. |
| `binding` | `{ "connected": true, "generation": nonzero_uint64, "resource_id": "opaque-id" }`. No implicit login or connection. |
| `expected_exit_ip` | Optional legacy string metadata, ignored. It neither enables nor prevents probing and is never compared with the observed address. |
| `exit_endpoints` | Up to 3 trusted `{ "url": "https://...", "ipv4": "public IPv4" }` entries. IPv4 is the **probe service's address**, not expected egress. Host/SNI and TLS verification use the URL hostname. Only optional `?format=json` query is allowed. |
| `udp` | `{ "address": "public-IPv4:port", "protocol": "echo_nonce_v1" }`; opt-in service that echoes the entire payload exactly. |
| `proxy` | `{ "kind": "http_connect" or "socks5", "address": "loopback-IP:port" }`. No credentials, remote gateway or hostname allowed. |
| `timeout_ms` | Per-probe budget, default 3000; allowed 100–10000. Run budget is at most 30 seconds (or caller deadline). |

The host maps D-04 product DNS candidates into the explicit custom list when it
wants to diagnose that list (including candidates selected by product `auto`).
This code does not read persisted settings or emulate the sing-box resolver.
Custom DNS tries A and AAAA, observes the queried/CNAME owners only, limits
results to four addresses and reserves time for later candidates. TCP tries
those addresses with a shared bounded budget. A literal entry skips DNS.
All eight DNS candidates share the same per-probe budget; adding candidates
does not multiply it. The CLI's five-second limit applies only to reading
stdin, separately from the unchanged per-probe and 30-second run limits.

Trusted endpoints must come from app-controlled configuration. This command is
not a service that should accept untrusted remote requests. It intentionally
allows private/local API and entry endpoints for legitimate internal resources;
it does not create firewall exceptions for them. HTTPS probe pins must be
maintained by the trusted configuration owner as service addresses change.

## What each result establishes

| ID | `passed` establishes | Limits / skipped cases |
| --- | --- | --- |
| `api_health` | Actual TLS-verified HTTP 200 with the existing client API's `success:true`, `data.status:"ok"` and valid `server_time`. | Unauthenticated health only; no DB, login or authenticated clock proof. Uses `Proxy:nil`, never environment/Windows HTTP proxy settings. Ordinary routing may still traverse TUN. |
| `entry_dns` | Actual system or specified DNS answer. Custom result includes one-based `candidate`. | Literal address = `skipped/literal_address`. No engine policy or physical-network bypass claim. |
| `entry_tcp` | Actual TCP connection to one resolved/literal entry address. | No credentials, protocol handshake, TLS or proxy authentication. DNS failure skips this dependent probe. |
| `proxy_exit` | Actual TLS-verified HTTPS through the selected channel and a valid current public IPv4, reported as `passed/observed_exit`. | Missing binding/service/channel skips. Invalid HTTPS/JSON and IPv6 responses can try a fallback. The first valid public IPv4 ends fallback; a changed IPv4 is valid. Neither a changed address nor detector failure disconnects a session. |
| `proxy_udp` | Actual selected-channel datagram exchange with an exact unpredictable nonce echo. | No endpoint/channel/active binding skips. Timeout, wrong echo, transport failure or fragmented SOCKS reply fails; no direct fallback. This does not prove UDP source IP or all UDP applications. |

Exit JSON is exactly `{ "ip": "public IPv4", "country": "optional string" }`.
Duplicate/unknown fields, non-public addresses, redirects, oversized responses
and untrusted certificates cannot pass. No business token, cookie, node auth or
subscription data is sent to the API health or exit/UDP probe services.

UDP `echo_nonce_v1` payload is 40 bytes: ASCII `BGDIAG1` then NUL (8 bytes total),
followed by 32 cryptographically random bytes. The service must return the same
40 bytes. This is a local client protocol contract, **not a claim that a matching
production service is deployed**. SOCKS UDP uses UDP ASSOCIATE, holds its TCP
association, accepts only the same loopback gateway's relay, and validates
encapsulation, source target and fragment byte. It never opens a socket to the
public logical UDP target itself.

The following always return `skipped/not_measured`: `physical_bypass`,
`entry_engine_policy`, `account_auth`, `resource_sync`, `resource_eligibility`,
`proxy_authentication`, `udp_exit_identity`, `ipv6_routing`, `system_proxy`,
`tun`, `network_switch`, `wfp_protection`. A successful network observation must
not be used to label any of these as passed.

When the standalone helper has no authorized physical control-plane path (for
example, it is not whitelisted under active TUN protection), the host can send
`{}` to perform no probes and return all skipped. This package does not reuse
entrydns automatically or create a bypass/whitelist entry on the host's behalf.

All results have a fixed `id`, `status` (`passed`, `failed`, `skipped`), fixed
safe `code`, and monotonic `duration_ms`. Actual public exit observations may
include `observed_ip`; request URLs/hosts/DNS addresses/raw resource IDs, raw
network errors, headers and response bodies never appear in reports. There is
no aggregate success field. A failed UDP diagnostic alone must not disconnect
working TCP. Exit discovery is best-effort after connection, not a connection
eligibility check. Hosts must discard stale results while retaining forwarding
on detector failure or a changed valid address.

`started_at_utc` is the local wall-clock observation timestamp, **not trusted
server time**. Do not calibrate native eligibility/expiry from this report or
the unauthenticated health response. The separate data-source integration is
`ApiV2DataSource.effectiveNowUtc` (fresh authenticated HTTPS + Stopwatch), nullable
per login generation; `environment` returns the normalized base URI before the
first resource page, with source identity `api_v2`. The main/native clock bridge
remains outside this package.

## Accepted scope and remaining evidence

Audited against D-04, D-28, D-35 and the design/addendum diagnostics sections.
These new probes cover the concrete network subset; the skipped IDs above
preserve the broader accepted scope without claiming it is implemented.

- D-28 first-party HTTPS service, trusted endpoint/pin ownership and its
  production source-IP contract still need deployment/operational validation.
  There are no default third-party requests hidden in this package.
- D-35 production UDP service/contract and real current-resource UDP/exit
  acceptance remain unverified. A nonce echo has no source-IP attestation.
- Exit probing needs no authoritative expected address. Missing optional legacy
  metadata does not block connection or observation.
- Full owned-resource coverage remains limited by current query predicates and
  unmapped lifecycle/shared/delivery cases, not blanket absence of Laravel
  ownership fields. See `Windows/packages/data_source/docs/owned-resource-source-audit.md`.
- Isolated MySQL/EXPLAIN/concurrency verification remains **skipped** from the
  source-contract task; diagnostics performs no DB reads or writes.
- The package's local fixtures prove real loopback protocol operations only.
  They do not prove main/controller integration, deployed proxy protocols,
  physical binding, protection, installed service behavior or production egress.

See [verification.md](verification.md) for commands and observed results.
