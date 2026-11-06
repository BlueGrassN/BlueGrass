package entrydns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
)

func routeFixture(index uint32, prefix string, metric uint64) physicalCandidate {
	return physicalCandidate{Route: PhysicalRoute{Destination: netip.MustParseAddr("1.1.1.1"), InterfaceIndex: index, InterfaceLUID: uint64(index),
		AdapterGUID: "{00112233-4455-6677-8899-AABBCCDDEEFF}", LocalAddress: netip.MustParseAddr("192.168.1.20"),
		Prefix: netip.MustParsePrefix(prefix), NextHop: netip.MustParseAddr("192.168.1.1"), Metric: metric}, Name: "Ethernet", IfType: 6, Up: true, Hardware: true}
}

func TestPhysicalSelectionExcludesTUNAndUsesSpecificRouteThenMetric(t *testing.T) {
	base := routeFixture(1, "0.0.0.0/0", 1)
	specific := routeFixture(2, "1.1.1.0/24", 20)
	faster := routeFixture(3, "1.1.1.0/24", 10)
	tun := routeFixture(99, "1.1.1.1/32", 0)
	tun.Name, tun.Hardware = "BlueGrass", false
	got, err := selectPhysicalRoute(base.Route.Destination, []physicalCandidate{base, specific, tun, faster}, 99)
	if err != nil || got != faster.Route {
		t.Fatal("TUN route captured physical selection", got, err)
	}
	if _, err := selectPhysicalRoute(base.Route.Destination, []physicalCandidate{base, specific}, 1); err == nil {
		t.Fatal("inconsistent physical OS route accepted")
	}
}

func TestPhysicalSelectionAcceptsOnlyVerifiedHardwareWWAN(t *testing.T) {
	for _, ifType := range []uint32{243, 244} {
		cellular := routeFixture(42, "0.0.0.0/0", 45)
		cellular.Name, cellular.IfType = "Cellular", ifType
		if got, err := selectPhysicalRoute(cellular.Route.Destination, []physicalCandidate{cellular}, 42); err != nil || got != cellular.Route {
			t.Fatalf("hardware WWAN %d rejected: %v", ifType, err)
		}
		for _, alter := range []func(*physicalCandidate){
			func(c *physicalCandidate) { c.Hardware = false },
			func(c *physicalCandidate) { c.Up = false },
			func(c *physicalCandidate) { c.Name = "BlueGrass TUN" },
			func(c *physicalCandidate) { c.Route.InterfaceLUID = 0 },
			func(c *physicalCandidate) { c.Route.AdapterGUID = "" },
			func(c *physicalCandidate) { c.Route.LocalAddress = netip.MustParseAddr("198.19.0.1") },
		} {
			invalid := cellular
			alter(&invalid)
			if _, err := selectPhysicalRoute(cellular.Route.Destination, []physicalCandidate{invalid}, 42); err == nil {
				t.Fatalf("unverified WWAN %d accepted", ifType)
			}
		}
	}
}

func TestPhysicalSelectionRejectsUnusableOrAmbiguousSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*physicalCandidate)
	}{
		{"down", func(c *physicalCandidate) { c.Up = false }},
		{"virtual", func(c *physicalCandidate) { c.Hardware = false }},
		{"own", func(c *physicalCandidate) { c.Name = "BlueGrass TUN" }},
		{"unsupported", func(c *physicalCandidate) { c.IfType = 53 }},
		{"luid", func(c *physicalCandidate) { c.Route.InterfaceLUID = 0 }},
		{"guid", func(c *physicalCandidate) { c.Route.AdapterGUID = "" }},
		{"fake-source", func(c *physicalCandidate) { c.Route.LocalAddress = netip.MustParseAddr("198.18.0.1") }},
		{"family", func(c *physicalCandidate) { c.Route.LocalAddress = netip.MustParseAddr("2001:db8::1") }},
		{"wrong-prefix", func(c *physicalCandidate) { c.Route.Prefix = netip.MustParsePrefix("8.8.8.0/24") }},
		{"wrong-destination", func(c *physicalCandidate) { c.Route.Destination = netip.MustParseAddr("8.8.8.8") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := routeFixture(1, "0.0.0.0/0", 1)
			dest := c.Route.Destination
			tc.alter(&c)
			if _, err := selectPhysicalRoute(dest, []physicalCandidate{c}, 1); err == nil {
				t.Fatal("unusable route accepted")
			}
		})
	}
	a, b := routeFixture(1, "0.0.0.0/0", 1), routeFixture(2, "0.0.0.0/0", 1)
	if _, err := selectPhysicalRoute(a.Route.Destination, []physicalCandidate{a, b}, 99); err == nil {
		t.Fatal("ambiguous physical routes accepted")
	}
	if got, err := selectPhysicalRoute(a.Route.Destination, []physicalCandidate{a, b}, 2); err != nil || got != b.Route {
		t.Fatal("OS-confirmed tie rejected", got, err)
	}
	for _, prefix := range excludedPhysicalPrefixes {
		if physicalAddress(prefix.Addr()) {
			t.Fatal("virtual pool accepted", prefix)
		}
	}
	for _, ip := range []string{"fe80::1", "fe80::1%3", "::ffff:1.1.1.1", "127.0.0.1", "224.0.0.1"} {
		if physicalAddress(netip.MustParseAddr(ip)) {
			t.Fatal("unrepresentable endpoint accepted", ip)
		}
	}
}

