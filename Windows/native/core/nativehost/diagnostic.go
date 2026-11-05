package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
)

const diagnosticTimeout = 30 * time.Second

// Optional, trusted in-process capability. It takes no caller-selected probe
// targets or credentials. Engines bind all sockets and observations to the
// captured session; concurrent gate closure must cancel that session promptly.
type diagnosticEngine interface {
	Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)
}

type diagnosticStatus struct {
	State      string                 `json:"state"`
	Context    control.ControlContext `json:"context"`
	Generation uint64                 `json:"generation"`
	Report     *diagnostics.Report    `json:"report,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

type diagnosticCompletion struct {
	report *diagnostics.Report
	state  string
	code   string
}

type diagnosticJob struct {
	status     diagnosticStatus // accessed only by the host's serialized dispatch
	requestID  string
	controller *control.Controller
	binding    diagnostics.Binding
	cancel     context.CancelFunc
	result     <-chan diagnosticCompletion
}

func diagnosticCurrent(c *control.Controller, expected control.ControlContext) bool {
	s := c.Snapshot()
	return s.Context != nil && *s.Context == expected && s.Connection == "connected" &&
		s.GateKnown && s.BusinessGateOpen && !s.Expired && !gate.Expired(s.Time.EffectiveUTC, s.ValidUntilUTC)
}

func (h *Host) startDiagnostic(r Request) string {
	if r.Context == nil {
		return "INVALID_CONTEXT"
	}
	if h.controller == nil {
		return "DIAGNOSTIC_UNAVAILABLE"
	}
	if err := h.controller.CheckExpiry(); err != nil {
		return "ENGINE_FAILURE"
	}
	// Preserve ordinary snapshot ownership checks before admitting any probes.
	_ = h.snapshot()
	s := h.controller.Snapshot()
	if s.Context == nil || *r.Context != *s.Context || *r.Context != h.context {
		return "STALE_CONTEXT"
	}
	if !diagnosticCurrent(h.controller, *r.Context) {
		return "DIAGNOSTIC_UNAVAILABLE"
	}
	e, ok := h.engine.(diagnosticEngine)
	if !ok {
		return "DIAGNOSTIC_UNAVAILABLE"
	}
	if old := h.diagnostic; old != nil && old.requestID == r.ID && old.status.Context == *r.Context {
		return ""
	}
	// Cancellation does not free the slot until the engine actually returns.
	// An uncooperative engine can leave at most one worker, never a growing queue.
	if h.diagnosticWorker != nil {
		select {
		case <-h.diagnosticWorker:
		default:
			return "DIAGNOSTIC_BUSY"
		}
	}
	if h.diagnosticGeneration == ^uint64(0) {
		return "DIAGNOSTIC_UNAVAILABLE"
	}
	h.diagnosticGeneration++
	key, _ := json.Marshal(r.Context.ResourceKey)
	digest := sha256.Sum256(key)
	binding := diagnostics.Binding{Connected: true, Generation: h.diagnosticGeneration, ResourceID: hex.EncodeToString(digest[:])}
	ctx, cancel := context.WithTimeout(context.Background(), diagnosticTimeout)
	result := make(chan diagnosticCompletion, 1)
	workerDone := make(chan struct{})
	j := &diagnosticJob{
		status:    diagnosticStatus{State: "running", Context: *r.Context, Generation: binding.Generation},
		requestID: r.ID, controller: h.controller, binding: binding,
		cancel: cancel, result: result,
	}
	h.diagnostic, h.diagnosticWorker = j, workerDone
	go j.run(ctx, e, result, workerDone)
	return ""
}

func (j *diagnosticJob) run(ctx context.Context, e diagnosticEngine, result chan<- diagnosticCompletion, workerDone chan struct{}) {
	defer j.cancel()
	answers := make(chan diagnosticCompletion, 1)
	go func() {
		defer close(workerDone)
		report, err := e.Diagnose(ctx, j.binding)
		out := j.validateReport(report)
		if err != nil {
			if out.state != "cancelled" {
				out = diagnosticCompletion{state: "failed", code: "DIAGNOSTIC_FAILED"}
			}
		}
		answers <- out
	}()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			code := "DIAGNOSTIC_CANCELLED"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "DIAGNOSTIC_TIMEOUT"
			}
			result <- diagnosticCompletion{state: "cancelled", code: code}
			return
		case <-ticker.C:
			if !diagnosticCurrent(j.controller, j.status.Context) {
				j.cancel()
			}
		case out := <-answers:
			if ctx.Err() != nil || !diagnosticCurrent(j.controller, j.status.Context) {
				out = diagnosticCompletion{state: "cancelled", code: "DIAGNOSTIC_CANCELLED"}
			}
			result <- out
			return
		}
	}
}

func (h *Host) cancelDiagnostic() {
	if j := h.diagnostic; j != nil {
		j.cancel()
		j.status.State, j.status.Error, j.status.Report = "cancelled", "DIAGNOSTIC_CANCELLED", nil
	}
}

func (h *Host) collectDiagnostic() {
	j := h.diagnostic
	if j == nil {
		return
	}
	if h.controller != j.controller || h.context != j.status.Context {
		h.cancelDiagnostic()
		h.diagnostic = nil
		return
	}
	if j.status.State != "cancelled" && !diagnosticCurrent(j.controller, j.status.Context) {
		h.cancelDiagnostic()
		return
	}
	if j.status.State != "running" {
		return
	}
	select {
	case out := <-j.result:
		// run supplies only validated, generation-bound completed reports.
		// Recheck the full context atomically at publication, since expiry or
		// cancellation can invalidate it after the earlier snapshot check.
		if out.state == "completed" && out.report != nil {
			for _, r := range out.report.Results {
				if r.ID == "proxy_exit" && r.Status == diagnostics.Passed && r.Code == "observed_exit" {
					if !j.controller.PublishObservedExit(j.status.Context, r.ObservedIP) {
						out = diagnosticCompletion{state: "cancelled", code: "DIAGNOSTIC_CANCELLED"}
					}
					break
				}
			}
		}
		j.status.State, j.status.Error, j.status.Report = out.state, out.code, out.report
	default:
	}
}

func (h *Host) diagnosticEnvelope() *diagnosticStatus {
	if h.diagnostic == nil {
		return nil
	}
	s := h.diagnostic.status
	if s.Report != nil {
		report := *s.Report
		report.Results = append([]diagnostics.Result(nil), report.Results...)
		s.Report = &report
	}
	return &s
}

func (j *diagnosticJob) validateReport(report diagnostics.Report) diagnosticCompletion {
	invalid := diagnosticCompletion{state: "failed", code: "DIAGNOSTIC_INVALID_REPORT"}
	fingerprint := sha256.Sum256([]byte(j.binding.ResourceID))
	if report.Version != 1 || report.Generation != j.binding.Generation || report.ResourceFingerprint != hex.EncodeToString(fingerprint[:16]) ||
		report.StartedAtUTC.IsZero() || report.DurationMS < 0 || report.DurationMS > 60000 || len(report.Results) == 0 || len(report.Results) > 24 {
		return invalid
	}
	if report.Cancelled {
		return diagnosticCompletion{state: "cancelled", code: "DIAGNOSTIC_CANCELLED"}
	}
	seen := map[string]bool{}
	for _, r := range report.Results {
		if !diagnosticResultID(r.ID) || seen[r.ID] || !diagnosticResultCode(r.Code) ||
			r.DurationMS < 0 || r.DurationMS > 60000 || r.Attempts < 0 || r.Attempts > 16 || r.Candidate < 0 || r.Candidate > 8 ||
			(r.Status != diagnostics.Passed && r.Status != diagnostics.Failed && r.Status != diagnostics.Skipped) {
			return invalid
		}
		seen[r.ID] = true
		if r.ObservedIP != "" && (r.ID != "proxy_exit" || !exitprobe.ValidObservedAddress(r.ObservedIP)) {
			return invalid
		}
		if r.Code == "observed_exit" && (r.ID != "proxy_exit" || r.Status != diagnostics.Passed || r.ObservedIP == "") {
			return invalid
		}
	}
	data, err := json.Marshal(report)
	if err != nil || len(data) > 16384 {
		return invalid
	}
	report.Results = append([]diagnostics.Result(nil), report.Results...)
	return diagnosticCompletion{state: "completed", report: &report}
}

func diagnosticResultID(id string) bool {
	switch id {
	case "api_health", "entry_dns", "entry_tcp", "proxy_exit", "proxy_udp", "physical_bypass", "entry_engine_policy",
		"account_auth", "resource_sync", "resource_eligibility", "proxy_authentication", "udp_exit_identity", "ipv6_routing",
		"system_proxy", "tun", "network_switch", "wfp_protection":
		return true
	}
	return false
}

func diagnosticResultCode(code string) bool {
	switch code {
	case "cancelled", "cancelled_or_budget_exhausted", "invalid_request", "probe_timeout", "not_measured",
		"not_configured", "https_health_failed", "invalid_health_response", "https_health_ok", "literal_address",
		"system_dns_failed", "dns_no_addresses", "system_dns_answer", "custom_dns_answer", "custom_dns_failed",
		"dns_unavailable", "tcp_connected", "tcp_connect_failed", "no_active_binding",
		"probe_not_configured", "selected_channel_unavailable", "observed_exit", "exit_probe_failed",
		"udp_channel_unavailable", "udp_channel_failed", "udp_deadline_failed", "nonce_failed", "udp_send_failed",
		"udp_receive_failed", "udp_reply_mismatch", "udp_nonce_round_trip":
		return true
	}
	return false
}
