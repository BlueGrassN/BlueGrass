package tunengine

import (
	"context"
	"errors"
	tun "github.com/xtls/xray-core/proxy/tun"
	"sync/atomic"
)

type nativePlatform struct {
	luid      atomic.Uint64
	open      func(*tun.Config) (tun.Tun, uint64, error)
	makeStack func(context.Context, tun.StackOptions, tun.ConnectionHandler) (tun.Stack, error)
}

func (p *nativePlatform) OpenInterface(options *tun.Config) (tun.Tun, error) {
	// These process-wide helpers would compete with the service's existing
	// firewall, resolver and per-socket physical-interface policy.
	if options == nil || options.AutoOutboundsInterface != "" || options.AutoSystemDnsToGateway || len(options.AutoSystemWfpBlockLeak) != 0 {
		return nil, errors.New("TUN_HOST_POLICY_CONFLICT")
	}
	open := p.open
	if open == nil {
		open = openNativeTun
	}
	device, luid, err := open(options)
	if err != nil {
		return nil, err
	}
	p.luid.Store(luid)
	return device, nil
}

func (p *nativePlatform) newStack(ctx context.Context, options tun.StackOptions, handler tun.ConnectionHandler) (tun.Stack, error) {
	if p.makeStack != nil {
		return p.makeStack(ctx, options, handler)
	}
	return tun.NewStack(ctx, options, handler)
}
