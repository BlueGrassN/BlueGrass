package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/engine"
)

type diagnosticAnswer struct {
	report diagnostics.Report
	err    error
}
type diagnosticCall struct {
	ctx     context.Context
	binding diagnostics.Binding
	answer  chan diagnosticAnswer
}
type diagnosticTestEngine struct {
	trafficSamplingEngine
	calls    chan diagnosticCall
	probeErr error
}

func (e *diagnosticTestEngine) VerifiedIPv4() string { return "1.1.1.1" }
func (e *diagnosticTestEngine) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	if e.probeErr != nil {
		return engine.ProbeResult{}, e.probeErr
	}
	return e.trafficSamplingEngine.ProbeExit(ctx)
}
func (e *diagnosticTestEngine) Diagnose(ctx context.Context, binding diagnostics.Binding) (diagnostics.Report, error) {
	call := diagnosticCall{ctx, binding, make(chan diagnosticAnswer, 1)}
	e.calls <- call
	// Deliberately permit late results after cancellation, exercising the host's
	// own stale-result barrier rather than relying on a cooperative engine.
	a := <-call.answer
	return a.report, a.err
}
func diagnosticHost(t *testing.T, mode string) (*Host, *diagnosticTestEngine, *trafficSamplingClock) {
	t.Helper()
	return diagnosticHostWithProbeError(t, mode, nil)
}
func diagnosticHostWithProbeError(t *testing.T, mode string, probeErr error) (*Host, *diagnosticTestEngine, *trafficSamplingClock) {
	t.Helper()
	e := &diagnosticTestEngine{trafficSamplingEngine: trafficSamplingEngine{
		probe: make(chan struct{}), opened: make(chan struct{}, 1),
	}, calls: make(chan diagnosticCall, 4), probeErr: probeErr}
	h := dnsHost(t, e, mode)
	h.owner = control.Owner{Environment: "test", DataSource: "fake", AccountKey: "account", ClientEpoch: h.client}
	clk := &trafficSamplingClock{reading: h.clock.Read()}
	var err error
	h.controller, err = control.New(e, clk, h.runtime, h.owner)
	if err != nil {
		t.Fatal(err)
	}
	if r := h.Handle(dnsRequest(t, "diagnostic-connect", mode, nil)); !r.OK {
		t.Fatal(r.Error)
	}
	close(e.probe)
	select {
	case <-e.opened:
	case <-time.After(time.Second):
		t.Fatal("gate did not open")
	}
	e.active = true
	return h, e, clk
}
func diagnoseRequest(t *testing.T, id string, current any) Request {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"id": id, "command": "Diagnose", "context": current})
	r, err := Decode(b)
	if err != nil {
		t.Fatalf("Diagnose context is absent from the wire schema: %v", err)
	}
	return r
}

type diagnosticWire struct {
	State      string                 `json:"state"`
	Context    control.ControlContext `json:"context"`
	Generation uint64                 `json:"generation"`
	Report     *diagnostics.Report    `json:"report"`
	Error      string                 `json:"error"`
}

