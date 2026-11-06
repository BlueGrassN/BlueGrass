package cmdupdate

import (
	"context"
	"net"
	"net/netip"
)

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
}

func publicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
func controlDialer(hosts []string, resolve func(context.Context, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || network != "tcp" && network != "tcp4" {
			return nil, errControlDNS
		}
		allowed := false
		for _, h := range hosts {
			if h == host {
				allowed = true
			}
		}
		if !allowed || resolve == nil || dial == nil {
			return nil, errControlDNS
		}
		ips, err := resolve(ctx, host)
		if err != nil || len(ips) == 0 || len(ips) > 8 {
			return nil, errControlDNS
		}
		for _, ip := range ips {
			if !publicIPv4(ip) {
				return nil, errControlDNS
			}
		}
		for _, ip := range ips {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			conn, err := dial(ctx, "tcp4", net.JoinHostPort(ip.String(), "443"))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errControlDNS
	}
}
