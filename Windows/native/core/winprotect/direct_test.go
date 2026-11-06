package winprotect

import (
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"
)

type directProtection interface {
	AllowDirect(string, netip.AddrPort) error
	RemoveDirect(string, netip.AddrPort) error
}

func directManager(t *testing.T, m *Manager) directProtection {
	t.Helper()
	d, ok := any(m).(directProtection)
	if !ok {
		t.Fatal("precise direct egress grants are missing")
	}
	return d
}

func startDirectTunnel(t *testing.T, m *Manager) directProtection {
	t.Helper()
	d := directManager(t, m)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	return d
}

func directPacket(network string, endpoint netip.AddrPort) packet {
	proto := uint8(6)
	if network == "udp" {
		proto = 17
	}
	return packet{addr: endpoint.Addr().String(), remote: endpoint.Port(), protocol: proto, app: "service.exe", luid: 7}
}

func TestDirectRequiresArmedLiveTunnel(t *testing.T) {
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	m, _, f := testManager(t)
	d := directManager(t, m)
	if err := d.AllowDirect("tcp", ep); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("inactive protection authorized direct egress", err)
	}
	if err := m.ArmDNS(testSID, "account-1", "resource-1", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := d.AllowDirect("tcp", ep); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("DNS bootstrap authorized direct egress", err)
	}
	arm(t, m)
	if err := d.AllowDirect("tcp", ep); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("armed session without tunnel authorized direct egress", err)
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"inactive", "unarmed", "recovery", "local-only", "missing service"} {
		t.Run(state, func(t *testing.T) {
			active, armed, recovery, local, service := m.active, m.armed, m.state.Recovery, m.policy.LocalOnly, m.policy.Service
			switch state {
			case "inactive":
				m.active = false
			case "unarmed":
				m.armed = false
			case "recovery":
				m.state.Recovery = true
			case "local-only":
				m.policy.LocalOnly = true
			case "missing service":
				m.policy.Service = ""
			}
			transactions := len(*f.events)
			if d.AllowDirect("tcp", ep) == nil || len(*f.events) != transactions {
				t.Fatal("unauthorized state changed direct policy")
			}
			m.active, m.armed, m.state.Recovery, m.policy.LocalOnly, m.policy.Service = active, armed, recovery, local, service
		})
	}
	if err := d.AllowDirect("tcp", ep); err != nil {
		t.Fatal(err)
	}
	gone := errors.New("tunnel disappeared")
	m.verifyTunnel = func(luid uint64) error {
		if luid != 42 {
			t.Errorf("verified wrong TUN: %d", luid)
		}
		return gone
	}
	for _, network := range []string{"tcp", "udp"} {
		if err := d.AllowDirect(network, ep); !errors.Is(err, gone) {
			t.Fatal("new or shared grant did not verify live TUN", err)
		}
	}
	if err := d.RemoveDirect("tcp", ep); err != nil || permits(f.rules, directPacket("tcp", ep)) {
		t.Fatal("failed verification acquired a reference or prevented cleanup", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.AllowDirect("tcp", ep); !errors.Is(err, ErrClosed) {
		t.Fatal("closed manager allowed a grant", err)
	}
	if err := d.RemoveDirect("tcp", ep); !errors.Is(err, ErrClosed) {
		t.Fatal("closed manager allowed a removal", err)
	}
}

func TestDirectRejectsInvalidNetworksAndEndpoints(t *testing.T) {
	m, _, f := testManager(t)
	if err := m.SetVirtualPrefixes(prefixes("8.8.4.0/24")); err != nil {
		t.Fatal(err)
	}
	d := startDirectTunnel(t, m)
	transactions := len(*f.events)
	for _, network := range []string{"", "TCP", "UDP", "tcp4", "udp4", "tcp6", "udp6", "icmp", " tcp", "udp "} {
		if d.AllowDirect(network, netip.MustParseAddrPort("8.8.8.8:50001")) == nil || d.RemoveDirect(network, netip.MustParseAddrPort("8.8.8.8:50001")) == nil {
			t.Errorf("accepted network %q", network)
		}
	}
	for _, addr := range []string{"0.0.0.0", "0.1.2.3", "127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "100.127.255.254", "169.254.1.1", "192.0.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "198.18.0.1", "198.19.255.254", "224.0.0.1", "240.0.0.1", "255.255.255.255", "8.8.4.4", "::", "::1", "2001:4860::8888", "::ffff:8.8.8.8", "fe80::1%7"} {
		ep := netip.AddrPortFrom(netip.MustParseAddr(addr), 50001)
		for _, network := range []string{"tcp", "udp"} {
			if d.AllowDirect(network, ep) == nil || d.RemoveDirect(network, ep) == nil {
				t.Errorf("accepted %s endpoint %v", network, ep)
			}
		}
	}
	for _, ep := range []netip.AddrPort{{}, netip.MustParseAddrPort("8.8.8.8:0")} {
		if d.AllowDirect("tcp", ep) == nil || d.RemoveDirect("tcp", ep) == nil {
			t.Errorf("accepted invalid/zero-port endpoint %v", ep)
		}
	}
	if len(*f.events) != transactions {
		t.Fatal("invalid request mutated firewall or journal")
	}
}

func TestDirectRulesAreExactAndReferencesSeparateFromRelays(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			m, s, f := testManager(t)
			d := startDirectTunnel(t, m)
			ep := netip.MustParseAddrPort("8.8.8.8:50001")
			saved := *s.record
			if err := d.AllowDirect(network, ep); err != nil {
				t.Fatal(err)
			}
			p := directPacket(network, ep)
			if !permits(f.rules, p) {
				t.Fatal("exact outbound service connection blocked")
			}
			for _, change := range []func(*packet){
				func(p *packet) { p.addr = "8.8.8.9" },
				func(p *packet) { p.remote++ },
				func(p *packet) { p.app = "other.exe" },
				func(p *packet) { p.app = "ui.exe" },
				func(p *packet) { p.protocol = 23 - p.protocol },
				func(p *packet) { p.protocol = 1 },
				func(p *packet) { p.inbound = true },
				func(p *packet) { p.addr = "2001:4860::8888" },
			} {
				other := p
				change(&other)
				if permits(f.rules, other) {
					t.Errorf("direct grant broadened: %+v", other)
				}
			}
			found := 0
			for _, r := range f.rules {
				if r.Kind == "direct" {
					found++
					if r.Persistent || r.Boot || r.Inbound || r.IPv6 || !r.Permit || r.App != "service.exe" || r.Protocol != p.protocol || r.Remote != netip.MustParsePrefix("8.8.8.8/32") || r.RemotePort != 50001 {
						t.Fatalf("direct grant persisted or broadened: %+v", r)
					}
				}
			}
			if found != 1 || *s.record != saved {
				t.Fatal("grant duplicated or persisted in journal")
			}
		})
	}
	m, _, f := testManager(t)
	d := startDirectTunnel(t, m)
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	for _, network := range []string{"tcp", "udp", "tcp"} {
		if err := d.AllowDirect(network, ep); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.AllowUDPRelay(ep); err != nil {
		t.Fatal(err)
	}
	transactions := len(*f.events)
	if err := d.RemoveDirect("tcp", ep); err != nil || len(*f.events) != transactions || !permits(f.rules, directPacket("tcp", ep)) {
		t.Fatal("shared TCP grant revoked too soon", err)
	}
	if err := d.RemoveDirect("tcp", ep); err != nil || permits(f.rules, directPacket("tcp", ep)) || !permits(f.rules, directPacket("udp", ep)) {
		t.Fatal("TCP removal crossed network boundary", err)
	}
	if err := d.RemoveDirect("udp", ep); err != nil || !permits(f.rules, directPacket("udp", ep)) {
		t.Fatal("direct removal revoked independent relay", err)
	}
	if err := d.AllowDirect("udp", ep); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveUDPRelay(ep); err != nil || !permits(f.rules, directPacket("udp", ep)) {
		t.Fatal("relay removal revoked independent direct grant", err)
	}
	if err := d.RemoveDirect("udp", ep); err != nil || permits(f.rules, directPacket("udp", ep)) {
		t.Fatal("last direct reference not removed", err)
	}
	transactions = len(*f.events)
	if err := d.RemoveDirect("udp", ep); err != nil || len(*f.events) != transactions {
		t.Fatal("absent grant removal changed policy", err)
	}
}

