package tunengine

import (
	"encoding/binary"
	"net/netip"
	"testing"

	tun "github.com/xtls/xray-core/proxy/tun"
	"google.golang.org/protobuf/proto"
)

func TestDarwinACLBufferRejectsACLAndMalformedSecurity(t *testing.T) {
	noACL := make([]byte, 12)
	binary.LittleEndian.PutUint32(noACL, 12)
	binary.LittleEndian.PutUint32(noACL[4:], 8)
	if !darwinNoACLBuffer(noACL) {
		t.Fatal("missing ACL rejected")
	}
	sentinel := make([]byte, 56)
	binary.LittleEndian.PutUint32(sentinel, 56)
	binary.LittleEndian.PutUint32(sentinel[4:], 8)
	binary.LittleEndian.PutUint32(sentinel[8:], 44)
	binary.LittleEndian.PutUint32(sentinel[12:], 0x012cc16d)
	binary.LittleEndian.PutUint32(sentinel[48:], 0xffffffff)
	if !darwinNoACLBuffer(sentinel) {
		t.Fatal("Apple NOACL sentinel rejected")
	}
	for _, change := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b[48:], 0) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[48:], 1) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0xfffffffc) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[8:], 4096) },
		func(b []byte) { b[12] = 0 },
		func(b []byte) { b[52] = 1 },
	} {
		bad := append([]byte(nil), sentinel...)
		change(bad)
		if darwinNoACLBuffer(bad) {
			t.Fatal("unsafe security blob accepted", bad)
		}
	}
	for _, bad := range [][]byte{nil, {0}, sentinel[:20]} {
		if darwinNoACLBuffer(bad) {
			t.Fatal("truncated ACL accepted")
		}
	}
}

// Catches upstream retaining caller-owned route/DNS policy or shared slices.
func TestDarwinOptionsCannotMutateHostPolicy(t *testing.T) {
	in := &tun.Config{Name: "utun233", MTU: 1420, Gateway: []string{"172.31.255.1/30"}, DNS: []string{"172.31.255.1"}, AutoSystemRoutingTable: []string{"0.0.0.0/0"}, AutoSystemDnsToGateway: true, AutoOutboundsInterface: "auto", AutoSystemWfpBlockLeak: []string{"all"}}
	before := proto.Clone(in)
	out := darwinOwnedOptions(in)
	if !proto.Equal(in, before) {
		t.Fatal("caller configuration mutated")
	}
	if out.Name != "utun233" || out.MTU != 1420 || len(out.Gateway) != 1 || out.Gateway[0] != "172.31.255.1/30" {
		t.Fatal("lost adapter addressing")
	}
	if len(out.AutoSystemRoutingTable) != 0 || len(out.DNS) != 0 || out.AutoSystemDnsToGateway || out.AutoOutboundsInterface != "" || len(out.AutoSystemWfpBlockLeak) != 0 {
		t.Fatal("upstream can change host policy")
	}
	out.Gateway[0] = "10.0.0.1/30"
	if !proto.Equal(in, before) {
		t.Fatal("clone aliases caller gateway")
	}
}

// Catches DNS substitution after hashing, nil-vs-empty overrides and aliasing.
func TestDarwinOriginalDNSSubstitutionPrecedesFingerprint(t *testing.T) {
	a, r := networkFixture()
	before := systemSnapshot(a, r)
	original := append([]netip.Addr(nil), a[0].DNS...)
	a[0].DNS = []netip.Addr{netip.MustParseAddr("172.31.255.1")}
	overrides := map[string][]netip.Addr{a[0].Name: original}
	restored := darwinOriginalDNS(a, overrides)
	if got := systemSnapshot(restored, r); got.Generation != before.Generation || got.EntryDNS[0].String() != "192.168.1.1:53" {
		t.Fatal("owned DNS invalidates generation or bootstrap resolver")
	}
	if systemSnapshot(darwinOriginalDNS(a, nil), r).Generation == before.Generation {
		t.Fatal("unowned resolver change hidden")
	}
	restored[0].DNS[0] = netip.MustParseAddr("1.1.1.1")
	if original[0].String() != "192.168.1.1" || a[0].DNS[0].String() != "172.31.255.1" {
		t.Fatal("mutated inputs")
	}
	if len(darwinOriginalDNS(a, map[string][]netip.Addr{a[0].Name: nil})[0].DNS) != 0 {
		t.Fatal("empty original DNS ignored")
	}
	a[0].Addresses = []netip.Prefix{netip.MustParsePrefix("10.2.3.4/24")}
	if systemSnapshot(darwinOriginalDNS(a, overrides), r).Generation == before.Generation {
		t.Fatal("real topology hidden")
	}
}