func diagnosticEnvelope(t *testing.T, response Response) *diagnosticWire {
	t.Helper()
	b, err := json.Marshal(response.Snapshot["diagnostic"])
	if err != nil {
		t.Fatal(err)
	}
	var result *diagnosticWire
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func nextDiagnosticCall(t *testing.T, e *diagnosticTestEngine) diagnosticCall {
	t.Helper()
	select {
	case call := <-e.calls:
		t.Cleanup(func() {
			select {
			case call.answer <- diagnosticAnswer{err: context.Canceled}:
			default:
			}
		})
		deadline, bounded := call.ctx.Deadline()
		if !bounded || time.Until(deadline) > 30*time.Second || !call.binding.Connected || call.binding.Generation == 0 || call.binding.ResourceID == "" || len(call.binding.ResourceID) > 256 {
			t.Fatal("engine did not receive a bounded current-session binding")
		}
		return call
	case <-time.After(time.Second):
		t.Fatal("diagnostic worker did not start")
		return diagnosticCall{}
	}
}
func diagnosticResult(binding diagnostics.Binding, result diagnostics.Result) diagnostics.Report {
	report := (diagnostics.Runner{}).Run(context.Background(), diagnostics.Request{Binding: &binding})
	if result.ID != "" {
		for i := range report.Results {
			if report.Results[i].ID == result.ID {
				report.Results[i] = result
			}
		}
	}
	return report
}
func waitDiagnostic(t *testing.T, h *Host, state string) Response {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r := h.Handle(Request{ID: "diagnostic-status", Command: "GetStatus"})
		if d := diagnosticEnvelope(t, r); d != nil && d.State == state {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("diagnostic did not reach %s", state)
	return Response{}
}

func TestDiagnoseAsyncBoundReportAndLatestOnly(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		t.Run(mode, func(t *testing.T) {
			h, e, _ := diagnosticHost(t, mode)
			r := h.Handle(diagnoseRequest(t, "diagnose-1", h.context))
			d := diagnosticEnvelope(t, r)
			if !r.OK || d == nil || d.State != "running" || d.Context != h.context || d.Generation != 1 || d.Report != nil {
				t.Fatalf("diagnostic was not acknowledged asynchronously: %+v", r)
			}
			call := nextDiagnosticCall(t, e)
			keyDigest := sha256.Sum256([]byte(`{"environment":"test","dataSource":"fake","accountKey":"account","kind":"asset","sourceId":"dns-resource"}`))
			if call.binding.ResourceID != hex.EncodeToString(keyDigest[:]) || call.binding.Generation != d.Generation {
				t.Fatal("engine binding did not cover the entire authenticated resource key")
			}
			if retry := h.Handle(diagnoseRequest(t, "diagnose-1", h.context)); !retry.OK || diagnosticEnvelope(t, retry).Generation != 1 {
				t.Fatal("retry did not retain the same diagnostic job")
			}
			if busy := h.Handle(diagnoseRequest(t, "diagnose-busy", h.context)); busy.OK || busy.Error != "DIAGNOSTIC_BUSY" {
				t.Fatal("parallel diagnostic work was admitted")
			}
			if status := h.Handle(Request{ID: "while-running", Command: "GetStatus"}); !status.OK || status.Snapshot["businessGateOpen"] != true {
				t.Fatal("diagnostics blocked ordinary status requests")
			}
			call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, diagnostics.Result{})}
			done := diagnosticEnvelope(t, waitDiagnostic(t, h, "completed"))
			if done.Report == nil || done.Report.Generation != done.Generation || done.Report.ResourceFingerprint == "" || done.Context != h.context {
				t.Fatal("completed report lost its authenticated binding")
			}
			second := h.Handle(diagnoseRequest(t, "diagnose-2", h.context))
			if d = diagnosticEnvelope(t, second); !second.OK || d.Generation != 2 || d.Report != nil || d.State != "running" {
				t.Fatal("new diagnostic retained an old report")
			}
			nextDiagnosticCall(t, e)
		})
	}
}

func TestDiagnoseRejectsContextAndCallerConfiguration(t *testing.T) {
	h, e, _ := diagnosticHost(t, "tun")
	old := h.context
	old.AttemptID = "old-attempt"
	for _, current := range []any{nil, control.ControlContext{}, old} {
		r := h.Handle(diagnoseRequest(t, "bad-context", current))
		if r.OK || r.Error != "INVALID_CONTEXT" && r.Error != "STALE_CONTEXT" {
			t.Fatal("unbound diagnostic request accepted")
		}
	}
	for _, field := range []string{"exit_endpoints", "udp", "credentials", "binding", "timeout_ms"} {
		b, _ := json.Marshal(map[string]any{"id": "untrusted", "command": "Diagnose", "context": h.context, field: "untrusted"})
		if _, err := Decode(b); err == nil {
			t.Fatalf("caller-controlled %s entered diagnostic schema", field)
		}
	}
	for _, command := range []string{"Hello", "GetStatus", "Pause", "Disconnect", "Connect", "ApplyResourceUpdate", "ResolveControlHost"} {
		r := diagnoseRequest(t, "context-on-other-command", h.context)
		r.Command = command
		if reply := h.Handle(r); reply.OK || reply.Error != "INVALID_PARAMETERS" {
			t.Fatalf("diagnostic context accepted on %s", command)
		}
	}
	select {
	case <-e.calls:
		t.Fatal("rejected request reached the engine")
	default:
	}
}

