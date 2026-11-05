//go:build windows

package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ipHelper = windows.NewLazySystemDLL("iphlpapi.dll")
var getIPForwardTable2 = ipHelper.NewProc("GetIpForwardTable2")
var freeMibTable = ipHelper.NewProc("FreeMibTable")

// Native layouts from netioapi.h. SOCKADDR_INET is 28 bytes; its containing
// IP_ADDRESS_PREFIX is 32 bytes. MIB_IPFORWARD_ROW2 is 104 bytes on Win64.
type sockaddrInet struct {
	Family uint16
	Data   [26]byte
}
type addressPrefix struct {
	Address sockaddrInet
	Length  uint8
	Padding [3]byte
}
type forwardRow struct {
	LUID                                               uint64
	Index                                              uint32
	Destination                                        addressPrefix
	NextHop                                            sockaddrInet
	SitePrefixLength                                   uint8
	_                                                  [3]byte
	ValidLifetime, PreferredLifetime, Metric, Protocol uint32
	Loopback, Autoconfigure, Publish, Immortal         uint8
	Age, Origin                                        uint32
}
type forwardTable struct {
	Count uint32
	_     uint32
	Rows  [1]forwardRow
}

func sockaddrIP(a *sockaddrInet) netip.Addr {
	switch a.Family {
	case windows.AF_INET:
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(a)).Addr)
	case windows.AF_INET6:
		r := (*windows.RawSockaddrInet6)(unsafe.Pointer(a))
		ip := netip.AddrFrom16(r.Addr)
		if r.Scope_id != 0 {
			ip = ip.WithZone(strconv.FormatUint(uint64(r.Scope_id), 10))
		}
		return ip
	}
	return netip.Addr{}
}

func readSystemAdapters(ctx context.Context) ([]systemAdapter, error) {
	size := uint32(16 * 1024)
	var buffer []byte
	for attempts := 0; attempts < 4; attempts++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if size > 16*1024*1024 {
			return nil, errors.New("ADAPTER_TABLE_TOO_LARGE")
		}
		buffer = make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX|windows.GAA_FLAG_INCLUDE_GATEWAYS, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, errors.New("ADAPTER_READ_FAILED")
		}
		var result []systemAdapter
		for a := first; a != nil; a = a.Next {
			item := systemAdapter{Name: windows.UTF16PtrToString(a.FriendlyName), LUID: a.Luid, Index: a.IfIndex, Index6: a.Ipv6IfIndex,
				Up: a.OperStatus == 1, Metric4: a.Ipv4Metric, Metric6: a.Ipv6Metric}
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if u.Address.Sockaddr == nil {
					continue
				}
				ip, ok := netip.AddrFromSlice(u.Address.IP())
				if ok {
					ip = ip.Unmap()
					if int(u.OnLinkPrefixLength) <= ip.BitLen() {
						item.Addresses = append(item.Addresses, netip.PrefixFrom(ip, int(u.OnLinkPrefixLength)))
					}
				}
			}
			for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
				if d.Address.Sockaddr == nil {
					continue
				}
				ip, ok := netip.AddrFromSlice(d.Address.IP())
				if ok {
					ip = ip.Unmap()
					if ip.Is6() && ip.IsLinkLocalUnicast() {
						ip = ip.WithZone(strconv.FormatUint(uint64(a.Ipv6IfIndex), 10))
					}
					item.DNS = append(item.DNS, ip)
				}
			}
			result = append(result, item)
		}
		runtime.KeepAlive(buffer)
		return result, ctx.Err()
	}
	return nil, errors.New("ADAPTER_TABLE_CHANGED")
}

func readSystemRoutes(ctx context.Context) ([]systemRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if unsafe.Sizeof(forwardRow{}) != 104 || unsafe.Offsetof(forwardRow{}.NextHop) != 44 || unsafe.Offsetof(forwardTable{}.Rows) != 8 {
		return nil, errors.New("UNSUPPORTED_ROUTE_TABLE_LAYOUT")
	}
	var table *forwardTable
	status, _, _ := getIPForwardTable2.Call(windows.AF_UNSPEC, uintptr(unsafe.Pointer(&table)))
	if status != 0 || table == nil {
		return nil, errors.New("ROUTE_READ_FAILED")
	}
	defer freeMibTable.Call(uintptr(unsafe.Pointer(table)))
	if table.Count > 1<<20 {
		return nil, errors.New("ROUTE_TABLE_TOO_LARGE")
	}
	rows := unsafe.Slice(&table.Rows[0], int(table.Count))
	result := make([]systemRoute, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		ip := sockaddrIP(&r.Destination.Address).WithZone("")
		if !ip.IsValid() || int(r.Destination.Length) > ip.BitLen() {
			continue
		}
		result = append(result, systemRoute{LUID: r.LUID, Prefix: netip.PrefixFrom(ip, int(r.Destination.Length)).Masked(), NextHop: sockaddrIP(&r.NextHop), Metric: r.Metric})
	}
	return result, ctx.Err()
}

// SystemNetwork performs only GetAdaptersAddresses and GetIpForwardTable2 reads.
// Two matching snapshots avoid granting a proof against a torn network change.
// No WMI/PowerShell, resolver queries, route writes or firewall calls are used.
func SystemNetwork(ctx context.Context) (NetworkSnapshot, error) {
	var previous NetworkSnapshot
	for i := 0; i < 3; i++ {
		a, err := readSystemAdapters(ctx)
		if err != nil {
			return NetworkSnapshot{}, err
		}
		r, err := readSystemRoutes(ctx)
		if err != nil {
			return NetworkSnapshot{}, err
		}
		n := systemSnapshot(a, r)
		if i > 0 && n.Generation == previous.Generation {
			return n, nil
		}
		previous = n
	}
	return NetworkSnapshot{}, errors.New("NETWORK_SNAPSHOT_UNSTABLE")
}
