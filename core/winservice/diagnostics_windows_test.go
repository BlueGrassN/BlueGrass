//go:build windows

package winservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/tunengine"
)

type diagnosticMethod interface {
	Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)
}
type diagnosticTunnelFixture struct {
	*tunnelFixture
	run func(context.Context, diagnostics.Binding) (diagnostics.Report, error)
}

func (d *diagnosticTunnelFixture) Diagnose(ctx context.Context, b diagnostics.Binding) (diagnostics.Report, error) {
	return d.run(ctx, b)
}

var diagnosticBinding = diagnostics.Binding{Connected: true, Generation: 7, ResourceID: "full-resource-binding"}

func diagnosticReport(ip string) diagnostics.Report {
	return diagnostics.Report{Version: 1, Generation: 7, Results: []diagnostics.Result{{ID: "proxy_exit", Status: diagnostics.Passed, Code: "observed_exit", ObservedIP: ip}, {ID: "proxy_udp", Status: diagnostics.Skipped, Code: "not_configured"}}}
}
func diagnosticFixture(t *testing.T, run func(context.Context, diagnostics.Binding) (diagnostics.Report, error)) (*compositionFixture, diagnosticMethod) {
	t.Helper()
	f := newCompositionFixture()
	factory := f.e.newTun
	f.e.newTun = func(c tunengine.Config) (ownedTunnel, error) {
		v, err := factory(c)
		if err != nil {
			return v, err
		}
		return &diagnosticTunnelFixture{tunnelFixture: v.(*tunnelFixture), run: run}, nil
	}
	d, ok := any(f.e).(diagnosticMethod)
	if !ok {
		t.Fatal("protectedTunnel is missing the optional Diagnose method")
	}
	return f, d
}
func diagnosticReady(t *testing.T, f *compositionFixture) {
	t.Helper()
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedDiagnosticsNeedsCurrentAllowedSessionAndForwardsExactBinding(t *testing.T) {
	calls := 0
	f, d := diagnosticFixture(t, func(ctx context.Context, b diagnostics.Binding) (diagnostics.Report, error) {
		calls++
		if b != diagnosticBinding {
			t.Fatal("binding rewritten", b)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("diagnostic budget missing")
		}
		return diagnosticReport("1.1.1.1"), nil
	})
	if _, err := d.Diagnose(context.Background(), diagnosticBinding); err == nil || calls != 0 {
		t.Fatal("unprepared diagnostic ran")
	}
	f.prepare(t)
	if _, err := d.Diagnose(context.Background(), diagnosticBinding); err == nil || calls != 0 {
		t.Fatal("diagnostic ran before business gate and WFP permission")
	}
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	r, err := d.Diagnose(context.Background(), diagnosticBinding)
	if err != nil || calls != 1 || r.Generation != 7 || len(r.Results) != 2 || r.Results[0].Code != "observed_exit" || r.Results[0].Status != diagnostics.Passed || r.Results[1].Code != "not_configured" || f.log.count("release") != 0 || f.log.count("probe") != 0 || f.e.VerifiedIPv4() != "" {
		t.Fatal("forwarded report changed or released WFP", r, err)
	}
}

func TestProtectedDiagnosticsObservationsAndDetectorFailureAreAdvisory(t *testing.T) {
	want := diagnosticReport("1.1.1.1")
	f, d := diagnosticFixture(t, func(context.Context, diagnostics.Binding) (diagnostics.Report, error) {
		return want, nil
	})
	diagnosticReady(t, f)
	current := f.e.tun
	before := f.log.count("block")
	reports := []diagnostics.Report{diagnosticReport("1.1.1.1"), diagnosticReport("8.8.8.8")}
	failed := diagnosticReport("")
	failed.Results[0].Status, failed.Results[0].Code = diagnostics.Failed, "exit_probe_failed"
	reports = append(reports, failed)
	// A stale producer's legacy status must not reinstate IP enforcement either.
	legacy := diagnosticReport("8.8.8.8")
	legacy.Results[0].Status, legacy.Results[0].Code = diagnostics.Failed, "exit_mismatch"
	reports = append(reports, legacy)
	for _, report := range reports {
		want = report
		r, err := d.Diagnose(context.Background(), diagnosticBinding)
		if err != nil || len(r.Results) == 0 || r.Results[0] != want.Results[0] {
			t.Fatal("advisory result changed", r, err)
		}
		if f.log.count("block") != before || f.log.count("close-tun") != 0 || f.log.count("release") != 0 || f.log.count("clear-cache") != 0 || !f.e.Active() || !f.e.prepared || f.e.tun != current {
			t.Fatal("diagnostic disrupted current connection", r, f.log.snapshot())
		}
	}
}

func TestProtectedDiagnosticsDoesNotHoldEngineLockAndLateObservationCannotAffectReplacement(t *testing.T) {
	entered, complete := make(chan struct{}), make(chan struct{})
	var probeContext context.Context
	f, d := diagnosticFixture(t, func(ctx context.Context, _ diagnostics.Binding) (diagnostics.Report, error) {
		probeContext = ctx
		close(entered)
		<-complete
		return diagnosticReport("8.8.8.8"), nil
	})
	diagnosticReady(t, f)
	done := make(chan error, 1)
	go func() {
		r, err := d.Diagnose(context.Background(), diagnosticBinding)
		if len(r.Results) != 0 {
			done <- errors.New("stale report escaped")
			return
		}
		done <- err
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- f.e.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(complete)
		t.Fatal("diagnostic held e.mu across asynchronous work")
	}
	select {
	case <-probeContext.Done():
	case <-time.After(time.Second):
		close(complete)
		t.Fatal("Stop did not cancel the captured diagnostic")
	}
	diagnosticReady(t, f)
	before := f.log.count("block")
	close(complete)
	if err := <-done; err == nil {
		t.Fatal("late observation accepted")
	}
	if f.log.count("block") != before || !f.e.prepared || f.log.count("release") != 0 {
		t.Fatal("late observation harmed replacement")
	}
}

func TestProtectedDiagnosticsRejectsUnsupportedInvalidAndWrongGeneration(t *testing.T) {
	f := newCompositionFixture()
	diagnosticReady(t, f)
	if _, err := f.e.Diagnose(context.Background(), diagnosticBinding); err == nil {
		t.Fatal("unsupported runtime accepted diagnostics")
	}
	calls := 0
	f, d := diagnosticFixture(t, func(context.Context, diagnostics.Binding) (diagnostics.Report, error) {
		calls++
		r := diagnosticReport("8.8.8.8")
		r.Generation++
		return r, nil
	})
	diagnosticReady(t, f)
	before := f.log.count("block")
	if _, err := d.Diagnose(context.Background(), diagnostics.Binding{}); err == nil || calls != 0 {
		t.Fatal("invalid binding reached runtime")
	}
	if r, err := d.Diagnose(context.Background(), diagnosticBinding); err == nil || len(r.Results) != 0 || f.log.count("block") != before {
		t.Fatal("wrong generation altered current session", r, err)
	}
}

func TestProtectedDiagnosticsCancellationAndOrdinaryFailureDoNotReleaseOrRevoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f, d := diagnosticFixture(t, func(context.Context, diagnostics.Binding) (diagnostics.Report, error) {
		cancel()
		return diagnosticReport("8.8.8.8"), nil
	})
	diagnosticReady(t, f)
	before := f.log.count("block")
	if r, err := d.Diagnose(ctx, diagnosticBinding); !errors.Is(err, context.Canceled) || len(r.Results) != 0 || f.log.count("block") != before {
		t.Fatal("cancelled observation applied", r, err)
	}
	f, d = diagnosticFixture(t, func(context.Context, diagnostics.Binding) (diagnostics.Report, error) {
		return diagnostics.Report{}, errors.New("probe timeout")
	})
	diagnosticReady(t, f)
	before = f.log.count("block")
	if _, err := d.Diagnose(context.Background(), diagnosticBinding); err == nil || f.log.count("block") != before || f.log.count("release") != 0 {
		t.Fatal("ordinary probe failure changed protection")
	}
}
