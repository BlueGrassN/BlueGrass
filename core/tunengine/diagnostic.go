package tunengine

import (
	"context"
	"errors"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/exitprobe"
)

// Diagnose observes the current outbound without changing connectivity policy
// or configuring diagnostic endpoints from IPC.
// Probe uses selected + the fixed proxied-DNS allowlist. Host wrapper retains
// WFP and must discard reports after its own authenticated binding changes.
func (e *Tun) Diagnose(ctx context.Context, binding diagnostics.Binding) (diagnostics.Report, error) {
	e.mu.Lock()
	r, session, epoch, g := e.runtime, e.session, e.generation, e.gate
	networkGeneration := e.prepared.network.Generation
	open := g.isOpen()
	e.mu.Unlock()
	if !open || r == nil || session == nil || session.Err() != nil {
		return exitprobe.Diagnostic(ctx, binding, "", nil), nil
	}
	ctx, cancel := context.WithTimeout(ctx, exitprobe.TotalTimeout)
	defer cancel()
	unhook := context.AfterFunc(session, cancel)
	defer unhook()
	if err := e.checkNetwork(ctx, networkGeneration); err != nil {
		if errors.Is(err, errNetworkChanged) {
			g.retire()
			e.stopIfCurrent(g)
		}
		return exitprobe.CancelDiagnostic(exitprobe.Diagnostic(ctx, binding, "", nil)), err
	}
	report := exitprobe.Diagnostic(ctx, binding, "", r.Probe)
	networkErr := e.checkNetwork(ctx, networkGeneration)
	e.mu.Lock()
	current := epoch == e.generation && e.gate == g && g.isOpen() && session.Err() == nil && ctx.Err() == nil && !report.Cancelled
	if current && networkErr == nil {
		for _, result := range report.Results {
			if result.ID == "proxy_exit" && result.Status == diagnostics.Passed && result.Code == "observed_exit" && exitprobe.ValidObservedAddress(result.ObservedIP) {
				e.observed = result.ObservedIP
			}
		}
	}
	e.mu.Unlock()
	if !current {
		return exitprobe.CancelDiagnostic(report), errors.New("STALE_DIAGNOSTIC")
	}
	if networkErr != nil {
		if errors.Is(networkErr, errNetworkChanged) {
			g.retire()
			e.stopIfCurrent(g)
		}
		return exitprobe.CancelDiagnostic(report), networkErr
	}
	return report, nil
}
