//go:build windows && (amd64 || arm64)

package entrydns

import (
	"context"
	"errors"
	"math/bits"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var physicalIPHelper = windows.NewLazySystemDLL("iphlpapi.dll")
var physicalGetBestRoute2 = physicalIPHelper.NewProc("GetBestRoute2")

// Win64 netioapi.h ABI: SOCKADDR_INET=28, IP_ADDRESS_PREFIX=32,
// MIB_IPFORWARD_ROW2=104; no opaque third-party handles or route writes.
type routeSockaddr struct {
	Family uint16
	Data   [26]byte
}
type routePrefix struct {
	Address routeSockaddr
	Length  uint8
	_       [3]byte
}
type physicalRouteRow struct {
	LUID                                               uint64
	Index                                              uint32
	Destination                                        routePrefix
	NextHop                                            routeSockaddr
	SitePrefixLength                                   uint8
	_                                                  [3]byte
	ValidLifetime, PreferredLifetime, Metric, Protocol uint32
	Loopback, Autoconfigure, Publish, Immortal         uint8
	Age, Origin                                        uint32
}

func routeAddress(a *routeSockaddr) netip.Addr {
	if a.Family == windows.AF_INET {
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(a)).Addr)
	}
	if a.Family == windows.AF_INET6 {
		return netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(a)).Addr)
	}
	return netip.Addr{}
}

func routeDestination(ip netip.Addr) routeSockaddr {
	var result routeSockaddr
	if ip.Is4() {
		p := (*windows.RawSockaddrInet4)(unsafe.Pointer(&result))
		p.Family, p.Addr = windows.AF_INET, ip.As4()
	} else {
		p := (*windows.RawSockaddrInet6)(unsafe.Pointer(&result))
		p.Family, p.Addr = windows.AF_INET6, ip.As16()
	}
	return result
}

func bestPhysicalCandidate(adapter *windows.IpAdapterAddresses, destination netip.Addr) (physicalCandidate, error) {
	row := windows.MibIfRow2{InterfaceLuid: adapter.Luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return physicalCandidate{}, err
	}
	name := windows.UTF16PtrToString(adapter.FriendlyName)
	// Hardware Ethernet/Wi-Fi/WWAN only. Wintun, other VPNs, software vEthernet,
	// loopback, down/paused/disconnected interfaces cannot become a fallback.
	flags := row.InterfaceAndOperStatusFlags
	if row.InterfaceLuid != adapter.Luid || row.OperStatus != windows.IfOperStatusUp || row.AdminStatus != 1 || row.MediaConnectState != 1 ||
		flags&1 == 0 || flags&0xfa != 0 || !physicalInterfaceType(row.Type) || strings.HasPrefix(strings.ToLower(name), "bluegrass") || row.InterfaceGuid == (windows.GUID{}) {
		return physicalCandidate{}, errPhysicalRoute
	}
	if unsafe.Sizeof(physicalRouteRow{}) != 104 || unsafe.Offsetof(physicalRouteRow{}.NextHop) != 44 {
		return physicalCandidate{}, errPhysicalRoute
	}
	dest := routeDestination(destination)
	var best physicalRouteRow
	var source routeSockaddr
	luid := adapter.Luid
	status, _, _ := physicalGetBestRoute2.Call(uintptr(unsafe.Pointer(&luid)), 0, 0, uintptr(unsafe.Pointer(&dest)), 0, uintptr(unsafe.Pointer(&best)), uintptr(unsafe.Pointer(&source)))
	runtime.KeepAlive(adapter)
	if status != 0 {
		return physicalCandidate{}, syscall.Errno(status)
	}
	index, metric := adapter.IfIndex, adapter.Ipv4Metric
	if destination.Is6() {
		index, metric = adapter.Ipv6IfIndex, adapter.Ipv6Metric
	}
	local := routeAddress(&source)
	assigned := false
	for u := adapter.FirstUnicastAddress; u != nil; u = u.Next {
		if u.Address.Sockaddr == nil || u.DadState != windows.IpDadStatePreferred || u.ValidLifetime == 0 {
			continue
		}
		ip, ok := netip.AddrFromSlice(u.Address.IP())
		if ok && ip.Unmap() == local {
			assigned = true
			break
		}
	}
	prefixIP := routeAddress(&best.Destination.Address)
	if !assigned || best.LUID != adapter.Luid || best.Index != index || !prefixIP.IsValid() || int(best.Destination.Length) > prefixIP.BitLen() || best.Loopback != 0 || best.ValidLifetime == 0 {
		return physicalCandidate{}, errPhysicalRoute
	}
	return physicalCandidate{Route: PhysicalRoute{Destination: destination, InterfaceIndex: index, InterfaceLUID: luid,
		AdapterGUID: row.InterfaceGuid.String(), LocalAddress: local, Prefix: netip.PrefixFrom(prefixIP, int(best.Destination.Length)).Masked(),
		NextHop: routeAddress(&best.NextHop), Metric: uint64(best.Metric) + uint64(metric)}, Name: name, IfType: row.Type, Up: true, Hardware: true}, nil
}

