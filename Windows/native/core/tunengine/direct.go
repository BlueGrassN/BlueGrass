package tunengine

import (
	"context"
	"errors"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
)

// OS sockets are only used for explicit loopback, interface-scoped LAN/DNS,
// or authenticated SOCKS relay plumbing. They never resolve names implicitly.
type scopedDirect struct {
	interfaceName string
	loopback      bool
}

func (s *scopedDirect) destination(d M.Socksaddr) M.Socksaddr {
	if d.Addr.Is4In6() {
		d.Addr = d.Addr.Unmap()
	}
	if s.interfaceName != "" && d.Addr.Is6() && (d.Addr.IsLinkLocalUnicast() || d.Addr.IsMulticast()) {
		d.Addr = d.Addr.WithZone(s.interfaceName)
	}
	return d
}
func (s *scopedDirect) DialContext(ctx context.Context, network string, d M.Socksaddr) (net.Conn, error) {
	d = s.destination(d)
	if !d.Addr.IsValid() || d.Fqdn != "" || s.loopback && !d.Addr.IsLoopback() {
		return nil, errors.New("PHYSICAL_LITERAL_REQUIRED")
	}
	dial := net.Dialer{Timeout: 10 * time.Second}
	if s.interfaceName != "" {
		// The generic finder refreshes every adapter and its address table on a
		// cache miss. Creating one for each socket made concurrent TUN connections
		// spend seconds in enumeration before connect. Resolve only the requested
		// interface; keep the exact index binding and fail if it disappeared.
		iface, err := net.InterfaceByName(s.interfaceName)
		if err != nil {
			return nil, err
		}
		dial.Control = control.BindToInterface(nil, s.interfaceName, iface.Index)
	}
	if s.loopback {
		if network == "udp" {
			dial.LocalAddr = &net.UDPAddr{IP: loopbackAddress(d.Addr)}
		} else {
			dial.LocalAddr = &net.TCPAddr{IP: loopbackAddress(d.Addr)}
		}
	}
	c, e := dial.DialContext(ctx, network, d.String())
	if e != nil {
		return nil, e
	}
	if d.Addr.IsMulticast() {
		if s.interfaceName == "" {
			_ = c.Close()
			return nil, errors.New("MULTICAST_SCOPE_REQUIRED")
		}
		iface, err := net.InterfaceByName(s.interfaceName)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		packet, ok := c.(net.PacketConn)
		if !ok {
			_ = c.Close()
			return nil, errors.New("MULTICAST_UDP_REQUIRED")
		}
		if d.Addr.Is4() {
			e = ipv4.NewPacketConn(packet).SetMulticastInterface(iface)
		} else {
			e = ipv6.NewPacketConn(packet).SetMulticastInterface(iface)
		}
		if e != nil {
			_ = c.Close()
			return nil, e
		}
	}
	return c, nil
}
func (s *scopedDirect) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	c, e := s.DialContext(ctx, "udp", d)
	if e != nil {
		return nil, e
	}
	return &connectedPacket{Conn: c, remote: s.destination(d).UDPAddr()}, nil
}

// One connected physical socket per destination. Its zone selects the NIC;
// packets exchanged with the IP stack have an unscoped, logical peer address.
type connectedPacket struct {
	net.Conn
	remote net.Addr
}

func (c *connectedPacket) logicalPeer() M.Socksaddr {
	d := M.ParseSocksaddr(c.remote.String())
	d.Addr = d.Addr.WithZone("")
	return d
}

func (c *connectedPacket) ReadFrom(b []byte) (int, net.Addr, error) {
	n, e := c.Read(b)
	return n, c.logicalPeer().UDPAddr(), e
}
func (c *connectedPacket) WriteTo(b []byte, a net.Addr) (int, error) {
	if a == nil {
		return 0, errors.New("PACKET_DESTINATION_CHANGED")
	}
	// SocksaddrFromNet discards net.UDPAddr.Zone; parse the literal address to
	// retain the explicit interface until it has been checked.
	destination := M.ParseSocksaddr(a.String())
	physical := M.ParseSocksaddr(c.remote.String())
	// An explicit scope cannot select another interface. An absent scope is
	// expected from TUN packets and never changes the connected physical peer.
	if zone := destination.Addr.Zone(); zone != "" && zone != physical.Addr.Zone() {
		return 0, errors.New("PACKET_DESTINATION_CHANGED")
	}
	destination.Addr = destination.Addr.WithZone("")
	if destination != c.logicalPeer() {
		return 0, errors.New("PACKET_DESTINATION_CHANGED")
	}
	return c.Write(b)
}

func selectPhysicalInterface(entry netip.Addr, adapters []systemAdapter, routes []systemRoute) (string, error) {
	bestBits := -1
	bestMetric := uint64(^uint64(0))
	name := ""
	ambiguous := false
	for _, r := range routes {
		if !r.Prefix.Contains(entry) {
			continue
		}
		for _, a := range adapters {
			if a.LUID != r.LUID || !a.Up || !physicalInterfaceName(a.Name) {
				continue
			}
			metric := uint64(r.Metric) + uint64(a.Metric4)
			if entry.Is6() {
				metric = uint64(r.Metric) + uint64(a.Metric6)
			}
			if r.Prefix.Bits() > bestBits || r.Prefix.Bits() == bestBits && metric < bestMetric {
				bestBits, bestMetric, name, ambiguous = r.Prefix.Bits(), metric, a.Name, false
			} else if r.Prefix.Bits() == bestBits && metric == bestMetric && name != a.Name {
				ambiguous = true
			}
		}
	}
	if name == "" || ambiguous {
		return "", errors.New("PHYSICAL_ROUTE_REQUIRED")
	}
	return name, nil
}
func physicalInterface(ctx context.Context, entry netip.Addr) (string, error) {
	a, e := readSystemAdapters(ctx)
	if e != nil {
		return "", e
	}
	r, e := readSystemRoutes(ctx)
	if e != nil {
		return "", e
	}
	return selectPhysicalInterface(entry, a, r)
}

func loopbackAddress(a netip.Addr) net.IP {
	if a.Is4() {
		return net.IPv4(127, 0, 0, 1)
	}
	return net.IPv6loopback
}
