# Independent Android network probe

Standalone, standard-library-only Go executable for a parent-controlled live
functional test. It does not link the BlueGrass core or read app/account settings.
The Linux amd64 static executable runs as Android's adb shell UID (normally 2000),
outside the BlueGrass app UID. The parent must verify that the shell UID is included
in the intended VpnService routing and compare baseline/VPN reports.

Every socket uses the OS-selected interface and source address. There is no
VpnService protection, interface binding, explicit local-address binding, proxy,
or redirect following. All traffic is IPv4. Every hostname lookup uses a Go
resolver with an explicit dial hook to the selected DNS server, including DNS TCP
fallback. Queries are absolute names and A-only; there is no external resolver
fallback. A hosts-file shortcut without a DNS query fails as `dns_not_queried`.

Fixed probes, in order:

1. DNS A lookup for `example.com` (reports answer count, accepting VPN FakeIP).
2. HTTPS exit IPv4 from `https://api.ipify.org?format=json`. On failure, try only
   `https://ipv4.icanhazip.com`, unless trust loading failed or the total budget
   expired. Only a valid public IPv4 and HTTP 200 count as success.
3. UDP STUN to `stun.cloudflare.com:3478`.
4. UDP STUN to `stun.l.google.com:19302`, even if the first server succeeded.

HTTPS always verifies certificates and hostnames. The trust store combines
`x509.SystemCertPool()` with PEM files explicitly read from
`/system/etc/security/cacerts`, which a Linux-targeted Go executable does not
automatically treat as its Android trust directory. Missing/partial Android roots
are reported; usable standard roots may still permit verified HTTPS. At most 1024
CA directory entries and 1 MiB per file are read. HTTPS headers are limited to
16 KiB and the response body to 1024 bytes. Neither bodies nor raw errors are
printed.

