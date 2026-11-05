package tunengine

import (
	"context"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

func policyOptions(t *testing.T, allow bool) nativeConfig {
	t.Helper()
	c := testConfig()
	c.AllowLAN = &allow
	n, _ := c.Network(context.Background())
	n.Routes = append(n.Routes, LocalRoute{Prefix: netip.MustParsePrefix("fd00:1234::/64"), Interface: "Ethernet", Connected: true})
	n.DNS = []LocalDNS{{Server: netip.MustParseAddrPort("192.168.1.1:53"), Interface: "Ethernet", Domains: []string{"lan", "home.arpa"}}}
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeOptions(p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Exercise the production native classifier, not a test-only rule approximation.
func routeAction(t *testing.T, o nativeConfig, address string, port uint16) string {
	t.Helper()
	return o.route(inboundContext{Network: "tcp", Destination: M.SocksaddrFrom(netip.MustParseAddr(address), port)})
}
func dnsAction(t *testing.T, o nativeConfig, name string, qtype uint16) string {
	t.Helper()
	r := o.dnsRule(name, qtype)
	if r.Server != "" {
		return r.Server
	}
	return r.Rcode
}

func TestPinnedRouterPolicyDecisions(t *testing.T) {
	o := policyOptions(t, true)
	for _, tc := range []struct {
		ip   string
		port uint16
		want string
	}{
		{"192.168.1.1", 53, "hijack-dns"}, {"192.168.1.10", 443, "lan-1"},
		{"fd00:1234::2", 443, "lan-0"}, {"2606:4700::1111", 443, "reject"},
		{"8.8.4.4", 443, "selected"}, {"10.2.3.4", 443, "reject"},
		{"198.19.0.1", 443, "reject"}, {"ff0e::123", 443, "reject"},
	} {
		if got := routeAction(t, o, tc.ip, tc.port); got != tc.want {
			t.Errorf("%s:%d got %s want %s", tc.ip, tc.port, got, tc.want)
		}
	}
	if got := routeAction(t, policyOptions(t, false), "192.168.1.10", 443); got != "reject" {
		t.Fatalf("LAN off: %s", got)
	}
}

func TestInstalledUpdaterControlRouteIsExactTCP443AfterDNSAndFakeIP(t *testing.T) {
	c := testConfig()
	c.UIExecutable = `D:\Apps\BlueGrass\bluegrass.exe`
	n, _ := c.Network(context.Background())
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeOptions(p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, network, ip string
		port              uint16
		want              string
	}{
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "8.8.4.4", 443, "control"},
		{`D:\Apps\BlueGrass\bluegrass.exe`, "tcp", "8.8.4.4", 443, "control"},
		{`D:\Other\BlueGrass.Update.exe`, "tcp", "8.8.4.4", 443, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Service.exe`, "tcp", "8.8.4.4", 443, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe.old`, "tcp", "8.8.4.4", 443, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "udp", "8.8.4.4", 443, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "8.8.4.4", 80, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "8.8.4.4", 8443, "selected"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "192.168.1.1", 53, "hijack-dns"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "198.18.0.2", 443, "reject"},
		{`D:\Apps\BlueGrass\BlueGrass.Update.exe`, "tcp", "2606:4700::1111", 443, "reject"},
	} {
		got := o.route(inboundContext{Network: tc.network, Destination: M.SocksaddrFrom(netip.MustParseAddr(tc.ip), tc.port), ProcessPath: tc.path})
		if got != tc.want {
			t.Errorf("%s %s %s:%d: got %s want %s", tc.path, tc.network, tc.ip, tc.port, got, tc.want)
		}
	}
}

func TestPinnedDNSPolicyLocalBoundariesAndNoPublicFallback(t *testing.T) {
	o := policyOptions(t, true)
	for _, tc := range []struct {
		name string
		qt   uint16
		want string
	}{
		{"NAS.LAN.", dns.TypeA, "local-dns-0"}, {"router.home.arpa", dns.TypeAAAA, "local-dns-0"},
		{"_service._tcp.nas.lan", dns.TypeSRV, "local-dns-0"}, {"printer.local", dns.TypeTXT, "SERVFAIL"},
		{"nas.lan.example.com", dns.TypeA, "fakeip"}, {"evilhome.arpa", dns.TypeTXT, "public"},
		{"example.com", dns.TypeAAAA, "NOERROR"}, {"example.com", dns.TypeTXT, "public"},
		{"1.1.168.192.in-addr.arpa", dns.TypePTR, "local-dns-0"},
		{"4.3.2.10.in-addr.arpa", dns.TypePTR, "SERVFAIL"},
	} {
		if got := dnsAction(t, o, tc.name, tc.qt); got != tc.want {
			t.Errorf("%s got %s want %s", tc.name, got, tc.want)
		}
	}
	if got := dnsAction(t, policyOptions(t, false), "nas.lan", dns.TypeA); got != "SERVFAIL" {
		t.Fatalf("LAN-off DNS: %s", got)
	}
}

func TestPinnedXrayConstructsWithoutStartingNativeInterface(t *testing.T) {
	c := testConfig()
	var authorized netip.AddrPort
	c.AllowUDPRelay = func(endpoint netip.AddrPort) error { authorized = endpoint; return nil }
	n, _ := c.Network(context.Background())
	profile := testProfile()
	profile.Auth.Username, profile.Auth.Password = "fixture-user", "fixture-password"
	p, err := prepareConfig(profile, c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	g := newSessionGate()
	r, err := newRuntime(context.Background(), p, g)
	if err != nil {
		t.Fatal(err)
	}
	if r.InterfaceLUID() != 0 {
		t.Fatal("construction opened TUN")
	}
	out, found := r.(*xrayRuntime).socks, r.(*xrayRuntime).socks != nil
	h, guarded := out, out != nil
	if !found || !guarded {
		t.Fatal("real Xray registry did not select guarded SOCKS outbound")
	}
	// Start the shared Xray instance, replacing only its final physical sockets
	// with net.Pipe. No native adapter or route is opened by this fixture.
	f := newRelayFixture(t, goodRelayReply())
	h.base = f
	if err := r.(*xrayRuntime).selected.start(""); err != nil {
		t.Fatal(err)
	}
	g.open()
	conn, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
	if err != nil {
		t.Fatal(err)
	}
	if authorized != netip.MustParseAddrPort("8.8.8.8:42000") || f.udpDials != 1 {
		t.Fatal("Config callback lost in real Xray construction")
	}
	_ = conn.Close()
	g.retire()
	assertAssociationClosed(t, f)
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
}
