package winprotect

import (
	"errors"
	"net/netip"
)

// ArmLocalDNS prepares only the trusted local resolver runtime after confirmed
// cleanup. It preserves the owner/full logical session without arming business.
// The service derives these TCP53 endpoints from the same validated physical
// snapshot passed to LocalOnly; no endpoint or interface comes from IPC.
func (m *Manager) ArmLocalDNS(sid string, dns []DNSEndpoint, allowLAN bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !validSID(sid) || !m.state.ProtectIntent || m.state.OwnerSID != sid {
		return ErrOwner
	}
	if m.state.LogicalSessionID == "" || m.state.CleanupRequired || !m.rebindReady || m.armed {
		return ErrRecoveryRequired
	}
	if err := validateDNS(dns, m.policy.Virtual); err != nil {
		return err
	}
	for _, d := range dns {
		if d.Transport != "tcp" || d.Endpoint.Port() != 53 || !allowLAN {
			return errors.New("local DNS requires LAN and exact TCP53")
		}
	}
	links, err := m.network()
	if err != nil {
		return err
	}
	p := m.policy
	p.Entry, p.EntryPolicy = netip.AddrPort{}, EntryPolicy{}
	p.DNS, p.ControlDNS = append([]DNSEndpoint(nil), dns...), nil
	p.AllowLAN, p.Links = allowLAN, links
	p.Tunnel, p.LocalTunnel, p.UDPRelays, p.LocalOnly = 0, 0, nil, true
	p.Direct = nil
	next := m.state
	next.Phase, next.Recovery = "blocked", false
	if err := m.saveState(next); err != nil {
		return err
	}
	m.localPrepared, m.armed, m.dnsBootstrap = false, false, false
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.state.Recovery = true
		return err
	}
	m.policy, m.active, m.localPrepared = p, true, true
	m.directRefs = nil
	return nil
}

// BeginLocalRuntime commits cleanup intent before construction, including a
// constructor that fails after partially acquiring the deterministic adapter.
func (m *Manager) BeginLocalRuntime(sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.state.ProtectIntent || !validSID(sid) || m.state.OwnerSID != sid {
		return ErrOwner
	}
	if !m.active || !m.localPrepared || m.armed || m.state.Recovery || m.state.CleanupRequired {
		return ErrRecoveryRequired
	}
	next := m.state
	next.Phase, next.CleanupRequired = "local", true
	if err := m.saveState(next); err != nil {
		m.localPrepared = false
		m.state.Recovery = true
		return err
	}
	m.runtimeOwned = true
	return nil
}

// AllowLocalTunnel grants DNS ingress only (plus already selected LAN/non53),
// never business AllowTunnel or relay authority. The LUID is ephemeral and must
// be verified against the live product adapter in this service lifetime.
func (m *Manager) AllowLocalTunnel(sid string, luid uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !validSID(sid) || m.state.OwnerSID != sid {
		return ErrOwner
	}
	if !m.active || !m.localPrepared || !m.runtimeOwned || !m.state.CleanupRequired || m.state.Phase != "local" || m.state.Recovery || m.armed || luid == 0 || m.policy.Tunnel != 0 {
		return ErrRecoveryRequired
	}
	if m.policy.LocalTunnel != 0 && m.policy.LocalTunnel != luid {
		return ErrNotBlocked
	}
	if err := m.verifyTunnel(luid); err != nil {
		return err
	}
	p := m.policy
	p.LocalTunnel = luid
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.state.Recovery = true
		m.localPrepared = false
		return err
	}
	m.policy = p
	return nil
}
