// Package diagnostics performs bounded, opt-in network observations. It does
// not grant eligibility, open an engine session, or change OS networking.
package diagnostics

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"net"
	"time"
)

type Status string

const (
	Passed  Status = "passed"
	Failed  Status = "failed"
	Skipped Status = "skipped"
)

type Binding struct {
	Connected  bool   `json:"connected"`
	Generation uint64 `json:"generation"`
	ResourceID string `json:"resource_id"`
}

type DNSServer struct {
	Address string `json:"address"` // Literal IP:port; bracket IPv6.
	Network string `json:"network"` // udp or tcp
}

type Entry struct {
	Host       string      `json:"host"`
	Port       string      `json:"port"`
	DNSMode    string      `json:"dns_mode,omitempty"`    // system (default) or custom
	DNSServers []DNSServer `json:"dns_servers,omitempty"` // Custom-only, 1..8 ordered candidates.
}

type ExitEndpoint struct {
	URL  string `json:"url"`  // Trusted HTTPS URL; never a subscription URL.
	IPv4 string `json:"ipv4"` // Trusted public IPv4 of this HTTPS service.
}

type UDPEndpoint struct {
	Address  string `json:"address"`  // Public IPv4:port, no DNS or direct fallback.
	Protocol string `json:"protocol"` // echo_nonce_v1; requires a compatible service.
}

type LoopbackProxy struct {
	Kind    string `json:"kind"`    // http_connect or socks5 (no authentication)
	Address string `json:"address"` // Existing loopback listener, literal IP:port.
}

type Request struct {
	APIBaseURL     string         `json:"api_base_url,omitempty"`
	Entry          *Entry         `json:"entry,omitempty"`
	Binding        *Binding       `json:"binding,omitempty"`
	ExpectedExitIP string         `json:"expected_exit_ip,omitempty"` // Ignored legacy metadata; never a probe prerequisite.
	ExitEndpoints  []ExitEndpoint `json:"exit_endpoints,omitempty"`
	UDP            *UDPEndpoint   `json:"udp,omitempty"`
	Proxy          *LoopbackProxy `json:"proxy,omitempty"`
	TimeoutMS      int            `json:"timeout_ms,omitempty"` // Per probe, default 3000, range 100..10000.
}

type Result struct {
	ID         string `json:"id"`
	Status     Status `json:"status"`
	Code       string `json:"code"` // Fixed code; never a raw network error.
	DurationMS int64  `json:"duration_ms"`
	Attempts   int    `json:"attempts,omitempty"`
	Candidate  int    `json:"candidate,omitempty"`   // One-based successful custom DNS candidate.
	ObservedIP string `json:"observed_ip,omitempty"` // Validated public address only.
}

type Report struct {
	Version             int       `json:"version"`
	StartedAtUTC        time.Time `json:"started_at_utc"` // Local observation timestamp, not a trusted clock.
	DurationMS          int64     `json:"duration_ms"`
	Generation          uint64    `json:"generation"`
	ResourceFingerprint string    `json:"resource_fingerprint,omitempty"`
	Cancelled           bool      `json:"cancelled"`
	Results             []Result  `json:"results"`
}

// DialFunc must honor ctx, return a connected stream/datagram net.Conn, and
// implement Close/SetDeadline. For "udp", each Write/Read is ONE datagram.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Runner struct {
	// DirectDial bypasses HTTP proxy configuration, but physical-network binding
	// is the host's responsibility. Nil uses net.Dialer (ordinary OS routing).
	DirectDial DialFunc
	// SelectedDial MUST use the already-authorized, current resource transport,
	// including gate and generation checks. It must never fall back to direct.
	// Nil uses Request.Proxy; nil with no proxy skips selected-path probes.
	SelectedDial DialFunc
	// Optional trust roots for an explicitly managed HTTPS service. Nil uses OS
	// trust. The CLI cannot disable TLS verification or supply custom roots.
	RootCAs *x509.CertPool
}

