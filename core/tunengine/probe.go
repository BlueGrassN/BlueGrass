package tunengine

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func (r *xrayRuntime) Probe(ctx context.Context) (string, error) {
	return exitprobe.Probe(ctx, probeDialer(r.probeLookup, r.selected), exitprobe.Options{AllowCountry: true})
}
func probeDialer(lookup func(context.Context, string) ([]netip.Addr, error), selected N.Dialer) exitprobe.DialFunc {
	// Resolve only the shared fixed allowlist through TCP DNS over the SAME
	// selected proxy. CONNECT receives literal IPv4; shared HTTPS keeps the host
	// name/SNI and certificate verification. No system resolver fallback.
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, ok := exitprobe.AllowedHost(network, address)
		if !ok {
			return nil, errors.New("INVALID_PROBE_TARGET")
		}
		if ctx.Err() != nil {
			return nil, errors.New("PROBE_CANCELED")
		}
		addresses, err := lookup(ctx, host)
		if err != nil {
			return nil, errors.New("PROBE_DNS_FAILED")
		}
		for _, address := range addresses {
			if ctx.Err() != nil {
				break
			}
			if !gate.PublicIPv4(address.String()) {
				continue
			}
			conn, err := selected.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("PROBE_CONNECT_FAILED")
	}
}
