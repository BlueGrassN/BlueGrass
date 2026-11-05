package protocolconfig

import (
	"context"
	"net"
)

// PhysicalDialer is a trusted, per-flow host boundary used for authenticated
// SOCKS UDP relay permissions. It cannot be supplied by resource JSON or IPC.
// The Xray instance still owns negotiation, framing and forwarding.
type PhysicalDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type physicalDialerKey struct{}

func WithPhysicalDialer(ctx context.Context, dialer PhysicalDialer) context.Context {
	return context.WithValue(ctx, physicalDialerKey{}, dialer)
}
