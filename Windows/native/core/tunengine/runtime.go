package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"bluegrass/native/core/routingpolicy"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	tun "github.com/xtls/xray-core/proxy/tun"
)

// Xray owns both the native adapter and TCP/UDP stack. The service retains
// session policy and WFP ownership; physical outbounds are bound explicitly.
type xrayRuntime struct {
	ctx              context.Context
	cancel           context.CancelFunc
	p                preparedConfig
	config           nativeConfig
	gate             *sessionGate
	platform         *nativePlatform
	selected         *xrayOutbound
	domesticPhysical *scopedDirect
	socks            *guardedSOCKS
	router           *nativeRouter
	dns              *policyDNS
	fake             *persistentFakeIPTransport
	probeLookup      func(context.Context, string) ([]netip.Addr, error)
	mu               sync.Mutex
	device           tun.Tun
	stack            tun.Stack
	started, closed  bool
	closeOnce        sync.Once
	closeErr         error
}

func newRuntime(ctx context.Context, p preparedConfig, g *sessionGate) (runtimeEngine, error) {
	var rules *routingpolicy.Rules
	if p.routingMode == routingpolicy.BypassCN {
		var err error
		rules, err = routingpolicy.Load()
		if err != nil {
			return nil, err
		}
		if p.allowDirect == nil || p.releaseDirect == nil {
			return nil, errors.New("DIRECT_PROTECTION_REQUIRED")
		}
	}
	c, e := decodeOptions(p, g)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	selected, e := newXrayOutbound(ctx, p.selected)
	if e != nil {
		cancel()
		return nil, e
	}
	r := &xrayRuntime{ctx: ctx, cancel: cancel, p: p, config: c, gate: g, platform: &nativePlatform{}, selected: selected}
	r.fake = newFakeIPTransport(ctx, p)
	transports := map[string]dnsTransport{"fakeip": r.fake, "public": newLocalOnlyTCP(ctx, selected, M.ParseSocksaddr(PublicDNSServer+":53"))}
	directs := map[string]N.Dialer{}
	for _, o := range c.Outbounds {
		directs[o.Tag] = &scopedDirect{interfaceName: o.BindInterface, loopback: o.Inet4BindAddress != ""}
	}
	for _, s := range c.DNS.Servers {
		if s.Type == "tcp" && s.Tag != "public" {
			transports[s.Tag] = newLocalOnlyTCP(ctx, &scopedDirect{interfaceName: s.BindInterface}, M.ParseSocksaddrHostPort(s.Server, s.ServerPort))
		}
		if s.Type == "mdns" {
			transports[s.Tag] = newMDNSTransport(ctx, s.Interface[0])
		}
	}
	r.dns = &policyDNS{config: c, transports: transports, routingMode: p.routingMode, rules: rules}
	if p.protocol.Type == "socks5" {
		r.socks = newGuardedSOCKS(ctx, p, g, p.protocol.SOCKS5.Username, p.protocol.SOCKS5.Password, &scopedDirect{})
		r.socks.selected = selected
	}
	r.router = &nativeRouter{ctx: ctx, config: c, gate: g, dns: r.dns, fake: r.fake.store, selected: selected, socks: r.socks, directs: directs}
	r.router.routingMode, r.router.rules = p.routingMode, rules
	if p.routingMode == routingpolicy.BypassCN {
		r.domesticPhysical = &scopedDirect{}
		transports["domestic"] = newLocalOnlyTCP(ctx, r.domesticPhysical, M.ParseSocksaddr(routingpolicy.DomesticDNS+":53"))
		r.router.directs["domestic"] = &authorizedDirect{base: r.domesticPhysical, session: ctx, gate: g, allow: p.allowDirect, release: p.releaseDirect}
	}
	r.probeLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return lookupIPv4(ctx, transports["public"], host)
	}
	return r, nil
}
func (r *xrayRuntime) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return errGateClosed
	}
	if r.started {
		return nil
	}
	iface, e := physicalInterface(r.ctx, r.p.entry.Addr())
	if e != nil {
		return e
	}
	if e = r.selected.start(iface); e != nil {
		return e
	}
	if r.socks != nil {
		r.socks.base = &scopedDirect{interfaceName: iface}
	}
	if r.domesticPhysical != nil {
		r.domesticPhysical.interfaceName = iface
	}
	// Control sockets use the same original physical route; never the new TUN default.
	r.router.directs["control"] = &scopedDirect{interfaceName: iface}
	for _, t := range r.dns.transports {
		if e = t.Start(); e != nil {
			return e
		}
	}
	r.started = true
	return nil
}
func nativeOptions(v4, v6 netip.Prefix) *tun.Config {
	// Gateway is assigned to the Windows host. DNS must target the unassigned
	// peer so the on-link route sends queries into Wintun, not local delivery.
	return &tun.Config{Name: InterfaceName, Desc: "Wintun", Gateway: []string{v4.String(), v6.String()}, MTU: 1500,
		DNS: []string{v4.Addr().Next().String()}, AutoSystemRoutingTable: []string{"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"}}
}