func TestPhysicalVerificationRejectsSnapshotDrift(t *testing.T) {
	r := routeFixture(1, "0.0.0.0/0", 1).Route
	for _, change := range []func(*PhysicalRoute){
		func(r *PhysicalRoute) { r.InterfaceLUID++ }, func(r *PhysicalRoute) { r.InterfaceIndex++ },
		func(r *PhysicalRoute) { r.AdapterGUID = "another" }, func(r *PhysicalRoute) { r.Metric++ },
		func(r *PhysicalRoute) { r.LocalAddress = netip.MustParseAddr("192.168.1.21") },
		func(r *PhysicalRoute) { r.NextHop = netip.MustParseAddr("192.168.1.2") },
		func(r *PhysicalRoute) { r.Prefix = netip.MustParsePrefix("1.1.1.0/24") },
	} {
		next := r
		change(&next)
		if verifyPhysicalRoute(context.Background(), r, func(context.Context, netip.Addr) (PhysicalRoute, error) { return next, nil }) == nil {
			t.Fatal("changed route reused")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(verifyPhysicalRoute(ctx, r, func(context.Context, netip.Addr) (PhysicalRoute, error) {
		t.Fatal("lookup after cancellation")
		return r, nil
	}), context.Canceled) {
		t.Fatal("lost cancellation")
	}
}

type localTestConn struct {
	net.Conn
	local  net.Addr
	closed bool
}

func (c *localTestConn) LocalAddr() net.Addr {
	if c.local != nil {
		return c.local
	}
	return c.Conn.LocalAddr()
}
func (c *localTestConn) Close() error {
	c.closed = true
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func TestPhysicalPostConnectCheckClosesWrongSourceOrChangedRoute(t *testing.T) {
	r := routeFixture(1, "0.0.0.0/0", 1).Route
	for _, wrongSource := range []bool{false, true} {
		ip := r.LocalAddress
		if wrongSource {
			ip = netip.MustParseAddr("198.18.0.1")
		}
		c := &localTestConn{local: &net.TCPAddr{IP: ip.AsSlice(), Port: 12345}}
		err := verifyPhysicalConnection(context.Background(), c, r, func(context.Context, PhysicalRoute) error { return errors.New("route changed") })
		if err == nil || !c.closed {
			t.Fatal("unsafe connected socket retained")
		}
	}
}

func TestPhysicalDialControlVerifiesBeforeBindingAndNeverRetriesUnbound(t *testing.T) {
	for _, transport := range []string{"tcp", "udp"} {
		r := routeFixture(1, "0.0.0.0/0", 1).Route
		endpoint := netip.AddrPortFrom(r.Destination, 53)
		var verifyErr, bindErr error
		var events []string
		d, network, err := boundPhysicalDialer(transport, endpoint, r, func(context.Context, PhysicalRoute) error { events = append(events, "verify"); return verifyErr }, func(syscall.RawConn, PhysicalRoute) error { events = append(events, "bind"); return bindErr })
		if err != nil || network != transport+"4" || d.LocalAddr.String() != "192.168.1.20:0" {
			t.Fatal("source/family not pinned", d, network, err)
		}
		if err := d.ControlContext(context.Background(), network, endpoint.String(), nil); err != nil || len(events) != 2 || events[0] != "verify" || events[1] != "bind" {
			t.Fatal("binding sequence", events, err)
		}
		events = nil
		verifyErr = errors.New("route changed")
		if !errors.Is(d.ControlContext(context.Background(), network, endpoint.String(), nil), verifyErr) || len(events) != 1 {
			t.Fatal("bind after failed verification")
		}
		events = nil
		verifyErr = nil
		bindErr = errors.New("setsockopt failed")
		if !errors.Is(d.ControlContext(context.Background(), network, endpoint.String(), nil), bindErr) || len(events) != 2 {
			t.Fatal("socket binding failure ignored")
		}
		events = nil
		if d.ControlContext(context.Background(), network, "8.8.8.8:53", nil) == nil || len(events) != 0 {
			t.Fatal("endpoint changed before socket connect")
		}
		if d.ControlContext(context.Background(), transport+"6", endpoint.String(), nil) == nil || len(events) != 0 {
			t.Fatal("family changed before socket connect")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(d.ControlContext(ctx, network, endpoint.String(), nil), context.Canceled) || len(events) != 0 {
			t.Fatal("socket binding after cancellation")
		}
	}
}

func TestPhysicalLiteralResolutionDoesNotNeedAnyRouteOrDNSPolicy(t *testing.T) {
	ip, err := ResolvePhysical(context.Background(), "8.8.8.8", Policy{}, nil)
	if err != nil || ip.String() != "8.8.8.8" {
		t.Fatal(ip, err)
	}
}
