package localengine

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/exitprobe"
)

func TestLocalDiagnoseSelectedOnlyAndGateClosureCancels(t *testing.T) {
	e := probeLocal(t)
	entered := make(chan struct{}, 1)
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "api.ipify.org:443" {
			t.Error("unexpected diagnostic target")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("no bounded dial")
		}
		entered <- struct{}{}
		<-ctx.Done()
		return nil, errors.New("fixture-secret")
	}
	done := make(chan error, 1)
	go func() {
		r, err := e.Diagnose(context.Background(), diagnostics.Binding{Connected: true, Generation: 4, ResourceID: "fixture-resource"})
		if !r.Cancelled {
			t.Error("cancellation missing")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("diagnosis did not dial")
	}
	_ = e.SetBusinessGate(false)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale result accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("diagnosis held gate lock")
	}
}

func TestLocalDiagnosticObservationIsAtomicAndCannotUpdateReplacement(t *testing.T) {
	for _, stale := range []bool{false, true} {
		e := probeLocal(t)
		epoch, session := e.generation, e.session
		report := exitprobe.Diagnostic(context.Background(), diagnostics.Binding{Connected: true, Generation: 4, ResourceID: "fixture-resource"}, "", func(context.Context) (string, error) { return "9.9.9.9", nil })
		if stale {
			_ = e.SetBusinessGate(false)
			e.observed, e.gate = "1.1.1.1", true
		}
		result, err := e.finishDiagnostic(context.Background(), epoch, session, report)
		if stale {
			if err == nil || !result.Cancelled || !e.gate || e.VerifiedIPv4() != "1.1.1.1" {
				t.Fatal("stale mismatch retired replacement")
			}
		} else if err != nil || result.Cancelled || !e.gate || e.VerifiedIPv4() != "9.9.9.9" {
			t.Fatal("current observation failed or retired session")
		}
	}
}
