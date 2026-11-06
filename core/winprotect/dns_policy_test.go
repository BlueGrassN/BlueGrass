package winprotect

import (
	"net/netip"
	"slices"
	"testing"
)

func TestDNSPolicyAuthorizesOnlyExactTransportAndService(t *testing.T) {
	for _, transport := range []string{"tcp", "udp"} {
		t.Run(transport, func(t *testing.T) {
			m, s, fw := testManager(t)
			endpoint := netip.MustParseAddrPort("9.9.9.9:53")
			dns := []DNSEndpoint{{Endpoint: endpoint, Transport: transport}}
			if err := m.ArmDNSWithPolicy(testSID, "a", "r", dns, false); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*s.events, []string{"save", "replace"}) || m.AllowTunnel(42) == nil {
				t.Fatal("typed bootstrap lost durable ordering or proof gate")
			}
			for _, full := range []bool{false, true} {
				if full {
					if err := m.ArmWithDNSPolicy(testSID, "a", "r", testEntry, dns, false); err != nil {
						t.Fatal(err)
					}
				}
				for _, app := range []string{"service.exe", "other.exe"} {
					for _, proto := range []uint8{6, 17} {
						want := app == "service.exe" && (transport == "tcp" && proto == 6 || transport == "udp" && proto == 17)
						p := packet{addr: endpoint.Addr().String(), remote: 53, protocol: proto, app: app}
						if permits(fw.rules, p) != want {
							t.Fatalf("full=%v app=%s proto=%d want=%v", full, app, proto, want)
						}
						p.remote = 5353
						if permits(fw.rules, p) {
							t.Fatal("DNS endpoint authorized another port")
						}
					}
				}
			}
		})
	}
}

func TestDNSPolicyInvalidTransportHasNoSideEffects(t *testing.T) {
	for _, transport := range []string{"", "TCP", "tls", "all"} {
		m, s, fw := testManager(t)
		dns := []DNSEndpoint{{Endpoint: netip.MustParseAddrPort("9.9.9.9:53"), Transport: transport}}
		if m.ArmDNSWithPolicy(testSID, "a", "r", dns, false) == nil || m.ArmWithDNSPolicy(testSID, "a", "r", testEntry, dns, false) == nil {
			t.Fatal("accepted invalid DNS transport", transport)
		}
		if len(*s.events) != 0 || len(fw.rules) != 0 || m.Active() {
			t.Fatal("invalid policy mutated protection")
		}
	}
}

func TestLegacyDNSAPIKeepsBothTransports(t *testing.T) {
	m, _, fw := testManager(t)
	dns := []netip.AddrPort{netip.MustParseAddrPort("9.9.9.9:53")}
	if err := m.ArmDNS(testSID, "a", "r", dns, false); err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{false, true} {
		if full {
			if err := m.Arm(testSID, "a", "r", testEntry, dns, false); err != nil {
				t.Fatal(err)
			}
		}
		for _, proto := range []uint8{6, 17} {
			if !permits(fw.rules, packet{addr: "9.9.9.9", remote: 53, protocol: proto, app: "service.exe"}) {
				t.Fatal("legacy endpoint lost its explicit dual transport permission")
			}
		}
	}
}
