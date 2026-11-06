//go:build darwin

package tunengine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func readSystemAdapters(ctx context.Context) ([]systemAdapter, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var adapters []systemAdapter
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		a := systemAdapter{Name: iface.Name, LUID: uint64(iface.Index), Index: uint32(iface.Index), Index6: uint32(iface.Index), Up: iface.Flags&net.FlagUp != 0}
		for _, address := range addresses {
			p, err := netip.ParsePrefix(address.String())
			if err != nil {
				return nil, errors.New("DARWIN_ADDRESS_INVALID")
			}
			a.Addresses = append(a.Addresses, p)
		}
		adapters = append(adapters, a)
	}
	// Use the physical default, not a VPN default or our installed split routes,
	// to associate the unscoped primary resolver with its original interface.
	routes, err := readSystemRoutes(ctx)
	if err != nil {
		return nil, err
	}
	defaultName, err := selectPhysicalInterface(netip.IPv4Unspecified(), adapters, routes)
	if err != nil {
		defaultName = ""
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(readCtx, "/usr/sbin/scutil", "--dns")
	// Never run subprocesses with caller-provided executable paths or env flags.
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	raw, err := cmd.Output()
	if err != nil {
		return nil, errors.New("DARWIN_DNS_READ_FAILED")
	}
	dns, err := parseDarwinDNS(string(raw), defaultName)
	if err != nil {
		return nil, err
	}
	for i := range adapters {
		adapters[i].DNS = dns[adapters[i].Name]
	}
	return adapters, ctx.Err()
}

func darwinRouteAddress(address route.Addr) netip.Addr {
	switch a := address.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		ip := netip.AddrFrom16(a.IP)
		if a.ZoneID != 0 {
			ip = ip.WithZone(strconv.Itoa(a.ZoneID))
		}
		return ip
	}
	return netip.Addr{}
}

func darwinSystemRoutes(messages []route.Message) ([]systemRoute, error) {
	var result []systemRoute
	for _, message := range messages {
		r, ok := message.(*route.RouteMessage)
		if !ok || r.Index <= 0 || r.Flags&unix.RTF_UP == 0 || r.Flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE|unix.RTF_LLINFO|unix.RTF_WASCLONED) != 0 || len(r.Addrs) <= unix.RTAX_NETMASK {
			continue
		}
		destination := darwinRouteAddress(r.Addrs[unix.RTAX_DST]).WithZone("")
		if !destination.IsValid() {
			continue
		}
		bits := destination.BitLen()
		if r.Flags&unix.RTF_HOST == 0 {
			mask := darwinRouteAddress(r.Addrs[unix.RTAX_NETMASK])
			if !mask.IsValid() {
				// Darwin encodes an all-zero default netmask as an empty sockaddr.
				if !destination.IsUnspecified() {
					return nil, errors.New("DARWIN_ROUTE_MASK_INVALID")
				}
				bits = 0
			} else {
				if mask.BitLen() != destination.BitLen() {
					return nil, errors.New("DARWIN_ROUTE_MASK_INVALID")
				}
				var width int
				bits, width = net.IPMask(mask.AsSlice()).Size()
				if width != destination.BitLen() {
					return nil, errors.New("DARWIN_ROUTE_MASK_INVALID")
				}
			}
		}
		// Scoped defaults are per-interface copies, not the global primary route.
		if bits == 0 && r.Flags&unix.RTF_IFSCOPE != 0 {
			continue
		}
		next := darwinRouteAddress(r.Addrs[unix.RTAX_GATEWAY])
		if !next.IsValid() {
			if _, ok := r.Addrs[unix.RTAX_GATEWAY].(*route.LinkAddr); !ok {
				continue
			}
			if destination.Is4() {
				next = netip.IPv4Unspecified()
			} else {
				next = netip.IPv6Unspecified()
			}
		}
		result = append(result, systemRoute{LUID: uint64(r.Index), Prefix: netip.PrefixFrom(destination, bits).Masked(), NextHop: next})
	}
	return result, nil
}

func readSystemRoutes(ctx context.Context) ([]systemRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	result, err := darwinSystemRoutes(messages)
	if err != nil {
		return nil, err
	}
	return result, ctx.Err()
}

func SystemNetwork(ctx context.Context) (NetworkSnapshot, error) {
	return SystemNetworkWithOwned(ctx, nil, nil)
}

// SystemNetworkWithDNS is a trusted-root-manager API, never GUI/IPC input.
// The caller must verify that its DNS override is still installed before
// supplying recorded original DNS. A present empty slice means originally no
// resolver. Substitute before hashing so our own transaction is generation-
// neutral; external resolver/topology changes must revoke the caller's proof.
func SystemNetworkWithDNS(ctx context.Context, originals map[string][]netip.Addr) (NetworkSnapshot, error) {
	return SystemNetworkWithOwned(ctx, originals, nil)
}

// SystemNetworkWithOwned additionally excludes exact, verified physical route
// identities before hashing every stability read. The trusted root caller must
// verify ownership and supply its journaled pins; this is not an IPC API.
// Different interfaces, gateways, metrics or prefixes remain topology changes.
func SystemNetworkWithOwned(ctx context.Context, originals map[string][]netip.Addr, ignored []OwnedPhysicalRoute) (NetworkSnapshot, error) {
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
		n := systemSnapshot(darwinOriginalDNS(a, originals), darwinWithoutOwnedRoutes(a, r, ignored))
		if i > 0 && n.Generation == previous.Generation {
			return n, nil
		}
		previous = n
	}
	return NetworkSnapshot{}, errors.New("NETWORK_SNAPSHOT_UNSTABLE")
}
