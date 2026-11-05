package winprotect

import (
	"net/netip"

	"bluegrass/native/core/engine"
)

// SwitchLogicalSession is a trusted service transition, never an IPC owner
// assertion. The service authenticates the persisted SID before calling this.
// A successful Block AND exact cleanup confirmation must precede it. Owner,
// environment, data source, account and protection intent cannot change.
func (m *Manager) SwitchLogicalSession(oldKey, newKey engine.ResourceKey, newID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.state.ProtectIntent || !validSID(m.state.OwnerSID) || !validResourceKey(oldKey) || !validResourceKey(newKey) || !validID(newID) ||
		m.state.ResourceKey != oldKey || m.state.LogicalSessionID == "" || m.state.LogicalSessionID == newID || oldKey == newKey ||
		oldKey.Environment != newKey.Environment || oldKey.DataSource != newKey.DataSource || oldKey.AccountKey != newKey.AccountKey {
		return ErrOwner
	}
	if !m.active || m.state.CleanupRequired || m.runtimeOwned || m.armed || !m.rebindReady || m.dnsBootstrap || m.localPrepared ||
		m.state.Phase != "blocked" || m.policy.Tunnel != 0 || m.policy.LocalTunnel != 0 {
		return ErrRecoveryRequired
	}
	p := m.policy
	p.Entry, p.EntryPolicy, p.DNS, p.UDPRelays = netip.AddrPort{}, EntryPolicy{}, nil, nil
	p.Direct = nil
	p.LocalOnly, p.LocalTunnel = false, 0
	// Revocation is safe before writing the new binding: if that write fails,
	// the original journal remains authoritative under this narrower block.
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.state.Recovery = true
		m.rebindReady = false
		return err
	}
	m.policy = p
	m.directRefs = nil
	next := m.state
	next.ResourceKey, next.ResourceID, next.LogicalSessionID = newKey, newKey.SourceID, newID
	next.Recovery = false
	if err := m.saveState(next); err != nil {
		m.state.Recovery = true
		m.rebindReady = false
		return err
	}
	return nil
}
