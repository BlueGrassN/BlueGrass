package routingpolicy

import (
	"context"
	"errors"
	"net/netip"

	"bluegrass/native/core/gate"
)

type Lookup func(context.Context, string) ([]netip.Addr, error)

// Route is a single resolved destination. The caller dials this literal address
// so a second DNS lookup cannot change the classification or enter a private net.
type Route struct {
	Address netip.Addr
	Direct  bool
}

func Resolve(ctx context.Context, mode Mode, rules *Rules, host string, domestic, selected Lookup) (Route, error) {
	if ctx.Err() != nil {
		return Route{}, ctx.Err()
	}
	if mode != Global && mode != BypassCN {
		return Route{}, errors.New("INVALID_ROUTING_MODE")
	}
	if mode == BypassCN && rules == nil {
		return Route{}, errors.New("ROUTING_RULES_UNAVAILABLE")
	}
	ip, parseErr := netip.ParseAddr(host)
	if parseErr != nil {
		host = normalizeDomain(host)
		if host == "" {
			return Route{}, errors.New("ROUTING_TARGET_INVALID")
		}
		lookup := selected
		if mode == BypassCN && rules.Domain(host) == Direct {
			lookup = domestic
		}
		if lookup == nil {
			return Route{}, errors.New("ROUTING_DNS_UNAVAILABLE")
		}
		addresses, err := lookup(ctx, host)
		if err != nil || ctx.Err() != nil {
			return Route{}, errors.New("ROUTING_DNS_FAILED")
		}
		for _, candidate := range addresses {
			if gate.PublicIPv4(candidate.String()) {
				ip = candidate
				break
			}
		}
	}
	if !gate.PublicIPv4(ip.String()) {
		return Route{}, errors.New("ROUTING_TARGET_INVALID")
	}
	return Route{Address: ip, Direct: mode == BypassCN && rules.ShouldDirect(host, ip)}, nil
}
