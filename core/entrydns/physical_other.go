//go:build !windows || (!amd64 && !arm64)

package entrydns

import (
	"context"
	"net"
	"net/netip"
)

func SelectPhysicalRoute(context.Context, netip.Addr) (PhysicalRoute, error) {
	return PhysicalRoute{}, errPhysicalRoute
}
func VerifyPhysicalRoute(context.Context, PhysicalRoute) error { return errPhysicalRoute }
func DialPhysical(context.Context, string, netip.AddrPort, PhysicalRoute) (net.Conn, error) {
	return nil, errPhysicalRoute
}