STUN uses one random 96-bit transaction and one connected UDP socket per server.
The same request is sent at most four times (approximately 0, 0.5, 1.5 and 3.5
seconds after initial send). Invalid replies are ignored within the existing
deadline, with a maximum of 64 received packets. A successful reply must have the
correct magic cookie, transaction, binding-success type and valid, complete
attribute framing, including attributes after XOR-MAPPED-ADDRESS. The IPv4 address
and port are decoded in network byte order. Private, loopback, link-local, shared,
documentation, benchmarking/FakeIP and reserved IPv4 observations are rejected.
These are bounded diagnostic retries, shorter than a general-purpose STUN client's
default transaction lifetime. See [RFC 8489](https://www.rfc-editor.org/rfc/rfc8489.html),
sections 6.2.1 and 14.2.

## Build and local validation

From `E:\workspace\APP`:

```powershell
& .\Android\scripts\build-network-probe.ps1
```

The script uses `D:\Program Files\Go\bin\go.exe`, a fresh per-invocation
`Android/tools/network-probe/.cache/build-<GUID>/go-build`, and temporary build files
inside that same ignored directory. It restores process environment variables on
exit. It runs host unit tests, Linux-targeted `go vet`, and then:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1
go build -mod=readonly -trimpath -buildvcs=false -ldflags "-s -w" -o <output> .
```

Output: `Android/.artifacts/live-functional-20261024/network-probe` (already ignored).
To keep the output inside this tool's owned directory, use:

```powershell
& .\Android\scripts\build-network-probe.ps1 -OutputPath .\Android\tools\network-probe\.artifacts\network-probe
```

The script checks the actual ELF is x86-64 with no PT_INTERP or PT_DYNAMIC,
prints Go build metadata and SHA-256, and never invokes adb. No external modules
or toolchain downloads are allowed. The module requires Go 1.23 or later.

Tests use only local UDP/DNS/TLS servers and temporary certificate fixtures. They
cover exact STUN request bytes, transaction/cookie validation, XOR IPv4/port,
truncated/malformed/padded attributes, same-socket/same-transaction retransmission,
deadlines, explicit DNS/no fallback, verified TLS/Android PEM loading, no proxy,
no redirects, bounded bodies, invalid observations and sanitized CLI errors.
These checks do not demonstrate emulator execution or real VPN routing.

## Parent-controlled invocation

After the parent copies the binary to `/data/local/tmp/network-probe` and makes
it executable, run as the ordinary adb shell user, without `su` or `run-as`:

```text
adb -s <serial> shell /data/local/tmp/network-probe --dns-server 10.0.2.3:53
adb -s <serial> shell /data/local/tmp/network-probe --dns-server 172.19.0.2:53
```

The required `--dns-server` accepts an IPv4 literal with port 53. Optional routing
flags are documented below. Invalid input, unknown flags (including `--help`) and positional arguments
produce one sanitized JSON report with exit code 2, without sending probes.

Each DNS, HTTPS attempt or STUN server has an 8-second budget including resolution,
connection, reads, retries and TLS as applicable. HTTPS may take up to 16 seconds
across its two attempts. Trust loading has a 5-second budget. All stages share a
45-second overall context; report encoding/process scheduling may add a small
amount of overhead. `duration_ms` means total elapsed stage time, not pure RTT.

Exactly one compact JSON line is written to stdout. Exit codes:

- `0`: all four logical probes succeeded (and every routing row if opted in).
- `1`: at least one probe failed, or the overall deadline expired.
- `2`: invalid command line. No argument contents are echoed.

## Exact report fields (schema version 1)

Every listed base field is always present. Arrays can be empty only for HTTPS attempts
before execution. Strings without a value are `""`, counts/ports/durations default
to `0`, and unsuccessful `ok` is `false`.

| Path | Type | Meaning |
| --- | --- | --- |
| `schema_version` | integer | `1` |
| `dns_server` | string | Validated IPv4 literal:53, or empty for invalid input |
| `uid` | integer | Actual process UID; parent expects shell UID 2000 on Android |
| `duration_ms` | integer | Whole run elapsed milliseconds |
| `error` | string | Empty, `invalid_arguments`, `probe_failed`, `deadline_exceeded` |
| `counts.total` | integer | Always 4: DNS, aggregate HTTPS, two STUN servers |
| `counts.ok` | integer | Successful logical probes |
| `counts.failed` | integer | 4 minus successful probes, including not-run probes |
| `ca.system_pool_loaded` | boolean | Standard system pool call succeeded |
| `ca.android_ca_files_loaded` | integer | Android files containing accepted PEM certificates |
| `ca.error` | string | Trust-loading diagnostic; does not itself determine probe success |
| `dns.host` | string | `example.com` |
| `dns.a_count` | integer | Number of A answers |
| `dns.ok`, `dns.duration_ms`, `dns.error` | boolean, integer, string | DNS stage status |
| `https.endpoint` | string | Successful URL, otherwise last attempted URL, or empty |
| `https.public_ipv4` | string | Validated observation on success, otherwise empty |
| `https.ok`, `https.duration_ms`, `https.error` | boolean, integer, string | Aggregate HTTPS status; successful alternate makes this successful |
| `https.attempts` | array | Primary attempt and optional alternate, preserving each result |
| `https.attempts[].url` | string | Fixed URL attempted |
| `https.attempts[].public_ipv4` | string | Validated observation or empty |
| `https.attempts[].ok`, `https.attempts[].duration_ms`, `https.attempts[].error` | boolean, integer, string | Individual request status |
| `stun` | array | Exactly two entries in Cloudflare, Google order |
| `stun[].server` | string | Fixed hostname:port |
| `stun[].public_ipv4` | string | Validated XOR-mapped public IPv4 or empty |
| `stun[].mapped_port` | integer | Validated mapped UDP port or 0 |
| `stun[].attempts` | integer | Successfully sent datagrams (0 to 4) |
| `stun[].ok`, `stun[].duration_ms`, `stun[].error` | boolean, integer, string | Per-server status, including DNS/retry time |

All `error` fields use only fixed codes. An empty code denotes success, except
that CA diagnostics can be nonempty even when HTTPS succeeds with available roots.

| Scope | Codes |
| --- | --- |
| Unexecuted/network deadline | `not_run`, `timeout` |
| DNS (including HTTPS/STUN hostname lookup) | `dns_failed`, `dns_not_queried`, `dns_no_a` |
| HTTPS | `tls_no_roots`, `tls_verify`, `connect_failed`, `https_failed`, `http_status`, `https_body`, `invalid_public_ipv4` |
| STUN | `stun_io`, `random_failed`, `stun_malformed`, `stun_cookie`, `stun_transaction`, `stun_type`, `stun_response_error`, `stun_no_ipv4`, `stun_packet_limit`, `invalid_public_ipv4` |
| CA | `not_run`, `ca_timeout`, `ca_unavailable`, `android_ca_unavailable`, `android_ca_limit`, `android_ca_partial`, `android_ca_empty` |

STUN invalid-packet codes describe the last rejected packet if no valid response
arrives before the transaction ends. A silent server reports `timeout`. Successful
reports expose only public observed IPv4s/ports, fixed target names, selected DNS,
UID, timings and status counts. They do not claim the VPN's expected exit or prove
which route was used; the parent must correlate live baseline/VPN evidence.

## Optional split-routing checks

`--routing-check` adds a `routing` object to the schema-1 JSON report. Without it
(or with `--routing-check=false`), the invocation, probe order, four-probe counts,
report fields and exit-code semantics remain unchanged. The extension does not
link the production matcher, access credentials, read app state, or operate adb.

With the flag, three endpoint workers run alongside the original HTTPS/STUN work:

- `https://myip.ipip.net/`: strict `当前 IP：<IPv4> 来自于：<nonempty text>` format.
- `https://4.ipw.cn/`: a single plain IPv4, with optional surrounding whitespace.
- `stun.miwifi.com:3478`: RFC 8489 binding observation. For each failed path, try
  `stun.qq.com:3478` once as an alternative if the overall budget remains.

Each endpoint gets an A lookup through **the explicitly configured DNS server**,
then a `domain` and a `literal_ip` transaction. DNS answers are cached only for
that endpoint in this invocation; the domain is retained on every result. Each
transaction uses a fresh socket. Domains with no safe A answer fail without a
connect attempt. These endpoints are candidates for the parent's CN tests; their
names, response location text and successful replies do not establish CN status.

The domain path selects the first safe A answer, which may be a VPN FakeIP.
`198.18.0.0/15` addresses are recorded separately in `synthetic_ipv4` and marked
`target_kind: "synthetic"`; they are **never** physical public targets. The literal
path selects the first public A answer, or the parent's optional cached address:

```text
--routing-ip myip.ipip.net=117.149.203.79
--routing-ip stun.miwifi.com=111.206.174.2
```

Repeat `--routing-ip domain=IPv4` at most once per fixed endpoint (including
`stun.qq.com`). It requires `--routing-check`; arbitrary hosts, duplicate hosts,
IPv6, private/reserved and FakeIP addresses are rejected before any network work.
An override applies **only to the literal path**. It cannot replace the domain
lookup. If DNS fails, a valid override can still be checked independently, with
that failed DNS status retained. Without an override or public DNS answer, the
literal row fails as `no_public_target`; it never quietly retries a FakeIP.
Other non-public DNS answers are omitted from the artifact and only counted.

Both HTTPS paths retain the original domain for HTTP Host, TLS SNI and certificate
hostname verification. `literal_ip` uses an IP URL and pins the TCP destination;
it does not bypass TLS. A router that sniffs SNI may still apply domain rules to
that connection: the parent must combine the literal target with matcher results
and routing evidence before claiming IP-only classification. STUN carries no TLS
SNI or HTTP Host. A domain STUN path means the explicit DNS answer was used, not
that a hostname is encoded in the STUN packet. Shared DNS/IP caches can also affect
route selection; the probe alone cannot identify the rule that matched.

All HTTPS requests reuse the existing trust, no-proxy/no-redirect configuration,
16 KiB header and 1024-byte body bounds. No response body, location text, certificate
contents, or raw error is logged. IP extraction rejects HTML, malformed/ambiguous
responses and non-public observations. STUN reuses the existing transaction,
retry, validation and cancellation code. Every resolution and transaction has an
8-second limit; all workers and fallback requests share the original **45-second
whole-run deadline**. There are no unbounded retries. One endpoint's domain then
literal sequence may consume up to 24 seconds, including its shared DNS lookup.

## Routing extension fields

The base `counts` still describes exactly the original four checks.
`routing.counts` counts **individual attempted or unavailable rows**, including a
failed primary even when its fallback succeeds. Thus exit `1` is also expected
when a candidate is unavailable or a physical address is missing; inspect rows
to distinguish endpoint availability from route behavior. A successful observation
is not a pass/fail verdict on routing policy.

`routing.china` contains six rows in fixed order: ipip domain/literal, ipw
domain/literal, miwifi domain/literal. Up to two QQ fallback rows follow, only for
the failed miwifi paths. All fields below are present on every row; empty arrays
are `[]`, unavailable strings `""`, and unavailable numbers `0`.

| Field | Meaning |
| --- | --- |
| `protocol` | `https` or `stun` |
| `endpoint`, `domain` | Fixed original URL or hostname:port, and original hostname |
| `path` | `domain` or `literal_ip` |
| `dns.ok`, `dns.error`, `dns.duration_ms` | Shared explicit DNS lookup status, independent of transaction status |
| `resolved_ipv4` | Deduplicated public A answers, usable by the parent classifier |
| `synthetic_ipv4` | Deduplicated `198.18.0.0/15` answers, never classifier inputs for physical targets |
| `rejected_address_count` | Number of other distinct addresses omitted |
| `target_ipv4` | Selected socket destination, empty when no usable target exists |
| `target_kind` | `public`, `synthetic`, or empty; “public” does not mean “CN” |
| `target_source` | `dns_answer` or `routing_ip_flag` |
| `classification` | Always `unverified`; parent verifies actual bundled rules separately |
| `public_ipv4` | Validated observed exit address on success, otherwise empty |
| `mapped_port`, `attempts` | STUN mapping and transmitted datagrams; both zero for HTTPS |
| `ok`, `error`, `duration_ms` | Transaction outcome and elapsed time, excluding the separately reported DNS lookup |

The extension reuses existing fixed error codes and adds `dns_answer_limit`
(more than 64 A answers), `no_safe_target` (domain had no public/FakeIP answer),
and `no_public_target` (no physical IPv4 for the literal path). It never emits
caller-supplied argument text on invalid input.

## Parent handoff

The implementation task builds and runs **local tests only**. The parent checks
live endpoint availability, the bundled GeoSite/GeoIP matcher, and the shell UID's
actual VPN inclusion, then copies the static binary and runs baseline, `global`,
and `bypass_cn` observations. For example, using the parent-verified addresses from
2026-10-24 (refresh if they no longer serve the endpoint):

```text
/data/local/tmp/network-probe --dns-server 10.0.2.3:53 --routing-check --routing-ip myip.ipip.net=117.149.203.79 --routing-ip stun.miwifi.com=111.206.174.2
/data/local/tmp/network-probe --dns-server 172.19.0.2:53 --routing-check --routing-ip myip.ipip.net=117.149.203.79 --routing-ip stun.miwifi.com=111.206.174.2
```

Use the actual baseline/VPN DNS for the emulator. The parent may add a verified
`stun.qq.com` physical address to cover fallback with FakeIP DNS. Capture each one-line
JSON stdout as a local artifact and retain the process exit code. Compare the
observed exits with baseline and selected-node exits; classify `domain` and each
physical `target_ipv4` independently. The parent's rule evidence lives at
`Android/.artifacts/routing-20261024/targets.jsonl`; it is not loaded or modified by
the probe. DNS failure for `4.ipw.cn` remains visible and does not suppress ipip.

Local tests cover flag gating and sanitized errors, strict domestic parsers,
explicit DNS provenance, physical overrides with FakeIP DNS, private-answer
redaction, QQ fallback, verified TLS on both URL forms, rejection of untrusted
issuers/wrong hostnames, shared STUN exit decoding, and bounded cancellation.
Local tests and an ELF build do not establish live endpoint or emulator success.