func skip(code string) Result { return Result{Status: Skipped, Code: code} }
func fail(code string) Result { return Result{Status: Failed, Code: code} }
func pass(code string) Result { return Result{Status: Passed, Code: code} }

var unsupported = []string{
	"physical_bypass", "entry_engine_policy", "account_auth", "resource_sync",
	"resource_eligibility", "proxy_authentication", "udp_exit_identity",
	"ipv6_routing", "system_proxy", "tun", "network_switch", "wfp_protection",
}

// Run is stateless and safe for concurrent independent calls. Cancel ctx when
// the session changes; the host must ALSO discard reports with stale bindings.
// No result here authorizes traffic or commands a disconnect.
func (r Runner) Run(ctx context.Context, req Request) Report {
	start := time.Now()
	report := Report{Version: 1, StartedAtUTC: start.UTC(), Results: make([]Result, 0, 17)}
	if req.Binding != nil && len(req.Binding.ResourceID) <= 256 {
		report.Generation = req.Binding.Generation
		if req.Binding.ResourceID != "" {
			hash := sha256.Sum256([]byte(req.Binding.ResourceID))
			report.ResourceFingerprint = hex.EncodeToString(hash[:16])
		}
	}
	budget, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	valid := ValidateRequest(req) == nil
	direct := r.DirectDial
	if direct == nil {
		direct = (&net.Dialer{}).DialContext
	}
	selected := r.SelectedDial
	if selected == nil && req.Proxy != nil && valid {
		selected = loopbackDial(*req.Proxy)
	}
	selectedUDP := selected
	if r.SelectedDial == nil && req.Proxy != nil && req.Proxy.Kind == "http_connect" {
		selectedUDP = nil
	}
	run := func(id string, probe func(context.Context) Result) {
		ts := time.Now()
		var res Result
		switch {
		case !valid:
			res = skip("invalid_request")
		case budget.Err() != nil:
			res = skip("cancelled_or_budget_exhausted")
		default:
			probeCtx, done := context.WithTimeout(budget, timeout)
			res = probe(probeCtx)
			if contextExpired(probeCtx) && res.Status == Passed {
				res.Status = Failed
				res.Code = "probe_timeout"
				res.ObservedIP = ""
			}
			done()
		}
		res.ID = id
		res.DurationMS = time.Since(ts).Milliseconds()
		report.Results = append(report.Results, res)
	}
	run("api_health", func(ctx context.Context) Result { return r.apiHealth(ctx, req.APIBaseURL, direct) })
	var addresses []string
	run("entry_dns", func(ctx context.Context) Result {
		var res Result
		addresses, res = resolveEntry(ctx, req.Entry)
		return res
	})
	run("entry_tcp", func(ctx context.Context) Result { return entryTCP(ctx, req.Entry, addresses, direct) })
	run("proxy_exit", func(ctx context.Context) Result { return r.proxyExit(ctx, req, selected) })
	run("proxy_udp", func(ctx context.Context) Result { return proxyUDP(ctx, req, selectedUDP) })
	for _, id := range unsupported {
		res := skip("not_measured")
		res.ID = id
		report.Results = append(report.Results, res)
	}
	report.Cancelled = ctx.Err() != nil
	if report.Cancelled {
		// A late successful response must not survive explicit cancellation.
		for i := range report.Results {
			report.Results[i].Status = Skipped
			report.Results[i].Code = "cancelled"
			report.Results[i].ObservedIP = ""
		}
	}
	report.DurationMS = time.Since(start).Milliseconds()
	return report
}

func active(req Request) bool {
	return req.Binding != nil && req.Binding.Connected && req.Binding.Generation > 0 && req.Binding.ResourceID != ""
}

func contextExpired(ctx context.Context) bool {
	deadline, hasDeadline := ctx.Deadline()
	return ctx.Err() != nil || hasDeadline && !time.Now().Before(deadline)
}