func TestDarwinOwnedPhysicalPinDoesNotChangeGeneration(t *testing.T) {
	a, r := networkFixture()
	before := systemSnapshot(a, r)
	pin := systemRoute{LUID: 1, Prefix: netip.MustParsePrefix("8.8.8.8/32"), NextHop: netip.MustParseAddr("192.168.1.1"), Metric: 7}
	ignored := []OwnedPhysicalRoute{{Prefix: netip.MustParsePrefix("8.8.8.8/32"), Interface: "en0", Gateway: netip.MustParseAddr("192.168.1.1"), Metric: 7}}
	r = append(r, pin)
	filtered := darwinWithoutOwnedRoutes(a, r, ignored)
	if got := systemSnapshot(a, filtered); got.Generation != before.Generation {
		t.Fatal("owned pin invalidates network generation")
	}
	if len(filtered) != 2 || len(r) != 3 || r[2] != pin {
		t.Fatal("filter mutated input or removed unrelated routes")
	}
	if systemSnapshot(a, darwinWithoutOwnedRoutes(a, r, nil)).Generation == before.Generation {
		t.Fatal("nil ignored routes hid topology change")
	}
	for _, tc := range []struct {
		name   string
		change func(*systemRoute)
	}{
		{"gateway", func(r *systemRoute) { r.NextHop = netip.MustParseAddr("192.168.1.254") }},
		{"metric", func(r *systemRoute) { r.Metric = 8 }},
		{"interface", func(r *systemRoute) { r.LUID = 2 }},
		{"prefix", func(r *systemRoute) { r.Prefix = netip.MustParsePrefix("8.8.4.4/32") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapters := append(append([]systemAdapter(nil), a...), systemAdapter{Name: "en1", LUID: 2, Up: true})
			base := systemSnapshot(adapters, r[:2])
			changed := pin
			tc.change(&changed)
			routes := append(append([]systemRoute(nil), r[:2]...), changed)
			if systemSnapshot(adapters, darwinWithoutOwnedRoutes(adapters, routes, ignored)).Generation == base.Generation {
				t.Fatal("unowned or externally changed route hidden")
			}
		})
	}
	// Another writer can add the same prefix alongside our pin. Only the
	// exact pin disappears; the other gateway still changes generation.
	external := pin
	external.NextHop = netip.MustParseAddr("192.168.1.254")
	if systemSnapshot(a, darwinWithoutOwnedRoutes(a, append(r, external), ignored)).Generation == before.Generation {
		t.Fatal("same-prefix external route hidden")
	}
	// A matching journal entry must not exempt a VPN route from the hash.
	vpn := []systemAdapter{{Name: "utun0", LUID: 1, Up: true}}
	vpnIgnored := []OwnedPhysicalRoute{{Prefix: pin.Prefix, Interface: "utun0", Gateway: pin.NextHop, Metric: 7}}
	if got := darwinWithoutOwnedRoutes(vpn, []systemRoute{pin}, vpnIgnored); len(got) != 1 {
		t.Fatal("nonphysical route ignored")
	}
}

func TestDarwinPhysicalPolicyRejectsVirtualUnderlays(t *testing.T) {
	for _, name := range []string{"utun0", "utun233", "tun0", "tap2", "ppp0", "ipsec0", "gif0", "stf0", "lo0", "awdl0", "llw0"} {
		if darwinPhysicalName(name) {
			t.Errorf("accepted virtual underlay %s", name)
		}
	}
	for _, name := range []string{"en0", "en12", "bridge0", "bond0", "vlan2"} {
		if !darwinPhysicalName(name) {
			t.Errorf("rejected physical link %s", name)
		}
	}
}

func TestDarwinDNSParserKeepsInterfaceScopeAndDefault(t *testing.T) {
	raw := `DNS configuration
resolver #1
  nameserver[0] : 192.168.1.1
  nameserver[1] : fe80::1%en0
resolver #2
  domain : corp.example
  nameserver[0] : 10.0.0.53
  if_index : 9 (utun0)
DNS configuration (for scoped queries)
resolver #1
  nameserver[0] : 10.1.0.53
  if_index : 5 (en1)
`
	got, err := parseDarwinDNS(raw, "en0")
	if err != nil {
		t.Fatal(err)
	}
	if len(got["en0"]) != 2 || got["en0"][1].String() != "fe80::1%en0" || len(got["en1"]) != 1 || got["en1"][0].String() != "10.1.0.53" || len(got["utun0"]) != 0 {
		t.Fatal("resolver scope lost", got)
	}
	if _, err = parseDarwinDNS("resolver #1\n nameserver[0] : invalid\n", "en0"); err == nil {
		t.Fatal("malformed resolver accepted")
	}
}

func TestDarwinSupplementalDNSDoesNotBecomeEntryResolver(t *testing.T) {
	got, err := parseDarwinDNS("resolver #1\n domain : private.example\n nameserver[0] : 10.0.0.53\n if_index : 4 (en0)\n", "en0")
	if err != nil {
		t.Fatal(err)
	}
	if len(got["en0"]) != 0 {
		t.Fatal("supplemental resolver promoted to default DNS", got)
	}
}
