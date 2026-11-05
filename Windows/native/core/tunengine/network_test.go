package tunengine

import (
	"net/netip"
	"testing"
)

func networkFixture() ([]systemAdapter, []systemRoute) {
	return []systemAdapter{
		{Name: "en0", LUID: 1, Index: 3, Up: true, Metric4: 20, Metric6: 30,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.168.1.50/24")}, DNS: []netip.Addr{netip.MustParseAddr("192.168.1.1")}},
	}, []systemRoute{
		{LUID: 1, Prefix: netip.MustParsePrefix("0.0.0.0/0"), NextHop: netip.MustParseAddr("192.168.1.1"), Metric: 5},
		{LUID: 1, Prefix: netip.MustParsePrefix("192.168.1.0/24"), NextHop: netip.MustParseAddr("0.0.0.0"), Metric: 256},
	}
}

// Catches fingerprints that invalidate their own proof when BlueGrass adds an
// adapter, while missing real NIC metric/default gateway/resolver changes.
func TestSystemSnapshotFingerprintExcludesOwnTUNButTracksRealNetwork(t *testing.T) {
	a, r := networkFixture()
	before := systemSnapshot(a, r)
	a = append(a, systemAdapter{Name: InterfaceName, LUID: 99, Up: true, Addresses: []netip.Prefix{netip.MustParsePrefix("172.31.255.1/30")}})
	r = append(r, systemRoute{LUID: 99, Prefix: netip.MustParsePrefix("0.0.0.0/1")})
	if got := systemSnapshot(a, r); got.Generation != before.Generation {
		t.Fatal("own TUN invalidated proof")
	}
	a[0].Metric4++
	if got := systemSnapshot(a, r); got.Generation == before.Generation {
		t.Fatal("missed physical metric change")
	}
	a, r = networkFixture()
	r[0].NextHop = netip.MustParseAddr("192.168.1.254")
	if got := systemSnapshot(a, r); got.Generation == before.Generation {
		t.Fatal("missed default gateway change")
	}
	a, r = networkFixture()
	a[0].DNS = []netip.Addr{netip.MustParseAddr("192.168.1.2")}
	if got := systemSnapshot(a, r); got.Generation == before.Generation {
		t.Fatal("missed local resolver change")
	}
}

func TestSystemSnapshotDoesNotPromotePublicDNSOrPublicStaticRouteToLAN(t *testing.T) {
	a, r := networkFixture()
	a[0].DNS = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	r = append(r, systemRoute{LUID: 1, Prefix: netip.MustParsePrefix("8.8.8.8/32"), NextHop: netip.MustParseAddr("192.168.1.1")})
	n := systemSnapshot(a, r)
	if len(n.DNS) != 0 {
		t.Fatal("public DNS promoted to local resolver")
	}
	if len(n.EntryDNS) != 1 || n.EntryDNS[0].String() != "1.1.1.1:53" {
		t.Fatal("lost physical bootstrap DNS candidates")
	}
	for _, route := range n.Routes {
		if route.Prefix.String() == "8.8.8.8/32" || route.Prefix.Bits() == 0 {
			t.Fatal("public/default route became LAN bypass")
		}
	}
}
