package tunengine

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/miekg/dns"
)

// LookupIPv4TCP keeps DNS on an explicitly chosen transport. It never consults
// Windows DNS or HTTP proxy environment settings as a fallback.
func LookupIPv4TCP(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), server, host string) ([]netip.Addr, error) {
	return lookupIPv4(ctx, &exchangeTransport{exchange: func(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		conn, err := dial(ctx, "tcp", server)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		client := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
		reply, _, err := client.ExchangeWithConnContext(ctx, query, &dns.Conn{Conn: conn})
		return reply, err
	}}, host)
}
