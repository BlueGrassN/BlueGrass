//go:build windows

package protocolconfig

import (
	"context"
	"net"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
)

// Register once, before any Xray instances start. This keeps Xray's own socket
// creation, interface binding and concrete TCP/UDP connection types intact.
// It changes no OS proxy, routes, firewall or process-wide network settings.
func init() { internet.UseAlternativeSystemDialer(&lifetimeSystemDialer{}) }

type lifetimeSystemDialer struct{ internet.DefaultSystemDialer }

func (d *lifetimeSystemDialer) Dial(ctx context.Context, source xnet.Address, destination xnet.Destination, options *internet.SocketConfig) (net.Conn, error) {
	var conn net.Conn
	var err error
	guard, guarded := ctx.Value(physicalDialerKey{}).(PhysicalDialer)
	if guarded {
		conn, err = guard.DialContext(ctx, destination.Network.SystemString(), destination.NetAddr())
	} else {
		conn, err = d.DefaultSystemDialer.Dial(ctx, source, destination, options)
	}
	if err != nil {
		return nil, err
	}
	// A successful physical dial can still be waiting in a proxy or TLS
	// handshake. Closing only the dispatch pipes does not interrupt that read.
	// Each flow/session context is canceled on retirement, including failures.
	lifetime := ctx
	if destination.Network == xnet.Network_UDP && !guarded {
		// QUIC can multiplex later flows on the first flow's physical socket.
		// Retire that socket with the outbound session, not its creator stream.
		if session, ok := ctx.Value(outboundLifetimeKey{}).(context.Context); ok {
			lifetime = session
		}
	}
	context.AfterFunc(lifetime, func() { _ = conn.Close() })
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
