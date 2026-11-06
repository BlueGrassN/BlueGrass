package winprotect

import (
	"errors"
	"fmt"
	"net/netip"

	"bluegrass/native/core/gate"
)

const maxDirectEndpoints = 256

type directEndpoint struct {
	Network  string
	Endpoint netip.AddrPort
}

func validDirectEndpoint(d directEndpoint, virtual []netip.Prefix) bool {
	return (d.Network == "tcp" || d.Network == "udp") && validEndpoint(d.Endpoint) &&
		gate.PublicIPv4(d.Endpoint.Addr().String()) && !isVirtual(d.Endpoint.Addr(), virtual)
}

// AllowDirect grants this service exact outbound TCP or UDP access for the
// current verified TUN session. The trusted service must call this only for its
// current bypass_cn session after domain/IP classification, never with raw IPC
// endpoints. No names are resolved here. Grants are nonpersistent and bounded
// to 256 active network/endpoint pairs, independent of SOCKS UDP relay grants.
// Each successful call owns a reference to release after its socket closes,
// including when socket construction fails.
func (m *Manager) AllowDirect(network string, endpoint netip.AddrPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.active || !m.armed || m.state.Recovery || m.policy.Tunnel == 0 || m.policy.LocalOnly {
		return ErrRecoveryRequired
	}
	d := directEndpoint{network, endpoint}
	if !validDirectEndpoint(d, m.policy.Virtual) {
		return errors.New("direct egress requires tcp or udp and an exact public IPv4 endpoint")
	}
	if m.policy.Service == "" {
		return errors.New("direct egress requires a trusted service executable")
	}
	if err := m.verifyTunnel(m.policy.Tunnel); err != nil {
		return err
	}
	if references := m.directRefs[d]; references != 0 {
		if references == ^uint64(0) {
			return errors.New("too many direct egress references")
		}
		m.directRefs[d] = references + 1
		return nil
	}
	if len(m.policy.Direct) >= maxDirectEndpoints {
		return errors.New("too many direct endpoints for this session")
	}
	p := m.policy
	p.Direct = append(append([]directEndpoint(nil), p.Direct...), d)
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.armed = false
		m.rebindReady = false
		m.state.Recovery = true
		return fmt.Errorf("allow direct egress: %w", err)
	}
	m.policy = p
	if m.directRefs == nil {
		m.directRefs = make(map[directEndpoint]uint64)
	}
	m.directRefs[d] = 1
	return nil
}

// RemoveDirect releases one successful AllowDirect reference. The trusted
// service must bind both callbacks to the same session and serialize them with
// Block/rearm, so a late close cannot revoke a new session's shared endpoint.
// Cleanup does not require a live TUN or fresh authorization. Failed final
// removal retains the reference and recovery state; the caller must surface
// the error and Block rather than claim that permission has been revoked.
func (m *Manager) RemoveDirect(network string, endpoint netip.AddrPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	d := directEndpoint{network, endpoint}
	if !validDirectEndpoint(d, m.policy.Virtual) {
		return errors.New("direct egress requires tcp or udp and an exact public IPv4 endpoint")
	}
	references := m.directRefs[d]
	if references == 0 {
		return nil
	}
	if references > 1 {
		m.directRefs[d] = references - 1
		return nil
	}
	p := m.policy
	p.Direct = make([]directEndpoint, 0, len(m.policy.Direct)-1)
	for _, grant := range m.policy.Direct {
		if grant != d {
			p.Direct = append(p.Direct, grant)
		}
	}
	if err := m.fw.Replace(compilePolicy(p)); err != nil {
		m.armed = false
		m.rebindReady = false
		m.state.Recovery = true
		return fmt.Errorf("remove direct egress: %w", err)
	}
	m.policy = p
	delete(m.directRefs, d)
	return nil
}
