package mactun

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/tunengine"
	"github.com/miekg/dns"
)

func appendPermit(list []Permit, p Permit) []Permit {
	for _, v := range list {
		if v == p {
			return list
		}
	}
	return append(list, p)
}
func (t *Tunnel) ensureUnderlayRouteLocked(ctx context.Context, ip netip.Addr) error {
	prefix := netip.PrefixFrom(ip, 32)
	for _, r := range t.m.j.Routes {
		if r.Prefix == prefix {
			return t.m.verifyRoute(ctx, r)
		}
	}
	// A pre-existing exact host route remains foreign and is never claimed.
	current, err := t.m.b.ReadRoute(ctx, prefix)
	if err != nil {
		return err
	}
	if current != nil {
		if current.Interface != t.underlay.Interface || current.Gateway != t.underlay.Gateway {
			return ErrExternalWriter
		}
		return nil
	}
	return t.m.addRoute(ctx, Route{Prefix: prefix, Interface: t.underlay.Interface, Gateway: t.underlay.Gateway})
}
func (t *Tunnel) authorizeDNSLocked(ctx context.Context, p entrydns.Policy, auto []netip.AddrPort) error {
	candidates, err := p.Effective(auto)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		endpoint := c.Endpoint()
		if !validEndpoint(endpoint) {
			continue
		}
		// IP_BOUND_IF resolver sockets need no system-wide DNS pin.
		permit := Permit{Endpoint: endpoint, Protocol: c.Transport}
		t.basePermits = appendPermit(t.basePermits, permit)
		t.dnsPermits[permit] = true
	}
	t.guard.Permits = append([]Permit(nil), t.basePermits...)
	if s := t.current.Load(); s != nil && s.allowed {
		for p := range s.permits {
			t.guard.Permits = appendPermit(t.guard.Permits, p)
		}
	}
	return t.m.guard(ctx, t.guard)
}
func (t *Tunnel) network(ctx context.Context) (tunengine.NetworkSnapshot, error) {
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	n, err := t.snapshotLocked(ctx)
	if err != nil {
		return n, err
	}
	if t.networkGeneration != 0 && n.Generation != t.networkGeneration {
		return n, errors.New("NETWORK_CHANGED")
	}
	return n, nil
}
func (t *Tunnel) snapshotLocked(ctx context.Context) (tunengine.NetworkSnapshot, error) {
	overrides, err := t.m.dnsOverrides(ctx)
	if err != nil {
		return tunengine.NetworkSnapshot{}, err
	}
	if t.m.j.Active {
		if err = t.m.verifyGuard(ctx); err != nil {
			return tunengine.NetworkSnapshot{}, err
		}
	}
	var pins []Route
	for _, r := range t.m.j.Routes {
		if r.Interface != adapterName {
			if err = t.m.verifyRoute(ctx, r); err != nil {
				return tunengine.NetworkSnapshot{}, err
			}
			pins = append(pins, r)
		}
	}
	return t.m.b.Network(ctx, overrides, pins)
}
func (t *Tunnel) checkNetworkLocked(ctx context.Context) error {
	n, err := t.snapshotLocked(ctx)
	if err != nil {
		return err
	}
	u, err := t.m.b.Underlay(ctx)
	if err != nil {
		return err
	}
	if n.Generation != t.networkGeneration || u != t.underlay {
		return errors.New("NETWORK_CHANGED")
	}
	return nil
}
func (t *Tunnel) refreshGenerationLocked(ctx context.Context, initial bool) error {
	n, err := t.snapshotLocked(ctx)
	if err != nil {
		return err
	}
	u, err := t.m.b.Underlay(ctx)
	if err != nil {
		return err
	}
	if u != t.underlay {
		return errors.New("NETWORK_CHANGED")
	}
	if n.Generation == 0 {
		return errors.New("NETWORK_GENERATION_REQUIRED")
	}
	if !initial && t.networkGeneration != 0 && n.Generation != t.networkGeneration {
		return errors.New("NETWORK_CHANGED")
	}
	t.networkGeneration = n.Generation
	return nil
}
func (t *Tunnel) verifyLocked(ctx context.Context) error {
	if t.guard.Forwarding {
		if err := t.m.b.VerifyAdapter(ctx, t.guard.TunnelIndex); err != nil {
			return err
		}
	}
	if err := t.checkNetworkLocked(ctx); err != nil {
		return err
	}
	for _, r := range t.m.j.Routes {
		if err := t.m.verifyRoute(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
func (t *Tunnel) watch(s *session) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
		case <-ticker.C:
		}
		if t.current.Load() != s {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		t.policyMu.Lock()
		err := t.verifyLocked(ctx)
		t.policyMu.Unlock()
		cancel()
		if err == nil && s.ctx.Err() == nil {
			continue
		}
		s.cancel()
		t.networkFault.Store(true)
		t.mu.Lock()
		if t.current.Load() == s {
			t.cleanupErr = errors.Join(err, t.pauseLocked())
		}
		t.mu.Unlock()
		return
	}
}

func (t *Tunnel) permit(s *session, network string, e netip.AddrPort, add, direct bool) error {
	if network != "tcp" && network != "udp" || !publicEndpoint(e) {
		return errors.New("INVALID_SCOPED_PERMIT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	if t.current.Load() != s || !s.allowed || s.ctx.Err() != nil {
		if !add {
			return nil
		}
		return errors.New("STALE_TUNNEL_SESSION")
	}
	if direct && !s.split || !direct && !s.relay {
		return errors.New("PERMIT_POLICY_REJECTED")
	}
	p := Permit{Endpoint: e, Protocol: network}
	count := s.permits[p]
	if add && count > 0 {
		s.permits[p]++
		return nil
	}
	if !add && count > 1 {
		s.permits[p]--
		return nil
	}
	if !add && count == 0 {
		return nil
	}
	if add && len(s.permits)+len(t.basePermits) >= maxPermits {
		return errors.New("PERMIT_LIMIT")
	}
	if err := t.checkNetworkLocked(ctx); err != nil {
		s.cancel()
		return err
	}
	if add {
		s.permits[p] = 1
	} else {
		delete(s.permits, p)
	}
	t.guard.Permits = append([]Permit(nil), t.basePermits...)
	for permit := range s.permits {
		t.guard.Permits = appendPermit(t.guard.Permits, permit)
	}
	if err := t.m.guard(ctx, t.guard); err != nil {
		s.allowed = false
		s.cancel()
		t.guard.Forwarding = false
		t.guard.TunnelIndex = 0
		t.guard.Permits = append([]Permit(nil), t.basePermits...)
		return errors.Join(err, t.m.guard(ctx, t.guard))
	}
	return nil
}

func (t *Tunnel) ResolveControlHost(ctx context.Context, host string) (addresses []netip.Addr, resultErr error) {
	if !validControlHost(host) {
		return nil, errors.New("CONTROL_HOST_REJECTED")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fault := false
	defer func() {
		if resultErr != nil && fault {
			t.networkFault.Store(true)
			resultErr = errors.Join(resultErr, t.pauseLocked())
		}
	}()
	if t.closed {
		return nil, errors.New("ENGINE_CLOSED")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	op := &operationCancel{cancel: cancel}
	t.operation.Store(op)
	defer t.operation.CompareAndSwap(op, nil)
	t.policyMu.Lock()
	if !t.m.j.Active {
		t.policyMu.Unlock()
		return nil, errors.New("PROTECTION_NOT_ACTIVE")
	}
	if !t.forwarding {
		// DHCP DNS and route generations can change without changing the NIC,
		// source or gateway. A stale resolver proof is a reason to rebuild while
		// paused, not permission to mask the new network with old DNS forever.
		u, e := t.m.b.Underlay(ctx)
		rebuild := u != t.underlay
		if e == nil && !rebuild {
			n, snapshotErr := t.snapshotLocked(ctx)
			rebuild = snapshotErr != nil || t.networkGeneration != 0 && n.Generation != t.networkGeneration
		}
		if e == nil && rebuild {
			if t.prepared || t.tun != nil {
				e = errors.New("NETWORK_CHANGED")
			} else {
				e = t.m.blockFast(ctx, t.guard)
			}
			if e == nil {
				e = t.m.restore(ctx)
			}
			if e == nil {
				u, e = t.m.b.Underlay(ctx)
			}
			var current tunengine.NetworkSnapshot
			if e == nil {
				current, e = t.m.b.Network(ctx, nil, nil)
			}
			if e == nil {
				t.underlay = u
				t.networkGeneration = 0
				t.guard = Guard{OwnerUID: t.m.j.OwnerUID, Interface: u.Interface}
				if t.allowLAN {
					for _, r := range current.Routes {
						if interfacePattern.MatchString(r.Interface) {
							t.guard.LAN = append(t.guard.LAN, r)
						}
					}
					if interfacePattern.MatchString(current.MDNSInterface) {
						t.guard.MDNSInterface = current.MDNSInterface
					}
				}
				t.basePermits = nil
				t.dnsPermits = map[Permit]bool{}
				t.controls = map[netip.Addr]bool{}
				e = t.m.guard(ctx, t.guard)
			}
		}
		if e != nil {
			t.policyMu.Unlock()
			fault = true
			return nil, e
		}
	}
	n, err := t.snapshotLocked(ctx)
	if err == nil && t.forwarding {
		err = t.checkNetworkLocked(ctx)
	}
	if err == nil {
		err = t.authorizeDNSLocked(ctx, entrydns.Policy{}, n.EntryDNS)
	}
	if err == nil {
		err = t.refreshGenerationLocked(ctx, false)
	}
	u := t.underlay
	t.policyMu.Unlock()
	if err != nil {
		fault = true
		return nil, err
	}
	ip, err := t.resolve(ctx, host, entrydns.Policy{}, n.EntryDNS, u)
	if err != nil || !gate.PublicIPv4(ip.String()) {
		return nil, errors.New("CONTROL_DNS_FAILED")
	}
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	if len(t.controls) >= 32 && !t.controls[ip] {
		return nil, errors.New("CONTROL_HOST_LIMIT")
	}
	if err = t.checkNetworkLocked(ctx); err != nil {
		fault = true
		return nil, err
	}
	if err = t.ensureUnderlayRouteLocked(ctx, ip); err != nil {
		fault = true
		return nil, err
	}
	t.basePermits = appendPermit(t.basePermits, Permit{Endpoint: netip.AddrPortFrom(ip, 443), Protocol: "tcp", UID: t.m.j.OwnerUID})
	t.guard.Permits = appendPermit(t.guard.Permits, Permit{Endpoint: netip.AddrPortFrom(ip, 443), Protocol: "tcp", UID: t.m.j.OwnerUID})
	if err = t.m.guard(ctx, t.guard); err != nil {
		fault = true
		return nil, err
	}
	if err = t.refreshGenerationLocked(ctx, false); err != nil {
		fault = true
		return nil, err
	}
	t.controls[ip] = true
	return []netip.Addr{ip}, nil
}

func (t *Tunnel) resolve(ctx context.Context, host string, p entrydns.Policy, auto []netip.AddrPort, u Underlay) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if gate.PublicIPv4(ip.String()) {
			return ip, nil
		}
		return netip.Addr{}, errors.New("ENTRY_DNS_FAILED")
	}
	if !validControlHost(host) {
		return netip.Addr{}, errors.New("INVALID_ENTRY_HOST")
	}
	candidates, err := p.Effective(auto)
	if err != nil {
		return netip.Addr{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for i, c := range candidates {
		if !validEndpoint(c.Endpoint()) {
			continue
		}
		deadline, _ := ctx.Deadline()
		attempt, done := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(candidates)-i))
		conn, e := t.m.b.Dial(attempt, c.Transport, c.Endpoint(), u)
		if e == nil {
			stop := context.AfterFunc(attempt, func() { _ = conn.Close() })
			q := new(dns.Msg)
			q.SetQuestion(dns.Fqdn(host), dns.TypeA)
			answer, _, exchangeErr := (&dns.Client{Net: c.Transport, Timeout: time.Second}).ExchangeWithConnContext(attempt, q, &dns.Conn{Conn: conn})
			_ = conn.Close()
			stop()
			if exchangeErr == nil && answer != nil && answer.Rcode == dns.RcodeSuccess && !answer.Truncated {
				for _, rr := range answer.Answer {
					if a, ok := rr.(*dns.A); ok {
						ip, valid := netip.AddrFromSlice(a.A)
						ip = ip.Unmap()
						if valid && gate.PublicIPv4(ip.String()) {
							done()
							return ip, nil
						}
					}
				}
			}
		}
		done()
		if ctx.Err() != nil {
			break
		}
	}
	return netip.Addr{}, errors.New("ENTRY_DNS_FAILED")
}
