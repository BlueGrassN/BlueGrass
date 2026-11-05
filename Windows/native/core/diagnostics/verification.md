# Diagnostics verification — 2026-10-12

Current production API endpoint: `https://client-api.sunip.cc/api/client/v1`.
The historical health request below used the former test endpoint; its result
is preserved and does not verify the current production endpoint. The hostname
migration changes source/test fixtures and documentation only, without a new
live production health or login request.

## Follow-up: eight custom DNS candidates

Aligned diagnostics validation with `entrydns.Policy` and user Preferences:
custom `dns_servers` accepts 1–8 ordered candidates, rejects 0/9, and does not
truncate the list. Exit-probe endpoint limits remain separate (maximum three).
No automatic entrydns reuse, main/controller, transport or service edits.

The new package/stdin boundary test failed for 4 and 8 before the limit change.
The new real-loopback integration test also failed validation before the fix;
afterwards, seven silent UDP candidates and a responding eighth TCP DNS
candidate succeeded in **1.40 seconds** with a single 1600 ms DNS probe budget.
This checks that eight candidates share the budget rather than multiplying it.

Fresh final checks:

- `go test -race -count=1 -v ./diagnostics ./cmd/bluegrass-diagnostics`:
  **29 top-level tests passed**, including the two new tests and boundary
  subtests. [Follow-up test output](verification-dns8-tests.txt).
- `go vet ./diagnostics ./cmd/bluegrass-diagnostics`: exit 0.
- Windows executable rebuilt: exit 0; `{}` stdin smoke returned 17 skipped
  results, exit 0. No live API or production resource probes repeated.
- `gofmt -l` returned no files.

Budgets are unchanged: five seconds for stdin acquisition; per-probe default
three seconds, configurable up to ten; whole run at most thirty seconds.

Production-command side-effect review: only requested network observations and
bounded stdout output; no credential/profile loading, engine/session startup,
process spawning, persistent writes, system-proxy/DNS changes or WFP/TUN
whitelist/protection mutation. No physical path is inferred from ordinary OS
routing. An unwhitelisted TUN helper receiving `{}` does no probes and cannot
report physical bypass as passed. App snapshot-generation discard is owned by
main; this follow-up does not claim a new end-to-end app/protection validation.

## Initial implementation evidence

Scope: new `Windows/native/core/diagnostics/` and
`Windows/native/core/cmd/bluegrass-diagnostics/` only. Existing dirty work and
endpoint edits preserved. No main/controller/engine/service/resolver/domain or
`http_transport.dart` changes. No commits, deployments, credentials, account
operations, database access, WFP/TUN/proxy/DNS configuration changes or agents.

Host: Windows amd64; `D:/Program Files/Go/bin/go.exe` reports
`go1.27.2 windows/amd64`. Race run used the existing
`C:/msys64/ucrt64/bin` GCC in the process PATH only. No dependencies or global
tool settings were changed; `github.com/miekg/dns` was already in go.mod.

## Completed checks

Executed from `E:/workspace/APP/Windows/native/core`:

| Check | Result |
| --- | --- |
| Initial tests before package/CLI implementation | Failed with undefined public API / command runner (RED). |
| `go test -race -count=1 -v ./diagnostics ./cmd/bluegrass-diagnostics` | **27 top-level tests passed**, additional table subtests passed; both packages exit 0, no race report. |
| `go vet ./diagnostics ./cmd/bluegrass-diagnostics` | Exit 0. |
| `go build -o ./cmd/bluegrass-diagnostics/out/bluegrass-diagnostics.exe ./cmd/bluegrass-diagnostics` | Exit 0; native Windows amd64 executable built. |
| Executable stdin `{}` | Exit 0; 17 results, all **skipped**, no network configured. |
| Executable stdin API health request with 5000 ms budget | Exit 0; actual `GET https://test-api.dos.im/api/client/v1/health` passed (`https_health_ok`, 1389 ms at this run); all other results skipped. No authorization header/account/database operation. |

Evidence files contain fixed result codes and local test output, no credentials:

- [Final race-test output](verification-tests.txt)
- [Empty-request report](verification-empty.json)
- [Live unauthenticated health report](verification-health.json)

Tests create real local TLS, TCP, UDP, custom UDP/TCP DNS, HTTP CONNECT and SOCKS5
relay fixtures. Public logical addresses inside fixtures are mapped by the
fixture to loopback listeners; these tests do not contact those public targets.
The sole live service operation above is the explicitly named health GET.

Coverage includes TLS verification and SNI/Host preservation with an IPv4 pin;
health envelope, redirects and response bounds; literal entry DNS skip and TCP
connect; custom DNS no-system-fallback and silent-primary/TCP-secondary fallback;
exit fallback on failed/invalid observations and immediate successful observation
on valid public IPv4 responses (IPv6 continues fallback); actual UDP nonce echo, wrong reply, timeout and no
direct fallback; concurrent generations and cancellation; no-auth SOCKS TCP/UDP,
remote relay/fragment rejection and closing UDP on TCP association loss; strict
bounded input, duplicate keys, URL userinfo/query rejection, fixed safe errors
and explicit unmeasured statuses.

TDD caught and fixed three transport boundaries: Go HTTP transport can detach
the original dial deadline; a nested TCP attempt can expire just before its
parent probe deadline; a SOCKS UDP association must stop when its TCP control
connection closes. The added tests failed before their respective fixes and
passed in the final race run.

## Limits

The full native suite was not run because unrelated service/WFP/TUN/system
proxy behavior is outside this worker's mutation scope. No application or
installed service integration is claimed. Physical routing/bypass, all global
protection states, current production resource auth/exit and UDP source IP
remain unmeasured. The live health pass verifies unauthenticated HTTPS health
with no HTTP proxy configuration, not a physically bound control-plane route.

The earlier completed data-source clock task recorded **95 passing Dart tests**
and clean `dart analyze`, including 14 time/generation tests, in
`Windows/packages/data_source/docs/effective-time-verification.md`. Main is now
editing `http_transport.dart` for a protected control-plane resolver callback;
this diagnostics verification neither edits that file nor claims to validate
main's subsequent transport changes. Main should run the combined data-source
suite after its transport integration.

The isolated MySQL suite/EXPLAIN/concurrency checks remain skipped. Expected
egress is optional ignored metadata under the current product contract; it is
not a connectivity or diagnostic prerequisite. Complete ownership lifecycle
mapping retains the source-audit boundaries. See [README.md](README.md) for the
current integration rules and [../exitprobe/observation-verification.md](../exitprobe/observation-verification.md)
for the subsequent observation-only correction.
