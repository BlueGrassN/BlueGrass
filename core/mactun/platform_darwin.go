//go:build darwin

package mactun

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"bluegrass/native/core/mactunipc"
	"bluegrass/native/core/tunengine"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

type darwinBackend struct{ lease *os.File }

func New(ownerUID uint32) (*Tunnel, error) {
	if os.Geteuid() != 0 || ownerUID == 0 {
		return nil, errors.New("MACOS_ROOT_TUN_REQUIRED")
	}
	if tunengine.InterfaceName != adapterName {
		return nil, errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	if err := secureDir(mactunipc.StateDirectory, 0700); err != nil {
		return nil, err
	}
	if err := secureDir(privateRuntime, 0755); err != nil {
		return nil, err
	}
	lease, err := secureOpen(mactunipc.StateDirectory+"/network.lock", unix.O_CREAT|unix.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lease.Close()
		return nil, errors.New("TUN_SYSTEM_LEASE_BUSY")
	}
	b := &darwinBackend{lease: lease}
	intent, err := mactunipc.ReadIntent()
	if err != nil {
		b.Close()
		return nil, err
	}
	if intent != nil && intent.OwnerUID != ownerUID {
		b.Close()
		return nil, ErrOwner
	}
	if intent != nil && intent.Active {
		j, e := b.LoadJournal()
		if e != nil || j == nil {
			b.Close()
			return nil, ErrRecovery
		}
	}
	t, err := newTunnel(ownerUID, b)
	if err != nil {
		b.Close()
		return nil, err
	}
	t.cachePath = mactunipc.StateDirectory + "/fakeip.db"
	t.clearCache = tunengine.ClearCacheMappings
	return t, nil
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("OS_OUTPUT_LIMIT")
	}
	return b.Buffer.Write(p)
}

// Never use a shell, caller PATH, caller locale, inherited DYLD environment,
// unbounded output, or an executable/path supplied in an IPC profile.
func systemCommand(ctx context.Context, input string, path string, args ...string) (string, string, error) {
	switch path {
	case "/sbin/pfctl", "/sbin/route", "/usr/sbin/networksetup", "/usr/sbin/ipconfig":
	default:
		return "", "", errors.New("OS_TOOL_REJECTED")
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "LANG=C"}
	cmd.Stdin = strings.NewReader(input)
	cmd.WaitDelay = 100 * time.Millisecond
	stdout, stderr := &limitedBuffer{max: 512 * 1024}, &limitedBuffer{max: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), errors.New("MACOS_NETWORK_COMMAND_FAILED")
	}
	return stdout.String(), stderr.String(), nil
}
func (b *darwinBackend) Network(ctx context.Context, original map[string][]netip.Addr, owned []Route) (tunengine.NetworkSnapshot, error) {
	var pins []tunengine.OwnedPhysicalRoute
	for _, r := range owned {
		if !validRoute(r) || r.Interface == adapterName {
			return tunengine.NetworkSnapshot{}, ErrRecovery
		}
		pins = append(pins, tunengine.OwnedPhysicalRoute{Prefix: r.Prefix, Interface: r.Interface, Gateway: r.Gateway, Metric: r.Metric})
	}
	return tunengine.SystemNetworkWithOwned(ctx, original, pins)
}

func routeIP(a route.Addr) netip.Addr {
	switch a := a.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	}
	return netip.Addr{}
}

type kernelRoute struct {
	Route
	scoped bool
}