func TestDirectCapCountsActiveNetworkEndpointPairsAndReusesSlots(t *testing.T) {
	m, _, f := testManager(t)
	d := startDirectTunnel(t, m)
	ep := func(port uint16) netip.AddrPort { return netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), port) }
	for port := uint16(50000); port < 50260; port++ {
		if err := d.AllowDirect("tcp", ep(port)); err != nil {
			t.Fatal("closed connections exhausted cap", err)
		}
		if err := d.RemoveDirect("tcp", ep(port)); err != nil {
			t.Fatal(err)
		}
	}
	for port := uint16(51000); port < 51128; port++ {
		for _, network := range []string{"tcp", "udp"} {
			if err := d.AllowDirect(network, ep(port)); err != nil {
				t.Fatal(err)
			}
		}
	}
	transactions := len(*f.events)
	if err := d.AllowDirect("tcp", ep(51000)); err != nil || len(*f.events) != transactions {
		t.Fatal("shared grant at capacity rewrote firewall or failed", err)
	}
	if d.AllowDirect("tcp", ep(51128)) == nil || len(*f.events) != transactions {
		t.Fatal("more than 256 network/endpoint grants accepted")
	}
	if err := d.RemoveDirect("tcp", ep(51000)); err != nil {
		t.Fatal(err)
	}
	if d.AllowDirect("tcp", ep(51128)) == nil {
		t.Fatal("nonfinal reference freed an active slot")
	}
	if err := d.RemoveDirect("tcp", ep(51000)); err != nil {
		t.Fatal(err)
	}
	if err := d.AllowDirect("tcp", ep(51128)); err != nil {
		t.Fatal("final removal did not reclaim capacity", err)
	}
	if permits(f.rules, directPacket("tcp", ep(51000))) || !permits(f.rules, directPacket("udp", ep(51000))) {
		t.Fatal("reused slot retained old TCP permission or lost UDP")
	}
	if err := m.AllowUDPRelay(ep(52000)); err != nil {
		t.Fatal("direct capacity consumed separate relay capacity", err)
	}
}

