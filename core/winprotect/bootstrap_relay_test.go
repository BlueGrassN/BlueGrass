package winprotect

import (
	"errors"
	"net/netip"
	"testing"
)

var bootstrapDNS = []netip.AddrPort{netip.MustParseAddrPort("9.9.9.9:53")}

func TestArmDNSBlocksBeforeResolutionAndNeedsFullArm(t *testing.T) {
	m, s, f := testManager(t)
	if err := m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, true); err != nil {
		t.Fatal(err)
	}
	if got := *s.events; len(got) != 2 || got[0] != "save" || got[1] != "replace" {
		t.Fatalf("bootstrap activation order: %v", got)
	}
	if !m.Active() || !m.Authorized(testSID, "account-1", "resource-1") || !s.record.ProtectIntent || m.RecoveryRequired() {
		t.Fatal("bootstrap lost committed identity or protection")
	}
	for _, proto := range []uint8{6, 17} {
		if !permits(f.rules, packet{addr: "9.9.9.9", remote: 53, protocol: proto, app: "service.exe", luid: 7}) {
			t.Fatal("controlled resolver is blocked")
		}
		if permits(f.rules, packet{addr: "9.9.9.9", remote: 53, protocol: proto, app: "other.exe", luid: 7}) || permits(f.rules, packet{addr: "203.0.113.9", remote: 8443, protocol: proto, app: "service.exe", luid: 7}) {
			t.Fatal("bootstrap broadened resolver or installed entry allowance")
		}
	}
	if !errors.Is(m.AllowTunnel(42), ErrRecoveryRequired) {
		t.Fatal("bootstrap authorized tunnel before full Arm")
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 80, protocol: 6, luid: 42}) {
		t.Fatal("bootstrap installed tunnel allowance")
	}
	arm(t, m) // No intervening Block needed to complete this exact binding.
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapBindingAndEndpointValidation(t *testing.T) {
	m, _, f := testManager(t)
	if err := m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.ArmDNS("S-1-5-21-100-200-300-1002", "account-1", "resource-1", bootstrapDNS, false), ErrOwner) {
		t.Fatal("bootstrap owner changed")
	}
	if m.Arm(testSID, "another-account", "resource-1", testEntry, bootstrapDNS, false) == nil || m.ArmDNS(testSID, "account-1", "another-resource", bootstrapDNS, false) == nil {
		t.Fatal("bootstrap changed identity without Block")
	}
	for _, endpoint := range []string{"[fe80::1%7]:53", "[fe80::1]:53", "198.18.0.1:53", "9.9.9.9:0"} {
		if err := m.ArmDNS(testSID, "account-1", "resource-1", []netip.AddrPort{netip.MustParseAddrPort(endpoint)}, false); err == nil {
			t.Errorf("accepted unsupported DNS endpoint %s", endpoint)
		}
	}
	arm(t, m)
	if !errors.Is(m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false), ErrNotBlocked) {
		t.Fatal("bootstrap bypassed active-session Block gate")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if err := m.ArmDNS(testSID, "another-account", "another-resource", bootstrapDNS, false); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "203.0.113.9", remote: 8443, protocol: 6, app: "service.exe", luid: 7}) {
		t.Fatal("bootstrap retained the previous entry allowance")
	}
}

func TestBootstrapFailuresPreserveIntentAndRequireRecovery(t *testing.T) {
	m, s, f := testManager(t)
	s.fail = true
	if m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false) == nil || len(f.rules) != 0 || m.Active() {
		t.Fatal("activated bootstrap without durable intent")
	}
	s.fail = false
	f.fail = true
	if m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false) == nil || !m.Active() || !m.RecoveryRequired() || s.record == nil || !s.record.ProtectIntent {
		t.Fatal("failed bootstrap lost durable recovery")
	}
	f.fail = false
	if m.AllowTunnel(42) == nil || m.Arm(testSID, "account-1", "resource-1", testEntry, bootstrapDNS, false) == nil {
		t.Fatal("failed bootstrap bypassed recovery")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if err := m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	if m.Arm(testSID, "account-1", "resource-1", testEntry, bootstrapDNS, false) == nil || !m.Active() || !m.RecoveryRequired() {
		t.Fatal("failed final Arm lost bootstrap recovery")
	}
	if permits(f.rules, packet{addr: "203.0.113.9", remote: 8443, protocol: 6, app: "service.exe", luid: 7}) {
		t.Fatal("failed final Arm opened entry")
	}
}