func TestDiagnoseCancellationDiscardsLateObservation(t *testing.T) {
	for _, transition := range []string{"pause", "disconnect", "network", "close", "switch", "expiry", "unavailable"} {
		t.Run(transition, func(t *testing.T) {
			h, e, clk := diagnosticHost(t, "tun")
			if r := h.Handle(diagnoseRequest(t, "diagnose", h.context)); !r.OK {
				t.Fatal(r.Error)
			}
			call := nextDiagnosticCall(t, e)
			switch transition {
			case "pause", "disconnect":
				command := "Pause"
				if transition == "disconnect" {
					command = "Disconnect"
				}
				if r := h.Handle(Request{ID: "stop", Command: command}); !r.OK {
					t.Fatal(r.Error)
				}
			case "network":
				if err := h.NetworkChanged(); err != nil {
					t.Fatal(err)
				}
			case "close":
				if err := h.Close(); err != nil {
					t.Fatal(err)
				}
			case "switch":
				if r := h.Handle(dnsRequest(t, "switch", "tun", nil)); !r.OK {
					t.Fatal(r.Error)
				}
				select {
				case <-e.opened:
				case <-time.After(time.Second):
					t.Fatal("replacement session did not connect")
				}
				if r := h.Handle(diagnoseRequest(t, "while-old-worker-exists", h.context)); r.OK || r.Error != "DIAGNOSTIC_BUSY" {
					t.Fatal("cancelled uncooperative worker allowed unbounded replacement work")
				}
			case "expiry":
				until := time.Now().UTC().Add(time.Hour)
				data, _ := json.Marshal(control.ResourceUpdate{Revision: 2, ValidUntilUTC: &until})
				if r := h.Handle(Request{ID: "expiry-deadline", Command: "ApplyResourceUpdate", Update: data}); !r.OK {
					t.Fatal(r.Error)
				}
				clk.mu.Lock()
				clk.reading.EffectiveUTC = until.Add(time.Second)
				clk.mu.Unlock()
				if err := h.controller.CheckExpiry(); err != nil {
					t.Fatal(err)
				}
			case "unavailable":
				if r := h.Handle(Request{ID: "unavailable", Command: "ApplyResourceUpdate", Update: json.RawMessage(`{"revision":2,"validUntilUtc":null,"unavailable":true}`)}); !r.OK {
					t.Fatal(r.Error)
				}
			}
			select {
			case <-call.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("transition did not cancel the diagnostic context")
			}
			call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: "8.8.8.8"})}
			// Let the deliberately late engine return; repeated snapshots must
			// never publish its report or stop the replacement session.
			for i := 0; i < 3; i++ {
				r := h.Handle(Request{ID: "after-cancel", Command: "GetStatus"})
				if d := diagnosticEnvelope(t, r); d != nil && d.Report != nil {
					t.Fatal("cancelled diagnostic leaked a report")
				}
				if r.Snapshot["verifiedExitIpv4"] == "8.8.8.8" {
					t.Fatal("cancelled diagnostic published a late observation")
				}
				if transition == "switch" && r.Snapshot["businessGateOpen"] != true {
					t.Fatal("late observation stopped the replacement session")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestDiagnoseObservationAndUDPFailureNeverPause(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		for _, observe := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "udp-failure", true: "exit-observation"}[observe], func(t *testing.T) {
				h, e, _ := diagnosticHost(t, mode)
				proxy := &testProxy{applied: true}
				if mode == "system_proxy" {
					h.proxy = proxy
				}
				if r := h.Handle(diagnoseRequest(t, "diagnose", h.context)); !r.OK {
					t.Fatal(r.Error)
				}
				call := nextDiagnosticCall(t, e)
				result := diagnostics.Result{ID: "proxy_udp", Status: diagnostics.Failed, Code: "udp_receive_failed"}
				if observe {
					result = diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: "8.8.8.8"}
				}
				call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, result)}
				r := waitDiagnostic(t, h, "completed")
				if r.Snapshot["businessGateOpen"] != true || r.Snapshot["protectionIntent"] != true || e.releaseCalls != 0 || !proxy.applied || proxy.restores != 0 {
					t.Fatal("diagnostic result changed the wrong forwarding/protection state")
				}
			})
		}
	}
}

