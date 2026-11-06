package tunengine

import (
	"context"
	"errors"
	N "github.com/sagernet/sing/common/network"
	tun "github.com/xtls/xray-core/proxy/tun"
	"sync"
)

type localOnlyRuntime interface {
	Start() error
	EnableTunnel() error
	InterfaceLUID() uint64
	Close() error
}
type localOnlyRuntimeFactory func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error)
type localOnlyNative struct {
	ctx             context.Context
	cancel          context.CancelFunc
	p               localOnlyPrepared
	gate            *sessionGate
	platform        *nativePlatform
	router          *nativeRouter
	dns             dnsTransport
	mu              sync.Mutex
	started, closed bool
	device          tun.Tun
	stack           tun.Stack
	once            sync.Once
	err             error
}

func newLocalOnlyRuntime(ctx context.Context, p localOnlyPrepared, g *sessionGate, current func(context.Context) bool) (localOnlyRuntime, error) {
	c, e := decodeLocalOnlyOptions(p, g, current)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	d, e := newNativeLocalOnlyDNS(ctx, p, current)
	if e != nil {
		cancel()
		return nil, e
	}
	r := &localOnlyNative{ctx: ctx, cancel: cancel, p: p, gate: g, platform: &nativePlatform{}, dns: d}
	directs := map[string]N.Dialer{}
	for _, o := range c.Outbounds {
		directs[o.Tag] = &scopedDirect{interfaceName: o.BindInterface, loopback: o.Inet4BindAddress != ""}
	}
	r.router = &nativeRouter{ctx: ctx, gate: g, config: c, dns: d, directs: directs, current: current}
	return r, nil
}
func (r *localOnlyNative) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return errGateClosed
	}
	if r.started {
		return nil
	}
	if e := r.dns.Start(); e != nil {
		return e
	}
	r.started = true
	return nil
}
func (r *localOnlyNative) EnableTunnel() error {
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
func (r *localOnlyNative) InterfaceLUID() uint64 { return r.platform.luid.Load() }
func (r *localOnlyNative) Close() error {
	r.once.Do(func() {
		r.gate.retire()
		r.cancel()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.closed = true
		if r.stack != nil {
			r.err = errors.Join(r.err, r.stack.Close())
		}
		if r.device != nil {
			r.err = errors.Join(r.err, r.device.Close())
		}
		r.err = errors.Join(r.err, r.dns.Close())
		if r.err == nil {
			r.platform.luid.Store(0)
		}
	})
	return r.err
}