func kernelRoutes(ctx context.Context) ([]kernelRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rib, err := route.FetchRIB(unix.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	var result []kernelRoute
	for len(rib) > 0 {
		if len(rib) < unix.SizeofRtMsghdr {
			return nil, ErrRecovery
		}
		length := int(binary.LittleEndian.Uint16(rib[:2]))
		if length < unix.SizeofRtMsghdr || length > len(rib) {
			return nil, ErrRecovery
		}
		raw := rib[:length]
		rib = rib[length:]
		messages, e := route.ParseRIB(route.RIBTypeRoute, raw)
		if e != nil {
			return nil, e
		}
		const metricOffset = int(unsafe.Offsetof(unix.RtMsghdr{}.Rmx)) + int(unsafe.Offsetof(unix.RtMetrics{}.Hopcount))
		metric := binary.LittleEndian.Uint32(raw[metricOffset : metricOffset+4])
		for _, msg := range messages {
			r, ok := msg.(*route.RouteMessage)
			if !ok || r.Index <= 0 || r.Flags&unix.RTF_UP == 0 || r.Flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE|unix.RTF_LLINFO|unix.RTF_WASCLONED) != 0 || len(r.Addrs) <= unix.RTAX_NETMASK {
				continue
			}
			ip := routeIP(r.Addrs[unix.RTAX_DST])
			if !ip.Is4() {
				continue
			}
			bits := 32
			if r.Flags&unix.RTF_HOST == 0 {
				mask := routeIP(r.Addrs[unix.RTAX_NETMASK])
				if !mask.IsValid() {
					if !ip.IsUnspecified() {
						return nil, ErrRecovery
					}
					bits = 0
				} else {
					var width int
					bits, width = net.IPMask(mask.AsSlice()).Size()
					if width != 32 {
						return nil, ErrRecovery
					}
				}
			}
			iface, err := net.InterfaceByIndex(r.Index)
			if err != nil {
				return nil, err
			}
			gateway := routeIP(r.Addrs[unix.RTAX_GATEWAY])
			result = append(result, kernelRoute{Route: Route{Prefix: netip.PrefixFrom(ip, bits).Masked(), Interface: iface.Name, Gateway: gateway, Metric: metric}, scoped: r.Flags&unix.RTF_IFSCOPE != 0})
		}
	}
	return result, ctx.Err()
}
func (b *darwinBackend) Underlay(ctx context.Context) (Underlay, error) {
	routes, err := kernelRoutes(ctx)
	if err != nil {
		return Underlay{}, err
	}
	var selected *Route
	for _, r := range routes {
		if r.scoped || r.Prefix.Bits() != 0 || !interfacePattern.MatchString(r.Interface) {
			continue
		}
		if selected != nil && *selected != r.Route {
			return Underlay{}, errors.New("PHYSICAL_ROUTE_AMBIGUOUS")
		}
		copy := r.Route
		selected = &copy
	}
	if selected == nil || !selected.Gateway.Is4() || selected.Gateway.IsUnspecified() {
		return Underlay{}, errors.New("PHYSICAL_ROUTE_UNAVAILABLE")
	}
	iface, err := net.InterfaceByName(selected.Interface)
	if err != nil || iface.Flags&net.FlagUp == 0 || len(iface.HardwareAddr) == 0 {
		return Underlay{}, errors.New("PHYSICAL_INTERFACE_UNAVAILABLE")
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return Underlay{}, err
	}
	for _, address := range addresses {
		p, e := netip.ParsePrefix(address.String())
		if e == nil && p.Addr().Is4() && p.Contains(selected.Gateway) {
			return Underlay{Interface: iface.Name, Index: uint32(iface.Index), Gateway: selected.Gateway, Source: p.Addr()}, nil
		}
	}
	return Underlay{}, errors.New("PHYSICAL_SOURCE_UNAVAILABLE")
}
func (b *darwinBackend) ReadRoute(ctx context.Context, p netip.Prefix) (*Route, error) {
	routes, err := kernelRoutes(ctx)
	if err != nil {
		return nil, err
	}
	var found *Route
	for _, r := range routes {
		if r.scoped || r.Prefix != p {
			continue
		}
		if found != nil && *found != r.Route {
			return nil, ErrExternalWriter
		}
		copy := r.Route
		found = &copy
	}
	return found, nil
}
func (b *darwinBackend) AddRoute(ctx context.Context, r Route) error {
	if !validRoute(r) {
		return ErrRecovery
	}
	before, err := b.ReadRoute(ctx, r.Prefix)
	if err != nil {
		return err
	}
	if before != nil {
		return ErrExternalWriter
	}
	args := []string{"-n", "add", "-net", r.Prefix.String()}
	args = append(args, "-hopcount", "0")
	if r.Interface == adapterName {
		args = append(args, "-interface", adapterName)
	} else {
		args = append(args, r.Gateway.String(), "-ifp", r.Interface)
	}
	_, _, err = systemCommand(ctx, "", "/sbin/route", args...)
	return err
}
func (b *darwinBackend) DeleteRoute(ctx context.Context, r Route) error {
	current, err := b.ReadRoute(ctx, r.Prefix)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if *current != r {
		return ErrExternalWriter
	}
	args := []string{"-n", "delete", "-net", r.Prefix.String()}
	if r.Interface == adapterName {
		args = append(args, "-interface", adapterName)
	} else {
		args = append(args, r.Gateway.String(), "-ifp", r.Interface)
	}
	_, _, err = systemCommand(ctx, "", "/sbin/route", args...)
	return err
}
func (b *darwinBackend) VerifyAdapter(ctx context.Context, index uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	iface, err := net.InterfaceByName(adapterName)
	if err != nil || index == 0 || uint64(iface.Index) != index || iface.Flags&net.FlagUp == 0 {
		return errors.New("TUN_ADAPTER_NOT_READY")
	}
	return nil
}
func (b *darwinBackend) Dial(ctx context.Context, network string, endpoint netip.AddrPort, u Underlay) (net.Conn, error) {
	if !validEndpoint(endpoint) || network != "tcp" && network != "udp" {
		return nil, ErrRecovery
	}
	verify := func() error {
		current, err := b.Underlay(ctx)
		if err != nil {
			return err
		}
		if current != u {
			return errors.New("PHYSICAL_ROUTE_CHANGED")
		}
		return nil
	}
	dialer := net.Dialer{Timeout: time.Second}
	if network == "tcp" {
		dialer.LocalAddr = &net.TCPAddr{IP: u.Source.AsSlice()}
	} else {
		dialer.LocalAddr = &net.UDPAddr{IP: u.Source.AsSlice()}
	}
	dialer.ControlContext = func(c context.Context, n, address string, raw syscall.RawConn) error {
		if n != network+"4" || address != endpoint.String() {
			return ErrRecovery
		}
		if err := verify(); err != nil {
			return err
		}
		var bindErr error
		if err := raw.Control(func(fd uintptr) {
			bindErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, int(u.Index))
		}); err != nil {
			return err
		}
		return bindErr
	}
	conn, err := dialer.DialContext(ctx, network+"4", endpoint.String())
	if err != nil {
		return nil, err
	}
	local, e := netip.ParseAddrPort(conn.LocalAddr().String())
	if e != nil || local.Addr().Unmap() != u.Source {
		conn.Close()
		return nil, ErrRecovery
	}
	if err = verify(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (b *darwinBackend) ReadDNS(ctx context.Context, service string) ([]netip.Addr, error) {
	if !validService(service) {
		return nil, ErrRecovery
	}
	out, _, err := systemCommand(ctx, "", "/usr/sbin/networksetup", "-getdnsservers", service)
	if err != nil {
		return nil, err
	}
	return parseDNSOutput(out)
}
func (b *darwinBackend) WriteDNS(ctx context.Context, service string, servers []netip.Addr) error {
	if !validService(service) || !validAddresses(servers) {
		return ErrRecovery
	}
	args := []string{"-setdnsservers", service}
	if len(servers) == 0 {
		args = append(args, "Empty")
	} else {
		for _, ip := range servers {
			args = append(args, ip.String())
		}
	}
	out, _, err := systemCommand(ctx, "", "/usr/sbin/networksetup", args...)
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error") {
		return errors.New("DNS_WRITE_FAILED")
	}
	return nil
}

