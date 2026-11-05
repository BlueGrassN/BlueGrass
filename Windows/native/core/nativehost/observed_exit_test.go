package nativehost

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/engine"
)

func TestObservedExitHostOptionalProfileAndCurrentObservation(t *testing.T) {
	h, e, _, _ := switchHost(t, "system_proxy")
	r := dnsRequest(t, "no-expected", "system_proxy", nil)
	var profile map[string]any
	_ = json.Unmarshal(r.Profile, &profile)
	delete(profile, "expectedExitIpv4")
	r.Profile, _ = json.Marshal(profile)
	if reply := h.Handle(r); !reply.OK {
		t.Fatal("missing legacy expected field rejected", reply.Error)
	}
	probe := nextSwitchProbe(t, e)
	s := h.snapshot()
	if s["connection"] != "connected" || s["businessGateOpen"] != true || s["systemProxyApplied"] != true || (s["verifiedExitIpv4"] != nil && s["verifiedExitIpv4"] != "") {
		t.Fatal("host did not commit the connection with unknown exit before probing", s)
	}
	probe.answer <- engine.ProbeResult{IPv4: "8.8.8.8", Authenticated: true, Reachable: true}
	deadline := time.Now().Add(time.Second)
	for h.snapshot()["verifiedExitIpv4"] != "8.8.8.8" {
		if time.Now().After(deadline) {
			t.Fatal("host did not publish current controller observation")
		}
		time.Sleep(time.Millisecond)
	}
	if reply := h.Handle(Request{ID: "pause", Command: "Pause"}); !reply.OK || reply.Snapshot["verifiedExitIpv4"] != nil && reply.Snapshot["verifiedExitIpv4"] != "" {
		t.Fatal("paused host retained an observation", reply)
	}
}

type unknownExitDiagnosticEngine struct{ *diagnosticTestEngine }

func (*unknownExitDiagnosticEngine) VerifiedIPv4() string { return "" }

func TestObservedExitDiagnosticNeedsNoCachedExpectation(t *testing.T) {
	h, e, _ := diagnosticHost(t, "tun")
	h.engine = &unknownExitDiagnosticEngine{e}
	if r := h.Handle(diagnoseRequest(t, "unknown-exit", h.context)); !r.OK {
		t.Fatal("unknown exit prevented selected-resource diagnostic", r.Error)
	}
	call := nextDiagnosticCall(t, e)
	call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: "8.8.8.8"})}
	r := waitDiagnostic(t, h, "completed")
	if r.Snapshot["connection"] != "connected" || r.Snapshot["businessGateOpen"] != true {
		t.Fatal("diagnostic observation disconnected session")
	}
	d := diagnosticEnvelope(t, r)
	found := false
	for _, result := range d.Report.Results {
		if result.ID == "proxy_exit" {
			found = result.Code == "observed_exit" && result.ObservedIP == "8.8.8.8"
		}
	}
	if !found {
		t.Fatal("valid current observation absent")
	}
}

func TestObservedExitDiagnosticPublishesCurrentSnapshot(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		for _, initial := range []string{"failed", "observed"} {
			t.Run(mode+"/"+initial, func(t *testing.T) {
				var probeErr error
				if initial == "failed" {
					probeErr = errors.New("initial probe unavailable")
				}
				h, e, _ := diagnosticHostWithProbeError(t, mode, probeErr)
				if initial == "observed" {
					deadline := time.Now().Add(time.Second)
					for h.controller.Snapshot().VerifiedExitIPv4 != "1.1.1.1" {
						if time.Now().After(deadline) {
							t.Fatal("initial observation did not commit")
						}
						time.Sleep(time.Millisecond)
					}
				} else if got := h.controller.Snapshot().VerifiedExitIPv4; got != "" {
					t.Fatal("failed initial probe published an observation", got)
				}
				if r := h.Handle(diagnoseRequest(t, "retry-exit", h.context)); !r.OK {
					t.Fatal(r.Error)
				}
				call := nextDiagnosticCall(t, e)
				before := h.controller.Snapshot().EventSequence
				call.answer <- diagnosticAnswer{report: diagnosticResult(call.binding, diagnostics.Result{
					ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: "8.8.8.8",
				})}
				r := waitDiagnostic(t, h, "completed")
				if got := r.Snapshot["verifiedExitIpv4"]; got != "8.8.8.8" {
					t.Fatalf("completed current diagnostic did not update snapshot: got %v", got)
				}
				s := h.controller.Snapshot()
				if s.VerifiedExitIPv4 != "8.8.8.8" || s.EventSequence != before+1 || s.Connection != "connected" || !s.BusinessGateOpen {
					t.Fatal("observation did not commit exactly one current connected event", s)
				}
				// The engine cache deliberately remains A; snapshots must retain B
				// without re-publishing the same completed job on every status poll.
				for i := 0; i < 3; i++ {
					r = h.Handle(Request{ID: "after-observation", Command: "GetStatus"})
					if r.Snapshot["verifiedExitIpv4"] != "8.8.8.8" || h.controller.Snapshot().EventSequence != before+1 {
						t.Fatal("snapshot used the engine cache or republished a completed diagnostic")
					}
				}
			})
		}
	}
}