func TestDirectFailedTransactionsRetainCommittedPolicyAndRequireRecovery(t *testing.T) {
	for _, operation := range []string{"allow", "remove", "block", "release"} {
		t.Run(operation, func(t *testing.T) {
			m, _, f := testManager(t)
			d := startDirectTunnel(t, m)
			ep := netip.MustParseAddrPort("8.8.8.8:50001")
			if err := d.AllowDirect("tcp", ep); err != nil {
				t.Fatal(err)
			}
			before := append([]rule(nil), f.rules...)
			f.fail = true
			var err error
			switch operation {
			case "allow":
				err = d.AllowDirect("udp", ep)
			case "remove":
				err = d.RemoveDirect("tcp", ep)
			case "block":
				err = m.Block()
			case "release":
				err = m.Release()
			}
			if err == nil || !m.RecoveryRequired() || !m.Active() || !reflect.DeepEqual(before, f.rules) {
				t.Fatal("failed transaction hid committed permissions or recovery", err)
			}
			f.fail = false
			if d.AllowDirect("tcp", ep) == nil || d.AllowDirect("udp", ep) == nil {
				t.Fatal("recovery allowed new or shared grants")
			}
			m.verifyTunnel = func(uint64) error { return errors.New("TUN already gone") }
			transactions := len(*f.events)
			if err := d.RemoveDirect("udp", ep); err != nil || len(*f.events) != transactions {
				t.Fatal("failed addition retained a phantom reference", err)
			}
			if err := d.RemoveDirect("tcp", ep); err != nil || permits(f.rules, directPacket("tcp", ep)) {
				t.Fatal("final removal lost its reference or could not retry after TUN loss", err)
			}
			if !m.RecoveryRequired() {
				t.Fatal("grant cleanup fabricated a fresh session")
			}
			if err := m.Block(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDirectRetiredSessionsDoNotInheritGrantsOrReferences(t *testing.T) {
	for _, transition := range []string{"block", "release", "restart", "switch", "local-only"} {
		t.Run(transition, func(t *testing.T) {
			m, s, f := testManager(t)
			d := directManager(t, m)
			journal := transition == "switch" || transition == "local-only"
			if journal {
				startJournalRuntime(t, m)
				if err := m.AllowTunnel(42); err != nil {
					t.Fatal(err)
				}
			} else {
				startDirectTunnel(t, m)
			}
			ep := netip.MustParseAddrPort("8.8.8.8:50001")
			for _, network := range []string{"tcp", "udp", "tcp", "udp"} {
				if err := d.AllowDirect(network, ep); err != nil {
					t.Fatal(err)
				}
			}
			if transition == "restart" {
				var err error
				m, err = newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
				if err != nil || !m.RecoveryRequired() {
					t.Fatal("restart failed to enter recovery", err)
				}
				d = directManager(t, m)
			} else if transition == "release" {
				if err := m.Release(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := m.Block(); err != nil {
					t.Fatal(err)
				}
			}
			for _, network := range []string{"tcp", "udp"} {
				if permits(f.rules, directPacket(network, ep)) || d.AllowDirect(network, ep) == nil {
					t.Fatal("retired session still authorized egress")
				}
				transactions := len(*f.events)
				if err := d.RemoveDirect(network, ep); err != nil || len(*f.events) != transactions {
					t.Fatal("retired session retained references", err)
				}
			}
			if journal {
				confirmJournalCleanup(t, m)
			}
			if transition == "restart" {
				confirmJournalCleanup(t, m)
				if err := m.Release(); err != nil {
					t.Fatal(err)
				}
			}
			if transition == "switch" {
				if err := m.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2"); err != nil {
					t.Fatal(err)
				}
				if err := m.Arm(testSID, "account-1", "resource-2", testEntry, nil, false); err != nil {
					t.Fatal(err)
				}
				if err := m.BeginRuntime(testSID, nextJournalKey(), "logical-2"); err != nil {
					t.Fatal(err)
				}
			} else if transition == "local-only" {
				if err := m.ArmLocalDNS(testSID, nil, false); err != nil {
					t.Fatal(err)
				}
				if err := m.BeginLocalRuntime(testSID); err != nil {
					t.Fatal(err)
				}
				if err := m.AllowLocalTunnel(testSID, 42); err != nil {
					t.Fatal(err)
				}
				for _, network := range []string{"tcp", "udp"} {
					if permits(f.rules, directPacket(network, ep)) || d.AllowDirect(network, ep) == nil {
						t.Fatal("local-only session inherited direct grants")
					}
				}
				if err := m.Block(); err != nil {
					t.Fatal(err)
				}
				confirmJournalCleanup(t, m)
				arm(t, m)
				if err := m.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
					t.Fatal(err)
				}
			} else {
				arm(t, m)
			}
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
			// A fresh/global session has no direct grants until explicitly requested.
			for _, network := range []string{"tcp", "udp"} {
				if permits(f.rules, directPacket(network, ep)) {
					t.Fatal("fresh tunnel inherited a direct grant")
				}
				if err := d.AllowDirect(network, ep); err != nil {
					t.Fatal(err)
				}
				if err := d.RemoveDirect(network, ep); err != nil || permits(f.rules, directPacket(network, ep)) {
					t.Fatal("fresh session inherited old references", err)
				}
			}
		})
	}
}

func TestDirectConcurrentReferencesReleaseLastConnection(t *testing.T) {
	m, _, f := testManager(t)
	d := startDirectTunnel(t, m)
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	if err := d.AllowDirect("tcp", ep); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.AllowDirect("tcp", ep); err != nil {
				t.Error(err)
				return
			}
			if err := d.RemoveDirect("tcp", ep); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if !permits(f.rules, directPacket("tcp", ep)) {
		t.Fatal("concurrent cleanup revoked surviving reference")
	}
	if err := d.RemoveDirect("tcp", ep); err != nil || permits(f.rules, directPacket("tcp", ep)) {
		t.Fatal("concurrent cleanup leaked references", err)
	}
}

func TestDirectReferenceOverflowDoesNotLoseExistingGrant(t *testing.T) {
	m, _, f := testManager(t)
	d := startDirectTunnel(t, m)
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	if err := d.AllowDirect("tcp", ep); err != nil {
		t.Fatal(err)
	}
	key := directEndpoint{"tcp", ep}
	m.directRefs[key] = ^uint64(0)
	transactions := len(*f.events)
	if d.AllowDirect("tcp", ep) == nil || m.directRefs[key] != ^uint64(0) || len(*f.events) != transactions || !permits(f.rules, directPacket("tcp", ep)) {
		t.Fatal("reference overflow lost a committed grant")
	}
	if err := d.RemoveDirect("tcp", ep); err != nil || m.directRefs[key] != ^uint64(0)-1 || len(*f.events) != transactions {
		t.Fatal("overflow prevented reference cleanup", err)
	}
}

func TestDirectCompilerRejectsMalformedOrInactiveGrants(t *testing.T) {
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	base := policy{Service: "service.exe", Tunnel: 42, Virtual: defaultVirtualPrefixes(), Direct: []directEndpoint{{"tcp", ep}}}
	for _, tc := range []struct {
		name   string
		change func(*policy)
	}{
		{"unarmed tunnel", func(p *policy) { p.Tunnel = 0 }},
		{"local-only", func(p *policy) { p.LocalOnly = true }},
		{"missing service", func(p *policy) { p.Service = "" }},
		{"unknown network", func(p *policy) { p.Direct = []directEndpoint{{"tcp4", ep}} }},
		{"zero port", func(p *policy) { p.Direct = []directEndpoint{{"tcp", netip.MustParseAddrPort("8.8.8.8:0")}} }},
		{"IPv6", func(p *policy) {
			p.Direct = []directEndpoint{{"tcp", netip.MustParseAddrPort("[2001:4860::8888]:50001")}}
		}},
		{"private", func(p *policy) { p.Direct = []directEndpoint{{"udp", netip.MustParseAddrPort("10.0.0.1:50001")}} }},
		{"virtual", func(p *policy) { p.Virtual = prefixes("8.8.8.0/24") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			for _, r := range compilePolicy(p) {
				if r.Kind == "direct" {
					t.Fatalf("unsafe grant encoded: %+v", r)
				}
			}
		})
	}
}

func TestDirectDoesNotRequireSOCKSRelayPolicy(t *testing.T) {
	m, _, f := testManager(t)
	d := directManager(t, m)
	if err := m.ArmWithEntryPolicy(testSID, "account-1", "resource-1", testEntry, EntryPolicy{UDP: true}, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	ep := netip.MustParseAddrPort("8.8.8.8:50001")
	if m.AllowUDPRelay(ep) == nil {
		t.Fatal("non-SOCKS session accepted a relay")
	}
	for _, network := range []string{"tcp", "udp"} {
		if err := d.AllowDirect(network, ep); err != nil || !permits(f.rules, directPacket(network, ep)) {
			t.Fatal("direct permission incorrectly requires SOCKS relay authority", err)
		}
	}
}
