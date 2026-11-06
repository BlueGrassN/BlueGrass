package bridge

import (
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
	"context"
	"errors"
	"net"
	"net/netip"
)

type publicResolver interface {
	LookupPublic(context.Context, string) ([]netip.Addr, error)
}

func probeDialer(resolver publicResolver, selected streamDialer) exitprobe.DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, ok := exitprobe.AllowedHost(network, address)
		if !ok || ctx.Err() != nil {
			return nil, errors.New("EXIT_PROBE_FAILED")
		}
		ips, err := resolver.LookupPublic(ctx, host)
		if err == nil {
			for _, ip := range ips {
				if ctx.Err() != nil {
					break
				}
				if !gate.PublicIPv4(ip.String()) {
					continue
				}
				conn, err := selected.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
				if err == nil {
					return conn, nil
				}
			}
		}
		return nil, errors.New("EXIT_PROBE_FAILED")
	}
}
