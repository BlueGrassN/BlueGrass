package entrydns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
)

var errPhysicalRoute = errors.New("PHYSICAL_ROUTE_UNAVAILABLE")

// PhysicalRoute is an immutable, destination-specific snapshot. AdapterGUID is
// the interface GUID (not an NLM network/profile GUID). It contains no handles.
type PhysicalRoute struct {
	Destination    netip.Addr
	InterfaceIndex uint32
	InterfaceLUID  uint64
	AdapterGUID    string
	LocalAddress   netip.Addr
	Prefix         netip.Prefix
	NextHop        netip.Addr
	Metric         uint64
}

type physicalCandidate struct {
	Route        PhysicalRoute
	Name         string
	IfType       uint32
	Up, Hardware bool
}

var excludedPhysicalPrefixes = []netip.Prefix{
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("fc00::/18"),
	netip.MustParsePrefix("172.19.0.0/30"), netip.MustParsePrefix("172.31.255.0/30"),
	netip.MustParsePrefix("172.30.255.0/30"), netip.MustParsePrefix("10.255.255.0/30"),
	netip.MustParsePrefix("fdfe:dcba:9876::/126"), netip.MustParsePrefix("fdde:adbe:ef01::/126"),
}

func physicalAddress(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.Is4In6() || ip.Zone() != "" {
		return false
	}
	for _, prefix := range excludedPhysicalPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// IANA/Windows IF_TYPE_ETHERNET_CSMACD, IEEE80211, WWANPP and WWANPP2.
// Type membership is necessary but never sufficient: hardware, connectivity,
// identity, source and route checks apply equally to every admitted type.
func physicalInterfaceType(ifType uint32) bool {
	return ifType == 6 || ifType == 71 || ifType == 243 || ifType == 244
}

// Select only currently usable hardware Ethernet/Wi-Fi/WWAN. An OS best interface
// that points at a TUN cannot override the per-physical-LUID route snapshots.
func selectPhysicalRoute(destination netip.Addr, candidates []physicalCandidate, osBest uint32) (PhysicalRoute, error) {
	if !physicalAddress(destination) {
		return PhysicalRoute{}, errPhysicalRoute
	}
	var eligible []PhysicalRoute
	for _, candidate := range candidates {
		r := candidate.Route
		if !candidate.Up || !candidate.Hardware || !physicalInterfaceType(candidate.IfType) || strings.HasPrefix(strings.ToLower(candidate.Name), "bluegrass") ||
			r.InterfaceIndex == 0 || r.InterfaceLUID == 0 || r.AdapterGUID == "" || r.Destination != destination ||
			!physicalAddress(r.LocalAddress) || r.LocalAddress.Is4() != destination.Is4() ||
			!r.Prefix.IsValid() || !r.Prefix.Contains(destination) || !r.NextHop.IsValid() || r.NextHop.Is4() != destination.Is4() ||
			r.NextHop.IsMulticast() || r.NextHop.IsLoopback() || r.NextHop.Is4In6() || r.NextHop.Zone() != "" {
			continue
		}
		eligible = append(eligible, r)
	}
	if len(eligible) == 0 {
		return PhysicalRoute{}, errPhysicalRoute
	}
	best := eligible[0]
	for _, r := range eligible[1:] {
		if r.Prefix.Bits() > best.Prefix.Bits() || r.Prefix.Bits() == best.Prefix.Bits() && r.Metric < best.Metric {
			best = r
		}
	}
	var tied []PhysicalRoute
	physicalOSBest := false
	for _, r := range eligible {
		if r.InterfaceIndex == osBest {
			physicalOSBest = true
		}
		if r.Prefix.Bits() == best.Prefix.Bits() && r.Metric == best.Metric {
			tied = append(tied, r)
			if r.InterfaceIndex == osBest {
				best = r
			}
		}
	}
	for _, r := range tied {
		if r != best && best.InterfaceIndex != osBest {
			return PhysicalRoute{}, errPhysicalRoute
		}
	}
	if physicalOSBest && best.InterfaceIndex != osBest {
		return PhysicalRoute{}, errPhysicalRoute
	}
	return best, nil
}

func verifyPhysicalRoute(ctx context.Context, route PhysicalRoute, selectRoute func(context.Context, netip.Addr) (PhysicalRoute, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := selectRoute(ctx, route.Destination)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if current != route {
		return errors.New("PHYSICAL_ROUTE_CHANGED")
	}
	return nil
}

func verifyPhysicalConnection(ctx context.Context, conn net.Conn, route PhysicalRoute, verify func(context.Context, PhysicalRoute) error) error {
	if conn == nil {
		return errPhysicalRoute
	}
	local, err := netip.ParseAddrPort(conn.LocalAddr().String())
	if err != nil || local.Addr().Unmap() != route.LocalAddress {
		_ = conn.Close()
		return errors.New("PHYSICAL_SOCKET_SOURCE_MISMATCH")
	}
	if err := verify(ctx, route); err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

// The dialer checks the exact socket family/endpoint and freshly verifies the
// route inside ControlContext, before the socket can connect or send packets.
func boundPhysicalDialer(network string, endpoint netip.AddrPort, route PhysicalRoute,
	verify func(context.Context, PhysicalRoute) error, bind func(syscall.RawConn, PhysicalRoute) error) (*net.Dialer, string, error) {
	if network != "tcp" && network != "udp" || !endpoint.IsValid() || endpoint.Port() == 0 || endpoint.Addr() != route.Destination ||
		!physicalAddress(route.Destination) || !physicalAddress(route.LocalAddress) || route.LocalAddress.Is4() != route.Destination.Is4() || route.InterfaceIndex == 0 || route.InterfaceLUID == 0 {
		return nil, "", errPhysicalRoute
	}
	family := "4"
	if route.Destination.Is6() {
		family = "6"
	}
	exactNetwork := network + family
	dialer := &net.Dialer{}
	if network == "tcp" {
		dialer.LocalAddr = &net.TCPAddr{IP: route.LocalAddress.AsSlice()}
	} else {
		dialer.LocalAddr = &net.UDPAddr{IP: route.LocalAddress.AsSlice()}
	}
	dialer.ControlContext = func(ctx context.Context, actualNetwork, actualAddress string, raw syscall.RawConn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if actualNetwork != exactNetwork || actualAddress != endpoint.String() {
			return errPhysicalRoute
		}
		if err := verify(ctx, route); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return bind(raw, route)
	}
	return dialer, exactNetwork, nil
}

// ResolvePhysical uses the normal bounded/ordered resolver policy, but every
// UDP/TCP socket is pinned to a verified physical interface. No unbound retry.
func ResolvePhysical(ctx context.Context, host string, policy Policy, auto []netip.AddrPort) (netip.Addr, error) {
	return resolveWithDial(ctx, host, policy, auto, func(ctx context.Context, network, address string) (net.Conn, error) {
		endpoint, err := netip.ParseAddrPort(address)
		if err != nil {
			return nil, err
		}
		route, err := SelectPhysicalRoute(ctx, endpoint.Addr())
		if err != nil {
			return nil, err
		}
		return DialPhysical(ctx, network, endpoint, route)
	})
}
