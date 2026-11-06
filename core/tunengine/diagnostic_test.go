package tunengine

import (
	"context"
	"testing"

	"bluegrass/native/core/diagnostics"
)

func TestTunDiagnoseChangedObservationPreservesSessionAndReturnsEvidence(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if e.Prepare(context.Background(), testProfile()) != nil {
		t.Fatal("prepare")
	}
	if _, err := e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetBusinessGate(true) != nil {
		t.Fatal("gate")
	}
	r.probe = func(context.Context) (string, error) { return "1.1.1.1", nil }
	report, err := e.Diagnose(context.Background(), diagnostics.Binding{Connected: true, Generation: 40, ResourceID: "fixture-resource"})
	if err != nil || report.Generation != 40 || report.Cancelled {
		t.Fatal("diagnosis contract", err)
	}
	for _, v := range report.Results {
		if v.ID == "proxy_exit" && (v.Code != "observed_exit" || v.Status != diagnostics.Passed) {
			t.Fatal("missing evidence")
		}
	}
	e.mu.Lock()
	runtime := e.runtime
	e.mu.Unlock()
	if runtime != r || e.VerifiedIPv4() != "1.1.1.1" {
		t.Fatal("observation closed runtime or failed to update")
	}
}

func TestTunStaleDiagnosticCannotStopNewSession(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	old := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, old)
	_ = e.Prepare(context.Background(), testProfile())
	_, _ = e.ProbeExit(context.Background())
	_ = e.SetBusinessGate(true)
	old.probe = func(context.Context) (string, error) { close(entered); <-release; return "1.1.1.1", nil }
	done := make(chan error, 1)
	go func() {
		report, err := e.Diagnose(context.Background(), diagnostics.Binding{Connected: true, Generation: 40, ResourceID: "fixture-resource"})
		if !report.Cancelled {
			t.Error("stale report not cancelled")
		}
		done <- err
	}()
	<-entered
	next := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e.factory = func(context.Context, preparedConfig, *sessionGate) (runtimeEngine, error) { return next, nil }
	_ = e.Prepare(context.Background(), testProfile())
	_, _ = e.ProbeExit(context.Background())
	_ = e.SetBusinessGate(true)
	close(release)
	if <-done == nil || e.VerifiedIPv4() != "9.9.9.9" {
		t.Fatal("old mismatch affected new session")
	}
}

func TestCancelledTunDiagnosticDoesNotRetireVerifiedSession(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if e.Prepare(context.Background(), testProfile()) != nil {
		t.Fatal("prepare")
	}
	if _, err := e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetBusinessGate(true) != nil {
		t.Fatal("gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := e.Diagnose(ctx, diagnostics.Binding{Connected: true, Generation: 7, ResourceID: "fixture-resource"})
	if err == nil || !report.Cancelled {
		t.Fatal("cancelled diagnostic retained observation")
	}
	e.mu.Lock()
	runtime := e.runtime
	e.mu.Unlock()
	if e.VerifiedIPv4() != "9.9.9.9" || runtime != r {
		t.Fatal("diagnostic cancellation retired a healthy verified session")
	}
}
