package tunengine

import (
	"context"
	"net/netip"
	"testing"
)

// Catches DNS accidentally placed behind LAN bypass, IPv6 default proxying,
// global private-address shortcuts, or an unbound local outbound.
func TestConfigUsesDNSBeforeLANAndRejectsPublicIPv6(t *testing.T) {
	c := testConfig()
	n, _ := c.Network(context.Background())
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeOptions(p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	rules := o.Route.Rules
	if rules[0].Action != "hijack-dns" || len(rules[0].Port) != 1 || rules[0].Port[0] != 53 {
		t.Fatal("all application DNS must be intercepted before LAN")
	}
	lan, ipv6 := -1, -1
	for i, r := range rules {
		d := r
		if d.Outbound == "lan-0" {
			lan = i
		}
		if d.IPVersion == 6 && d.Action == "reject" {
			ipv6 = i
		}
	}
	if lan < 1 || ipv6 <= lan || o.Route.Final != "selected" || !o.Route.AutoDetectInterface {
		t.Fatal("unsafe routing order")
	}
}

func TestLANOffAndVirtualConflictsFailClosed(t *testing.T) {
	c := testConfig()
	off := false
	c.AllowLAN = &off
	n, _ := c.Network(context.Background())
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeOptions(p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range o.Route.Rules {
		if r.Outbound == "lan-0" {
			t.Fatal("LAN off still bypasses")
		}
	}
	n.Routes = append(n.Routes, LocalRoute{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Interface: "Ethernet", Connected: true})
	if _, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8")); err == nil {
		t.Fatal("accepted default route as LAN")
	}
}

func TestLocalNameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"NAS.LAN.", true}, {"router.home.arpa", true}, {"printer.local", true},
		{"x.localhost", true}, {"nas.lan.example.com", false}, {"evilhome.arpa", false}, {"example.com", false},
	} {
		if got := localName(tc.name); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestAmbiguousLinkLocalCannotSelectInterfaceByMetric(t *testing.T) {
	c := testConfig()
	n, _ := c.Network(context.Background())
	n.Routes = append(n.Routes,
		LocalRoute{Prefix: netip.MustParsePrefix("fe80::/64"), Interface: "Ethernet", Metric: 1, Connected: true},
		LocalRoute{Prefix: netip.MustParsePrefix("fe80::/64"), Interface: "Wi-Fi", Metric: 2, Connected: true})
	if _, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8")); err == nil {
		t.Fatal("guessed link-local scope from metric")
	}
}

func TestStaleFakeIPPoolCannotBecomeLANWhenPoolChanges(t *testing.T) {
	c := testConfig()
	n, _ := c.Network(context.Background())
	n.Routes = append(n.Routes, LocalRoute{Prefix: netip.MustParsePrefix("198.18.0.0/24"), Interface: "Ethernet", Connected: true})
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeOptions(p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	if got := routeAction(t, o, "198.18.0.10", 443); got != "reject" {
		t.Fatal("stale FakeIP was sent to LAN:", got)
	}
}