func TestDiagnoseRejectsInvalidOrUnboundedReports(t *testing.T) {
	for _, variant := range []string{"generation", "fingerprint", "results", "oversized-code", "private-ip", "error", "legacy-match", "legacy-mismatch", "missing-observation", "failed-observation"} {
		t.Run(variant, func(t *testing.T) {
			h, e, _ := diagnosticHost(t, "tun")
			if r := h.Handle(diagnoseRequest(t, "diagnose", h.context)); !r.OK {
				t.Fatal(r.Error)
			}
			call := nextDiagnosticCall(t, e)
			report := diagnosticResult(call.binding, diagnostics.Result{})
			var engineErr error
			switch variant {
			case "generation":
				report.Generation++
			case "fingerprint":
				report.ResourceFingerprint = "wrong-resource"
			case "results":
				report.Results = make([]diagnostics.Result, 25)
			case "oversized-code":
				report.Results[0].Code = strings.Repeat("x", 20000)
			case "private-ip":
				report.Results[0].ObservedIP = "10.0.0.1"
			case "error":
				engineErr = errors.New("private engine detail")
			case "legacy-match":
				report = diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "expected_exit_match", ObservedIP: "1.1.1.1"})
			case "legacy-mismatch":
				report = diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Failed, Code: "exit_mismatch", ObservedIP: "8.8.8.8"})
			case "missing-observation":
				report = diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit"})
			case "failed-observation":
				report = diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Failed, Code: "observed_exit", ObservedIP: "8.8.8.8"})
			}
			call.answer <- diagnosticAnswer{report, engineErr}
			r := waitDiagnostic(t, h, "failed")
			d := diagnosticEnvelope(t, r)
			b, _ := json.Marshal(r)
			if d.Report != nil || len(b) > 16384 || strings.Contains(string(b), "private engine detail") || r.Snapshot["businessGateOpen"] != true {
				t.Fatal("invalid report escaped sanitization or stopped the session")
			}
			if r.Snapshot["verifiedExitIpv4"] == "8.8.8.8" {
				t.Fatal("invalid report published an observation")
			}
		})
	}
}

func TestDiagnoseUnavailableWithoutEngineCapabilityOrOpenGate(t *testing.T) {
	h, _ := trafficSamplingHost(t)
	if r := h.Handle(diagnoseRequest(t, "unsupported", h.context)); r.OK || r.Error != "DIAGNOSTIC_UNAVAILABLE" {
		t.Fatal("missing diagnostic capability fell back to another transport")
	}
	active, e, _ := diagnosticHost(t, "tun")
	if r := active.Handle(Request{ID: "pause", Command: "Pause"}); !r.OK {
		t.Fatal(r.Error)
	}
	if r := active.Handle(diagnoseRequest(t, "paused", active.context)); r.OK || r.Error != "DIAGNOSTIC_UNAVAILABLE" {
		t.Fatal("paused session admitted diagnostic traffic")
	}
	select {
	case <-e.calls:
		t.Fatal("closed gate reached diagnostic engine")
	default:
	}
}

func TestDiagnoseDeadlineDoesNotWaitForUncooperativeEngine(t *testing.T) {
	h, e, _ := diagnosticHost(t, "tun")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	results := make(chan diagnosticCompletion, 1)
	done := make(chan struct{})
	j := &diagnosticJob{controller: h.controller, status: diagnosticStatus{Context: h.context},
		binding: diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "test-resource"}, cancel: cancel}
	go j.run(ctx, e, results, done)
	call := nextDiagnosticCall(t, e)
	select {
	case result := <-results:
		if result.state != "cancelled" || result.code != "DIAGNOSTIC_TIMEOUT" || result.report != nil || call.ctx.Err() == nil {
			t.Fatal("deadline retained a report or an active diagnostic context")
		}
	case <-time.After(time.Second):
		t.Fatal("diagnostic deadline waited for the uncooperative engine")
	}
	select {
	case <-done:
		t.Fatal("deadline incorrectly freed the engine worker slot")
	default:
	}
	call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, diagnostics.Result{})}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("returned engine did not free the worker slot")
	}
}

func TestDiagnoseEngineFailureKeepsConnectionAndHidesPartialObservation(t *testing.T) {
	h, e, _ := diagnosticHost(t, "tun")
	if r := h.Handle(diagnoseRequest(t, "diagnose", h.context)); !r.OK {
		t.Fatal(r.Error)
	}
	call := nextDiagnosticCall(t, e)
	call.answer <- diagnosticAnswer{
		report: diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: "8.8.8.8"}),
		err:    errors.New("private cleanup detail"),
	}
	r := waitDiagnostic(t, h, "failed")
	d := diagnosticEnvelope(t, r)
	b, _ := json.Marshal(r)
	if d.Report != nil || d.Error != "DIAGNOSTIC_FAILED" || r.Snapshot["businessGateOpen"] != true || r.Snapshot["protectionIntent"] != true || e.releaseCalls != 0 || strings.Contains(string(b), "private cleanup detail") {
		t.Fatal("failed diagnostic changed protection or leaked a partial result")
	}
	if r.Snapshot["verifiedExitIpv4"] == "8.8.8.8" {
		t.Fatal("failed diagnostic published a partial observation")
	}
}