// An opener can acquire a resource before its cleanup can be confirmed.
type nativeOpenError struct {
	err    error
	device tun.Tun
	luid   uint64
}

func (e *nativeOpenError) Error() string { return e.err.Error() }
func (e *nativeOpenError) Unwrap() error { return e.err }

func startNative(ctx context.Context, p *nativePlatform, opts *tun.Config, router *nativeRouter, g *sessionGate) (tun.Tun, tun.Stack, error) {
	if err := nativeActivationError(ctx, g); err != nil {
		return nil, nil, err
	}
	device, e := p.OpenInterface(opts)
	if e != nil {
		var pending *nativeOpenError
		if errors.As(e, &pending) {
			p.luid.Store(pending.luid)
			return pending.device, nil, e
		}
		return nil, nil, e
	}
	var stack tun.Stack
	e = nativeActivationError(ctx, g)
	if e == nil {
		stack, e = p.newStack(ctx, tun.StackOptions{Tun: device, MTU: opts.MTU, IdleTimeout: 3 * time.Minute}, &nativeHandler{ctx: ctx, gate: g, router: &guardedRouter{Router: router, gate: g}})
	}
	if e == nil {
		e = nativeActivationError(ctx, g)
	}
	if e == nil {
		e = stack.Start()
	}
	if e == nil {
		e = nativeActivationError(ctx, g)
	}
	if e == nil {
		e = device.Start()
	}
	if e == nil {
		e = nativeActivationError(ctx, g)
	}
	if e != nil {
		var cleanup error
		if stack != nil {
			cleanup = stack.Close()
		}
		cleanup = errors.Join(cleanup, device.Close())
		if cleanup == nil {
			p.luid.Store(0)
			return nil, nil, e
		}
		return device, stack, errors.Join(e, cleanup)
	}
	return device, stack, nil
}

func nativeActivationError(ctx context.Context, g *sessionGate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.isRetired() {
		return errGateClosed
	}
	return nil
}
func (r *xrayRuntime) EnableTunnel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.started || r.ctx.Err() != nil {
		return errGateClosed
	}
	if r.device != nil {
		return nil
	}
	var e error
	r.device, r.stack, e = startNative(r.ctx, r.platform, nativeOptions(r.p.tun4, r.p.tun6), r.router, r.gate)
	return e
}
func (r *xrayRuntime) InterfaceLUID() uint64 { return r.platform.luid.Load() }
func (r *xrayRuntime) Close() error {
	r.closeOnce.Do(func() {
		r.gate.retire()
		r.cancel()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.closed = true
		if r.stack != nil {
			r.closeErr = errors.Join(r.closeErr, r.stack.Close())
		}
		if r.device != nil {
			r.closeErr = errors.Join(r.closeErr, r.device.Close())
		}
		for _, t := range r.dns.transports {
			r.closeErr = errors.Join(r.closeErr, t.Close())
		}
		r.closeErr = errors.Join(r.closeErr, r.selected.Close())
		if r.closeErr == nil {
			r.platform.luid.Store(0)
		}
	})
	return r.closeErr
}
