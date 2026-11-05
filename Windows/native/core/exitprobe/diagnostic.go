package exitprobe

import (
	"context"
	"errors"
	"net"
	"time"

	"bluegrass/native/core/diagnostics"
)

// Diagnostic adapts the existing fixed HTTPS probe to the public diagnostic
// envelope. It accepts no endpoint/DNS/UDP settings from the caller, performs no
// direct observations, and never authorizes or revokes a session. The ignored
// legacy expected argument remains temporarily for source compatibility.
func Diagnostic(ctx context.Context, binding diagnostics.Binding, _ string, probe func(context.Context) (string, error)) diagnostics.Report {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, TotalTimeout)
	defer cancel()
	runner := diagnostics.Runner{DirectDial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("DIRECT_DIAGNOSTIC_DISABLED")
	}}
	report := runner.Run(ctx, diagnostics.Request{Binding: &binding})
	result := diagnostics.Result{ID: "proxy_exit", Status: diagnostics.Skipped, Code: "no_active_binding"}
	if binding.Connected && binding.Generation > 0 && binding.ResourceID != "" && len(binding.ResourceID) <= 256 && probe != nil && ctx.Err() == nil {
		address, err := probe(ctx)
		result.Status, result.Code = diagnostics.Failed, "exit_probe_failed"
		if err == nil && ValidObservedAddress(address) {
			result.ObservedIP = address
			result.Status, result.Code = diagnostics.Passed, "observed_exit"
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	for i := range report.Results {
		if report.Results[i].ID == result.ID {
			report.Results[i] = result
		}
	}
	report.DurationMS = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		return CancelDiagnostic(report)
	}
	return report
}

// CancelDiagnostic removes observations whose captured session is no longer
// current, including a late dial that completed after cancellation.
func CancelDiagnostic(report diagnostics.Report) diagnostics.Report {
	report.Cancelled = true
	for i := range report.Results {
		report.Results[i].Status = diagnostics.Skipped
		report.Results[i].Code = "cancelled"
		report.Results[i].ObservedIP = ""
	}
	return report
}
