//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
)

type ownedLocalTunnel interface {
	Start(context.Context) error
	TunnelLUID() uint64
	Close() error
}

// localSnapshot filters only endpoints the WFP contract can represent. The
// independent runtime validates routes, domains, interfaces and ambiguous scope
// before it opens anything. Entry/public resolvers are never local fallbacks.
func localSnapshot(n tunengine.NetworkSnapshot, allowLAN bool) (tunengine.NetworkSnapshot, []winprotect.DNSEndpoint) {
	n.EntryDNS = nil
	input := n.DNS
	n.DNS = nil
	var permissions []winprotect.DNSEndpoint
	if !allowLAN {
		n.MDNSInterface = ""
		return n, nil
	}
	for _, d := range input {
		if d.Server.Port() != 53 || len(usableDNS([]netip.AddrPort{d.Server})) != 1 {
			continue
		}
		d.Domains = append([]string(nil), d.Domains...)
		n.DNS = append(n.DNS, d)
		permissions = append(permissions, winprotect.DNSEndpoint{Endpoint: d.Server, Transport: "tcp"})
	}
	return n, uniqueDNSPermissions(permissions)
}

// pause runs under mu. Its lifetime belongs to the authenticated service owner,
// not to the canceled attempt. Close/Release disable it before exact teardown.
func (e *protectedTunnel) pause() error {
	if e.closed || e.boundLogicalID == "" {
		return e.block()
	}
	e.localDesired = true
	e.startLocalWatcher()
	if e.local != nil && e.cleanupErr == nil && e.local.TunnelLUID() != 0 {
		return nil
	}
	if err := e.block(); err != nil {
		return err
	}
	return e.startLocal()
}

// startLocal requires confirmed cleanup and a fresh blocked WFP policy. Even a
// partial constructor/start failure remains owned until Close confirms cleanup.
func (e *protectedTunnel) startLocal() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := e.network(ctx)
	if err != nil || n.Generation == 0 {
		return errors.New("LOCAL_NETWORK_SNAPSHOT_FAILED")
	}
	n, dns := localSnapshot(n, e.allowLAN)
	if err := e.protection.ArmLocalDNS(e.sid, dns, e.allowLAN); err != nil {
		return errors.New("LOCAL_PROTECTION_FAILED")
	}
	if err := e.protection.BeginLocalRuntime(e.sid); err != nil {
		return errors.New("LOCAL_JOURNAL_FAILED")
	}
	l, err := e.newLocal(tunengine.LocalOnlyConfig{AllowLAN: e.allowLAN, Network: n, CurrentNetwork: e.network, UIExecutable: e.uiExecutable})
	e.local = l
	if err == nil && l == nil {
		err = errors.New("LOCAL_RUNTIME_MISSING")
	}
	if err == nil {
		err = l.Start(ctx)
	}
	if err == nil {
		// Do not install permissions for a different network observed during startup.
		current, snapshotErr := e.network(ctx)
		if snapshotErr != nil || current.Generation != n.Generation {
			err = errors.New("LOCAL_NETWORK_CHANGED")
		}
	}
	if err == nil {
		err = e.protection.AllowLocalTunnel(e.sid, l.TunnelLUID())
	}
	if err != nil {
		return errors.Join(err, e.block())
	}
	e.localGeneration = n.Generation
	return nil
}

// stopLocalWatcher never waits under mu: a pending tick may itself need mu.
// A captured context check under that same lock prevents a retired tick from
// touching a new business/local runtime. The goroutine has no OS ownership.
func (e *protectedTunnel) stopLocalWatcher() {
	e.localDesired = false
	if e.localWatchCancel != nil {
		e.localWatchCancel()
		e.localWatchCancel = nil
	}
}
func (e *protectedTunnel) startLocalWatcher() {
	if e.localWatchCancel != nil || e.localWatchInterval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.localWatchCancel = cancel
	go func() {
		ticker := time.NewTicker(e.localWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.refreshLocal(ctx)
			}
		}
	}()
}
func (e *protectedTunnel) refreshLocal(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() != nil || e.closed || !e.localDesired {
		return
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, time.Second)
	n, err := e.network(snapshotCtx)
	cancel()
	if ctx.Err() != nil {
		return
	}
	if err == nil && e.local != nil && e.cleanupErr == nil && n.Generation == e.localGeneration && e.local.TunnelLUID() != 0 {
		return
	}
	// Revoke the old interface and resolver rules before accepting/rebuilding
	// any new snapshot. Unreadable snapshots leave only blocking protection.
	if e.block() != nil || err != nil {
		return
	}
	_ = e.startLocal() // Failure keeps blocking; the next snapshot tick retries.
}
