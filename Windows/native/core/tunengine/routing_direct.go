package tunengine

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"bluegrass/native/core/gate"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Only the trusted router can choose this dialer. The host grants each exact
// socket before dialing, and the socket owns one revocable permission reference.
type authorizedDirect struct {
	base           N.Dialer
	session        context.Context
	gate           *sessionGate
	allow, release func(string, netip.AddrPort) error
}

func (d *authorizedDirect) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if ctx.Err() != nil || d.session.Err() != nil || !d.gate.isOpen() {
		return nil, errGateClosed
	}
	if (network != "tcp" && network != "udp") || destination.Fqdn != "" || !gate.PublicIPv4(destination.Addr.String()) || destination.Port == 0 || d.allow == nil || d.release == nil {
		return nil, errors.New("DIRECT_ROUTE_DENIED")
	}
	endpoint := destination.AddrPort()
	if err := d.allow(network, endpoint); err != nil {
		return nil, errors.New("DIRECT_ROUTE_DENIED")
	}
	owned := true
	defer func() {
		if owned {
			_ = d.release(network, endpoint)
		}
	}()
	if ctx.Err() != nil || d.session.Err() != nil || !d.gate.isOpen() {
		return nil, errGateClosed
	}
	dialCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.session, cancel)
	conn, err := d.base.DialContext(dialCtx, network, destination)
	stop()
	cancel()
	if err != nil {
		return nil, errors.New("DIRECT_CONNECT_FAILED")
	}
	wrapped := &relaySocket{Conn: conn, gate: d.gate, release: func() error { return d.release(network, endpoint) }, stop: func() bool { return false }, stopCaller: func() bool { return false }}
	owned = false
	// Close by the owner gate is synchronous. Context cancellation closes the raw
	// socket; the routing flow's defer closes the wrapper and releases its permit.
	wrapped.stop = context.AfterFunc(d.session, func() { _ = conn.Close() })
	wrapped.stopCaller = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if !d.gate.track(wrapped) || ctx.Err() != nil || d.session.Err() != nil {
		_ = wrapped.Close()
		return nil, errGateClosed
	}
	return &directConn{wrapped}, nil
}

type directConn struct{ *relaySocket }

func (c *directConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return errors.New("DIRECT_HALF_CLOSE_UNSUPPORTED")
}

func (d *authorizedDirect) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	conn, err := d.DialContext(ctx, "udp", destination)
	if err != nil {
		return nil, err
	}
	return &connectedPacket{Conn: conn, remote: destination.UDPAddr()}, nil
}
