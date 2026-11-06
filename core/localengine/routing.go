package localengine

import (
	"context"
	"net"
	"net/netip"

	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/tunengine"
)

type dialFunc = func(context.Context, string, string) (net.Conn, error)

func businessRouter(mode routingpolicy.Mode, rules *routingpolicy.Rules, selected, direct dialFunc) dialFunc {
	if mode == routingpolicy.Global {
		return selected
	}
	domesticDNS := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return tunengine.LookupIPv4TCP(ctx, direct, routingpolicy.DomesticDNS+":53", host)
	}
	proxyDNS := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return tunengine.LookupIPv4TCP(ctx, selected, tunengine.PublicDNSServer+":53", host)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		route, err := routingpolicy.Resolve(ctx, mode, rules, host, domesticDNS, proxyDNS)
		if err != nil {
			return nil, err
		}
		dial := selected
		if route.Direct {
			dial = direct
		}
		return dial(ctx, network, net.JoinHostPort(route.Address.String(), port))
	}
}
