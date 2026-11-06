package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// physicalBinding is supplied only by the verified Windows physical-route API.
// The exact destination/socket binding stays in its closures, never in UI input.
type physicalBinding struct {
	adapterGUID, key string
	verify           func(context.Context) error
	dial             func(context.Context) (net.Conn, error)
}
type routePolicy struct {
	mu                             sync.Mutex
	hosts                          []string
	resolve                        func(context.Context, string) ([]netip.Addr, error)
	selectRoute                    func(context.Context, netip.Addr) (physicalBinding, error)
	readCost                       func(context.Context, string) (connectionCost, error)
	prepared                       *physicalBinding
	host, profile                  string
	downloading, consent, poisoned bool
	cost                           update.NetworkCost
	sampleAt                       time.Time
}

func newRoutePolicy(hosts []string, resolve func(context.Context, string) ([]netip.Addr, error), selectRoute func(context.Context, netip.Addr) (physicalBinding, error), readCost func(context.Context, string) (connectionCost, error)) *routePolicy {
	return &routePolicy{hosts: append([]string(nil), hosts...), resolve: resolve, selectRoute: selectRoute, readCost: readCost}
}
func (p *routePolicy) beginDownload(consent bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloading, p.consent, p.poisoned = true, consent, false
	p.prepared = nil
	p.host = ""
	p.profile = ""
	p.sampleAt = time.Time{}
	p.cost = update.NetworkUnknown
}
func (p *routePolicy) allowed(host string) bool {
	for _, h := range p.hosts {
		if h == host {
			return true
		}
	}
	return false
}
func (p *routePolicy) prepare(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !p.allowed(u.Host) || p.resolve == nil || p.selectRoute == nil {
		return update.ErrWaitingNetwork
	}
	ips, err := p.resolve(ctx, u.Host)
	if err != nil || len(ips) == 0 || len(ips) > 8 {
		return update.ErrWaitingNetwork
	}
	for _, ip := range ips {
		if !publicIPv4(ip) {
			return update.ErrWaitingNetwork
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.downloading || p.poisoned {
		return update.ErrWaitingNetwork
	}
	if p.prepared != nil && p.prepared.verify(ctx) != nil {
		p.poisoned = true
		return update.ErrWaitingNetwork
	}
	for _, ip := range ips {
		binding, err := p.selectRoute(ctx, ip)
		if err != nil || binding.adapterGUID == "" || binding.key == "" || binding.verify == nil || binding.dial == nil {
			continue
		}
		if binding.verify(ctx) != nil {
			continue
		}
		if p.prepared != nil && (p.prepared.key != binding.key || p.prepared.verify(ctx) != nil) {
			p.poisoned = true
			return update.ErrWaitingNetwork
		}
		p.prepared = &binding
		p.host = u.Host
		p.sampleAt = time.Time{}
		return nil
	}
	return update.ErrWaitingNetwork
}
func (p *routePolicy) checkLocked(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.poisoned || p.prepared == nil {
		return update.ErrWaitingNetwork
	}
	if p.prepared.verify(ctx) != nil {
		p.poisoned = true
		return update.ErrWaitingNetwork
	}
	if time.Since(p.sampleAt) < 250*time.Millisecond {
		return nil
	}
	p.cost = update.NetworkUnknown
	if p.readCost != nil {
		sample, err := p.readCost(ctx, p.prepared.adapterGUID)
		if err == nil && strings.EqualFold(sample.AdapterGUID, p.prepared.adapterGUID) && sample.ConnectionGUID != "" && sample.NetworkGUID != "" {
			profile := sample.ConnectionGUID + "/" + sample.NetworkGUID
			if p.profile != "" && !strings.EqualFold(p.profile, profile) {
				p.poisoned = true
				return update.ErrWaitingNetwork
			}
			p.profile = profile
			p.cost = classifyNLMCost(sample.Flags)
		}
	}
	p.sampleAt = time.Now()
	return nil
}
func (p *routePolicy) routeValid(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.checkLocked(ctx)
}
func (p *routePolicy) network(ctx context.Context) (update.NetworkCost, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.poisoned {
		return update.NetworkUnknown, update.ErrWaitingNetwork
	}
	if p.sampleAt.IsZero() || time.Since(p.sampleAt) >= 250*time.Millisecond {
		if err := p.checkLocked(ctx); err != nil {
			return update.NetworkUnknown, err
		}
	}
	return p.cost, nil
}
func (p *routePolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !p.allowed(host) || port != "443" || network != "tcp" && network != "tcp4" {
		return nil, errControlDNS
	}
	p.mu.Lock()
	if !p.downloading {
		p.mu.Unlock()
		// Manifest checks are small control-plane transfers, not package consent.
		// They still use only a verified/bound physical socket, never net.Dial fallback.
		return controlDialer(p.hosts, p.resolve, func(ctx context.Context, _ string, numeric string) (net.Conn, error) {
			endpoint, err := netip.ParseAddrPort(numeric)
			if err != nil || p.selectRoute == nil {
				return nil, errControlDNS
			}
			binding, err := p.selectRoute(ctx, endpoint.Addr())
			if err != nil || binding.verify == nil || binding.dial == nil || binding.verify(ctx) != nil {
				return nil, errControlDNS
			}
			return binding.dial(ctx)
		})(ctx, network, address)
	}
	if p.host != host || p.checkLocked(ctx) != nil || !p.consent && p.cost != update.NetworkUnmetered {
		p.mu.Unlock()
		return nil, update.ErrWaitingNetwork
	}
	binding := p.prepared
	p.mu.Unlock()
	conn, err := binding.dial(ctx)
	if err != nil {
		p.mu.Lock()
		policyErr := p.checkLocked(ctx)
		if !p.consent && p.cost != update.NetworkUnmetered {
			policyErr = update.ErrWaitingNetwork
		}
		p.mu.Unlock()
		if policyErr != nil {
			return nil, update.ErrWaitingNetwork
		}
		return nil, err
	}
	p.mu.Lock()
	err = p.checkLocked(ctx)
	if err == nil && !p.consent && p.cost != update.NetworkUnmetered {
		err = update.ErrWaitingNetwork
	}
	p.mu.Unlock()
	if err != nil {
		conn.Close()
		return nil, update.ErrWaitingNetwork
	}
	return conn, nil
}