func TestUDPRelayRequiresVerifiedLiveTunnelAndExactServiceEndpoint(t *testing.T) {
	m, _, f := testManager(t)
	relay := netip.MustParseAddrPort("8.8.8.8:50001")
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("idle relay permitted")
	}
	if err := m.ArmDNS(testSID, "account-1", "resource-1", bootstrapDNS, false); err != nil {
		t.Fatal(err)
	}
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("bootstrap relay permitted")
	}
	arm(t, m)
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("relay permitted before exit proof")
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"203.0.113.10:50001", "192.168.1.1:50001", "100.64.0.1:50001", "198.18.0.1:50001", "[2001:db8::1]:50001", "[::ffff:8.8.8.8]:50001", "8.8.8.8:0", "0.0.0.0:50001"} {
		if m.AllowUDPRelay(netip.MustParseAddrPort(invalid)) == nil {
			t.Errorf("accepted relay %s", invalid)
		}
	}
	m.verifyTunnel = func(uint64) error { return errors.New("tunnel disappeared") }
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("relay accepted after tunnel disappeared")
	}
	m.verifyTunnel = func(uint64) error { return nil }
	if err := m.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p    packet
		want bool
	}{
		{packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}, true},
		{packet{addr: "8.8.8.8", remote: 50001, protocol: 6, app: "service.exe", luid: 7}, false},
		{packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "other.exe", luid: 7}, false},
		{packet{addr: "8.8.8.8", remote: 50002, protocol: 17, app: "service.exe", luid: 7}, false},
		{packet{addr: "9.9.9.9", remote: 50001, protocol: 17, app: "service.exe", luid: 7}, false},
		{packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7, inbound: true}, false},
	} {
		if permits(f.rules, tc.p) != tc.want {
			t.Errorf("relay policy mismatch: %+v", tc.p)
		}
	}
	found := false
	for _, r := range f.rules {
		if r.Kind == "udp-relay" {
			found = true
			if r.Persistent || r.Boot {
				t.Fatal("relay permission persisted")
			}
		}
	}
	if !found {
		t.Fatal("missing explicit relay rule")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) || m.AllowUDPRelay(relay) == nil {
		t.Fatal("Block retained relay authorization")
	}
	arm(t, m)
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("new session reused old tunnel proof")
	}
}

func TestUDPRelayCapAndTransactionFailure(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	endpoint := func(port uint16) netip.AddrPort { return netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), port) }
	for i := uint16(0); i < 64; i++ {
		if err := m.AllowUDPRelay(endpoint(50000 + i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.AllowUDPRelay(endpoint(50000)); err != nil {
		t.Fatal("existing endpoint consumed another slot", err)
	}
	if m.AllowUDPRelay(endpoint(50064)) == nil || permits(f.rules, packet{addr: "8.8.8.8", remote: 50064, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("exceeded relay endpoint limit")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	if m.AllowUDPRelay(endpoint(51000)) == nil || !m.RecoveryRequired() || !m.Active() {
		t.Fatal("failed relay transaction hid recovery")
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 51000, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("failed transaction allowed relay")
	}
	f.fail = false
	if m.AllowUDPRelay(endpoint(51000)) == nil {
		t.Fatal("relay failure reused session authorization")
	}
}

func TestUDPRelayRejectsIPv6EvenWhenEntryMatches(t *testing.T) {
	m, _, _ := testManager(t)
	if err := m.Arm(testSID, "account-1", "resource-1", netip.MustParseAddrPort("[2001:db8::1]:8443"), nil, false); err != nil {
		t.Fatal(err)
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if m.AllowUDPRelay(netip.MustParseAddrPort("[2001:db8::1]:50001")) == nil {
		t.Fatal("accepted an IPv6 relay matching an IPv6 entry")
	}
}

func TestRestartAndReleaseDiscardRelayPermissions(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.8.8:50001")
	if err := m.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) || restarted.AllowUDPRelay(relay) == nil {
		t.Fatal("restart recovered relay permission")
	}
	confirmJournalCleanup(t, restarted)
	if err := restarted.Release(); err != nil {
		t.Fatal(err)
	}
	arm(t, restarted)
	if err := restarted.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if err := restarted.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Release(); err != nil {
		t.Fatal(err)
	}
	arm(t, restarted)
	if err := restarted.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("release/rearm resurrected relay permission")
	}
}
