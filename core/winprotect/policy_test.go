package winprotect

import (
	"net/netip"
	"testing"
)

// Evaluate compiled rule predicates independently of the native syscall encoder.
type packet struct {
	addr              string
	remote, local     uint16
	protocol          uint8
	app               string
	luid              uint64
	loopback, inbound bool
}

func permits(rules []rule, p packet) bool {
	a := netip.MustParseAddr(p.addr)
	best := -1
	allow := false
	for _, r := range rules {
		if r.Boot || r.IPv6 != a.Is6() || r.Inbound != p.inbound {
			continue
		}
		if r.App != "" && r.App != p.app || r.LUID != 0 && r.LUID != p.luid || r.Protocol != 0 && r.Protocol != p.protocol {
			continue
		}
		if r.Loopback && !p.loopback || r.Remote.IsValid() && !r.Remote.Contains(a) {
			continue
		}
		if r.RemotePort != 0 && r.RemotePort != p.remote || r.LocalPort != 0 && r.LocalPort != p.local {
			continue
		}
		if r.NoDNS && (p.remote == 53 || p.local == 53) {
			continue
		}
		if int(r.Weight) > best {
			best = int(r.Weight)
			allow = r.Permit
		}
	}
	return allow
}

func TestPolicyLeastPrivilegeAndVirtualIsolation(t *testing.T) {
	rules := compilePolicy(policy{Service: "service", UI: "ui", Entry: testEntry,
		EntryPolicy: EntryPolicy{TCP: true},
		DNS:         dualDNS([]netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:53")}), AllowLAN: true,
		Links: []link{{LUID: 7, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:123::/64")}}}, Virtual: defaultVirtualPrefixes()})
	for _, tc := range []struct {
		name string
		p    packet
		want bool
	}{
		{"internet default", packet{addr: "8.8.8.8", remote: 443, protocol: 6, luid: 7}, false},
		{"public ipv6", packet{addr: "2001:4860::8888", remote: 443, protocol: 6, luid: 7}, false},
		{"service exact entry", packet{addr: "203.0.113.9", remote: 8443, protocol: 6, app: "service", luid: 7}, true},
		{"entry other process", packet{addr: "203.0.113.9", remote: 8443, protocol: 6, app: "ui", luid: 7}, false},
		{"entry other port", packet{addr: "203.0.113.9", remote: 8444, protocol: 6, app: "service", luid: 7}, false},
		{"GUI control", packet{addr: "8.8.8.8", remote: 443, protocol: 6, app: "ui", luid: 7}, true},
		{"GUI UDP not control", packet{addr: "8.8.8.8", remote: 443, protocol: 17, app: "ui", luid: 7}, false},
		{"LAN TCP", packet{addr: "10.0.0.2", remote: 80, protocol: 6, luid: 7}, true},
		{"LAN wrong interface", packet{addr: "10.0.0.2", remote: 80, protocol: 6, luid: 8}, false},
		{"on-link IPv6", packet{addr: "2001:db8:123::42", remote: 80, protocol: 6, luid: 7}, true},
		{"LAN DNS bypass", packet{addr: "192.168.1.1", remote: 53, protocol: 17, luid: 7}, false},
		{"controlled DNS", packet{addr: "192.168.1.1", remote: 53, protocol: 17, luid: 7, app: "service"}, true},
		{"other DNS", packet{addr: "192.168.1.2", remote: 53, protocol: 6, luid: 7, app: "service"}, false},
		{"fake ULA", packet{addr: "fc00::99", remote: 80, protocol: 6, luid: 7}, false},
		{"real ULA", packet{addr: "fd01::99", remote: 80, protocol: 6, luid: 7}, true},
		{"fake IPv4", packet{addr: "198.18.0.10", remote: 443, protocol: 6, luid: 7, app: "ui"}, false},
		{"TUN DNS", packet{addr: "172.19.0.2", remote: 80, protocol: 6, luid: 7}, false},
		{"TUN pool A", packet{addr: "172.31.255.1", remote: 80, protocol: 6, luid: 7}, false},
		{"TUN pool B", packet{addr: "172.30.255.2", remote: 80, protocol: 6, luid: 7}, false},
		{"TUN pool C", packet{addr: "10.255.255.2", remote: 80, protocol: 6, luid: 7}, false},
		{"TUN IPv6 pool B", packet{addr: "fdde:adbe:ef01::2", remote: 80, protocol: 6, luid: 7}, false},
		{"DHCP", packet{addr: "255.255.255.255", remote: 67, local: 68, protocol: 17, luid: 7}, true},
		{"loopback", packet{addr: "127.0.0.1", remote: 53, protocol: 17, loopback: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := permits(rules, tc.p); got != tc.want {
				t.Fatalf("permit=%v want %v", got, tc.want)
			}
		})
	}
}

func TestBootAndPersistentBlockEveryFamilyAndDirection(t *testing.T) {
	rules := compilePolicy(policy{Service: "service", UI: "ui"})
	for _, boot := range []bool{false, true} {
		for _, v6 := range []bool{false, true} {
			for _, inbound := range []bool{false, true} {
				found := false
				for _, r := range rules {
					if r.Boot == boot && r.IPv6 == v6 && r.Inbound == inbound && r.Kind == "block" && !r.Permit {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing block: boot=%v ipv6=%v inbound=%v", boot, v6, inbound)
				}
			}
		}
	}
	for _, r := range rules {
		if r.Boot && r.Permit {
			t.Fatal("boot-time app identity cannot be assumed available")
		}
	}
}

func TestTunnelIsExactAndLANOff(t *testing.T) {
	rules := compilePolicy(policy{Service: "service", UI: "ui", Tunnel: 42, Virtual: defaultVirtualPrefixes()})
	if !permits(rules, packet{addr: "198.18.0.5", remote: 53, protocol: 17, luid: 42}) {
		t.Fatal("verified TUN blocked")
	}
	if permits(rules, packet{addr: "8.8.8.8", remote: 80, protocol: 6, luid: 7}) || permits(rules, packet{addr: "192.168.1.1", remote: 80, protocol: 6, luid: 7}) {
		t.Fatal("broad permit")
	}
	for _, r := range rules {
		if r.Kind == "tunnel" && (r.Persistent || r.Boot || r.LUID != 42) {
			t.Fatal("tunnel persisted or not exact")
		}
	}
}
