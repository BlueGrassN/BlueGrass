package localengine

import (
	"context"
	"errors"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/exitprobe"
)

// Diagnose uses the authenticated host binding only for the report. It captures
// the current selected transport independently. Observations cannot revoke it.
// All network work runs without holding mu.
func (e *Local) Diagnose(ctx context.Context, binding diagnostics.Binding) (diagnostics.Report, error) {
	e.mu.Lock()
	dial, epoch, session, open := e.dial, e.generation, e.session, e.gate
	if e.selectedDial != nil {
		dial = e.selectedDial
	}
	e.mu.Unlock()
	if !open || dial == nil || session == nil || session.Err() != nil {
		return exitprobe.Diagnostic(ctx, binding, "", nil), nil
	}
	ctx, cancel := context.WithTimeout(ctx, exitprobe.TotalTimeout)
	defer cancel()
	unhook := context.AfterFunc(session, cancel)
	defer unhook()
	report := exitprobe.Diagnostic(ctx, binding, "", func(ctx context.Context) (string, error) { return exitprobe.Probe(ctx, dial, exitprobe.Options{}) })
	return e.finishDiagnostic(ctx, epoch, session, report)
}

func (e *Local) finishDiagnostic(ctx context.Context, epoch uint64, session context.Context, report diagnostics.Report) (diagnostics.Report, error) {
	e.mu.Lock()
	if ctx.Err() != nil || session.Err() != nil || epoch != e.generation || !e.gate || report.Cancelled {
		e.mu.Unlock()
		return exitprobe.CancelDiagnostic(report), errors.New("STALE_DIAGNOSTIC")
	}
	for _, result := range report.Results {
		if result.ID == "proxy_exit" && result.Status == diagnostics.Passed && result.Code == "observed_exit" && exitprobe.ValidObservedAddress(result.ObservedIP) {
			e.observed = result.ObservedIP
		}
	}
	e.mu.Unlock()
	return report, nil
}