func physicalSnapshot(ctx context.Context, destination netip.Addr) ([]physicalCandidate, uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if !physicalAddress(destination) {
		return nil, 0, errPhysicalRoute
	}
	var sa windows.Sockaddr
	if destination.Is4() {
		sa = &windows.SockaddrInet4{Addr: destination.As4()}
	} else {
		sa = &windows.SockaddrInet6{Addr: destination.As16()}
	}
	var osBest uint32
	if err := windows.GetBestInterfaceEx(sa, &osBest); err != nil {
		return nil, 0, err
	}
	size := uint32(16 * 1024)
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if size > 16*1024*1024 {
			return nil, 0, errPhysicalRoute
		}
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		var candidates []physicalCandidate
		count := 0
		for a := first; a != nil; a = a.Next {
			count++
			if count > 256 {
				return nil, 0, errPhysicalRoute
			}
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			if a.Luid == 0 || a.OperStatus != windows.IfOperStatusUp || !physicalInterfaceType(a.IfType) {
				continue
			}
			candidate, err := bestPhysicalCandidate(a, destination)
			if err == nil {
				candidates = append(candidates, candidate)
			}
		}
		runtime.KeepAlive(buffer)
		return candidates, osBest, ctx.Err()
	}
	return nil, 0, errPhysicalRoute
}

// SelectPhysicalRoute reads at most three destination-specific snapshots.
// GetBestRoute2 is constrained to each verified physical LUID, so an active
// TUN's more-specific route cannot capture the chosen DNS/update socket.
func SelectPhysicalRoute(ctx context.Context, destination netip.Addr) (PhysicalRoute, error) {
	var previous PhysicalRoute
	for attempt := 0; attempt < 3; attempt++ {
		candidates, osBest, err := physicalSnapshot(ctx, destination)
		if err != nil {
			return PhysicalRoute{}, err
		}
		selected, err := selectPhysicalRoute(destination, candidates, osBest)
		if err != nil {
			return PhysicalRoute{}, err
		}
		if attempt > 0 && selected == previous {
			return selected, nil
		}
		previous = selected
	}
	return PhysicalRoute{}, errors.New("PHYSICAL_ROUTE_UNSTABLE")
}

func VerifyPhysicalRoute(ctx context.Context, route PhysicalRoute) error {
	return verifyPhysicalRoute(ctx, route, SelectPhysicalRoute)
}

func bindPhysicalInterface(raw syscall.RawConn, route PhysicalRoute, setOption func(uintptr, int, int, int) error) error {
	if route.InterfaceIndex == 0 || !physicalAddress(route.LocalAddress) || route.LocalAddress.Is4() != route.Destination.Is4() {
		return errPhysicalRoute
	}
	level, value := windows.IPPROTO_IP, int(bits.ReverseBytes32(route.InterfaceIndex))
	if route.Destination.Is6() {
		level, value = windows.IPPROTO_IPV6, int(route.InterfaceIndex)
	}
	var optionErr error
	err := raw.Control(func(fd uintptr) { optionErr = setOption(fd, level, 31, value) }) // IP[ V6 ]_UNICAST_IF
	if err != nil {
		return err
	}
	return optionErr
}

func setVerifiedInterface(fd uintptr, level, option, value int) error {
	if err := windows.SetsockoptInt(windows.Handle(fd), level, option, value); err != nil {
		return err
	}
	actual, err := windows.GetsockoptInt(windows.Handle(fd), level, option)
	if err != nil {
		return err
	}
	// Winsock's IPv4 setter takes network byte order, but its getter returns
	// host byte order. IPV6_UNICAST_IF uses host byte order in both directions.
	if !interfaceOptionMatches(level, value, actual) {
		return errors.New("PHYSICAL_SOCKET_INTERFACE_MISMATCH")
	}
	return nil
}

func interfaceOptionMatches(level, setValue, actual int) bool {
	expected := uint32(setValue)
	if level == windows.IPPROTO_IP {
		expected = bits.ReverseBytes32(expected)
	}
	return uint32(actual) == expected
}

// DialPhysical accepts only an exact numeric endpoint and a fresh route. It
// pins source address and interface before connect, with no unbound retry.
func DialPhysical(ctx context.Context, network string, endpoint netip.AddrPort, route PhysicalRoute) (net.Conn, error) {
	dialer, exactNetwork, err := boundPhysicalDialer(network, endpoint, route, VerifyPhysicalRoute, func(raw syscall.RawConn, route PhysicalRoute) error {
		return bindPhysicalInterface(raw, route, setVerifiedInterface)
	})
	if err != nil {
		return nil, err
	}
	if err := VerifyPhysicalRoute(ctx, route); err != nil {
		return nil, err
	}
	conn, err := dialer.DialContext(ctx, exactNetwork, endpoint.String())
	if err != nil {
		return nil, err
	}
	if err := verifyPhysicalConnection(ctx, conn, route, VerifyPhysicalRoute); err != nil {
		return nil, err
	}
	return conn, nil
}