func (b *darwinBackend) dhcpDNS(ctx context.Context, iface string) ([]netip.Addr, error) {
	if !interfacePattern.MatchString(iface) {
		return nil, ErrRecovery
	}
	raw, _, err := systemCommand(ctx, "", "/usr/sbin/ipconfig", "getoption", iface, "domain_name_server")
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	for _, field := range strings.Fields(raw) {
		ip, e := netip.ParseAddr(field)
		if e != nil || !ip.Is4() {
			return nil, ErrRecovery
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 || !validAddresses(ips) {
		return nil, ErrRecovery
	}
	return ips, nil
}

func (b *darwinBackend) VerifyResolver(ctx context.Context, before Resolver) error {
	out, _, err := systemCommand(ctx, "", "/usr/sbin/networksetup", "-listnetworkserviceorder")
	if err != nil {
		return err
	}
	services, err := parseServiceOrder(out)
	if err != nil {
		return err
	}
	found := false
	for _, s := range services {
		if s.Service == before.Service && s.Interface == before.Interface {
			found = true
		}
	}
	if !found {
		return ErrExternalWriter
	}
	if len(before.DHCP) > 0 {
		current, e := b.dhcpDNS(ctx, before.Interface)
		if e != nil {
			return e
		}
		if !sameAddresses(current, before.DHCP) {
			return errors.New("NETWORK_CHANGED")
		}
	}
	return nil
}
func (b *darwinBackend) Resolvers(ctx context.Context) ([]Resolver, error) {
	out, _, err := systemCommand(ctx, "", "/usr/sbin/networksetup", "-listnetworkserviceorder")
	if err != nil {
		return nil, err
	}
	services, err := parseServiceOrder(out)
	if err != nil {
		return nil, err
	}
	n, err := b.Network(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	u, err := b.Underlay(ctx)
	if err != nil {
		return nil, err
	}
	return collectResolvers(services, u, func(name string) (resolverInterface, error) {
		iface, e := net.InterfaceByName(name)
		if e != nil {
			return resolverInterface{}, e
		}
		addresses, e := iface.Addrs()
		if e != nil {
			return resolverInterface{}, e
		}
		info := resolverInterface{Up: iface.Flags&net.FlagUp != 0, Hardware: len(iface.HardwareAddr) > 0}
		for _, address := range addresses {
			prefix, e := netip.ParsePrefix(address.String())
			if e != nil {
				return resolverInterface{}, e
			}
			info.Addresses = append(info.Addresses, prefix)
		}
		return info, nil
	}, func(r Resolver) (Resolver, error) {
		var e error
		r.Servers, e = b.ReadDNS(ctx, r.Service)
		if e != nil {
			return r, e
		}
		r.Effective = append([]netip.Addr(nil), r.Servers...)
		if len(r.Servers) == 0 {
			r.DHCP, e = b.dhcpDNS(ctx, r.Interface)
			// Without an independent resolver source we cannot safely mask a
			// manual override while still detecting the real network changing.
			if e != nil {
				return r, errors.New("PHYSICAL_DNS_SOURCE_UNVERIFIABLE")
			}
		}
		if r.Interface == u.Interface {
			r.Effective = nil
			for _, d := range n.EntryDNS {
				r.Effective = append(r.Effective, d.Addr())
			}
		}
		if len(r.Effective) == 0 {
			r.Effective = append([]netip.Addr(nil), r.DHCP...)
		}
		return r, nil
	})
}

var _ Backend = (*darwinBackend)(nil)
