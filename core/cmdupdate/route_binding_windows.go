//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/entrydns"
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/sys/windows"
)

func selectPhysicalBinding(ctx context.Context, destination netip.Addr) (physicalBinding, error) {
	if !publicIPv4(destination) {
		return physicalBinding{}, errControlDNS
	}
	route, err := entrydns.SelectPhysicalRoute(ctx, destination)
	if err != nil {
		return physicalBinding{}, err
	}
	guid, err := windows.GUIDFromString(route.AdapterGUID)
	if err != nil || guid == (windows.GUID{}) || route.Destination != destination || route.InterfaceIndex == 0 || route.InterfaceLUID == 0 {
		return physicalBinding{}, errControlDNS
	}
	// Exclude destination from the key so equal physical routes may serve signed
	// mirrors; a changed interface/source/prefix/gateway/metric pauses the attempt.
	key := fmt.Sprintf("%s/%d/%d/%s/%s/%s/%d", strings.ToLower(guid.String()), route.InterfaceIndex, route.InterfaceLUID, route.LocalAddress, route.Prefix, route.NextHop, route.Metric)
	return physicalBinding{adapterGUID: strings.ToLower(guid.String()), key: key,
		verify: func(ctx context.Context) error { return entrydns.VerifyPhysicalRoute(ctx, route) },
		dial: func(ctx context.Context) (net.Conn, error) {
			return entrydns.DialPhysical(ctx, "tcp", netip.AddrPortFrom(destination, 443), route)
		},
	}, nil
}
