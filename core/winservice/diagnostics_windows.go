//go:build windows

package winservice

import (
	"context"
	"errors"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/tunengine"
)

type tunnelDiagnoser interface {
	Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)
}

var _ tunnelDiagnoser = (*tunengine.Tun)(nil)

// Diagnose observes the captured, already-authorized runtime only. Nativehost
// authenticates and filters the complete request context; this layer prevents
// a late engine report from touching a replacement TUN or WFP session.
// Observations and detector failures do not change forwarding or protection.
func (e *protectedTunnel) Diagnose(ctx context.Context, binding diagnostics.Binding) (diagnostics.Report, error) {
	if err := ctx.Err(); err != nil {
		return diagnostics.Report{}, err
	}
	if !binding.Connected || binding.Generation == 0 || binding.ResourceID == "" || len(binding.ResourceID) > 256 {
		return diagnostics.Report{}, errors.New("INVALID_DIAGNOSTIC_BINDING")
	}
	e.mu.Lock()
	tun := e.tun
	e.relayMu.Lock()
	session := e.relay
	ready := !e.closed && e.prepared && tun != nil && session != nil && session.allowed && session.ctx.Err() == nil
	e.relayMu.Unlock()
	diagnostic, supported := tun.(tunnelDiagnoser)
	e.mu.Unlock()
	if !ready {
		return diagnostics.Report{}, errors.New("ENGINE_NOT_PREPARED")
	}
	if !supported {
		return diagnostics.Report{}, errors.New("DIAGNOSTICS_UNSUPPORTED")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	// Never hold mu: Pause/Close must be able to cancel the selected runtime.
	report, probeErr := diagnostic.Diagnose(probeCtx, binding)
	if err := probeCtx.Err(); err != nil {
		return diagnostics.Report{}, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.relayMu.Lock()
	current := !e.closed && e.prepared && e.tun == tun && e.relay == session && session.allowed && session.ctx.Err() == nil
	e.relayMu.Unlock()
	if !current {
		return diagnostics.Report{}, errors.New("STALE_DIAGNOSTIC_SESSION")
	}
	if err := probeCtx.Err(); err != nil {
		return diagnostics.Report{}, err
	}
	if report.Cancelled {
		return diagnostics.Report{}, context.Canceled
	}
	if probeErr != nil && len(report.Results) == 0 {
		return diagnostics.Report{}, probeErr
	}
	if report.Version != 1 || report.Generation != binding.Generation {
		return diagnostics.Report{}, errors.New("STALE_DIAGNOSTIC_REPORT")
	}
	return report, probeErr
}
