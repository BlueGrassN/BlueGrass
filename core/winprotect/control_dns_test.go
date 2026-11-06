package winprotect

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
)

func TestControlDNSOwnerCheckIsInsidePolicyTransactionBoundary(t *testing.T) {
	m, s, fw := testManager(t)
	dns := []DNSEndpoint{{Endpoint: netip.MustParseAddrPort("9.9.9.9:53"), Transport: "udp"}}
	if err := m.UpdateControlDNSForOwner(testSID, dns); err != nil || m.Active() || len(*s.events) != 0 {
		t.Fatal("inactive helper armed protection", err)
	}
	arm(t, m)
	before := append([]rule(nil), fw.rules...)
	if !errors.Is(m.UpdateControlDNSForOwner("S-1-5-21-100-200-300-1002", dns), ErrOwner) || !reflect.DeepEqual(fw.rules, before) {
		t.Fatal("other owner updated control DNS")
	}
	if err := m.UpdateControlDNSForOwner(testSID, dns); err != nil {
		t.Fatal(err)
	}
}

func TestControlDNSRefreshPreservesBindingAndVerifiedSession(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if err := m.AllowUDPRelay(netip.MustParseAddrPort("8.8.8.8:50000")); err != nil {
		t.Fatal(err)
	}
	state, saved, original := m.state, *s.record, m.policy
	dns := []DNSEndpoint{{Endpoint: netip.MustParseAddrPort("1.1.1.1:53"), Transport: "tcp"}}
	if err := m.UpdateControlDNS(dns); err != nil {
		t.Fatal(err)
	}
	dns[0].Transport = "udp"
	if m.state != state || *s.record != saved || !m.armed || m.policy.Tunnel != 42 || m.policy.Entry != original.Entry || !reflect.DeepEqual(m.policy.DNS, original.DNS) || !reflect.DeepEqual(m.policy.UDPRelays, original.UDPRelays) {
		t.Fatal("control DNS refresh mutated session authorization")
	}
	if !permits(f.rules, packet{addr: "1.1.1.1", remote: 53, protocol: 6, app: "service.exe"}) || permits(f.rules, packet{addr: "1.1.1.1", remote: 53, protocol: 17, app: "service.exe"}) || permits(f.rules, packet{addr: "1.1.1.1", remote: 53, protocol: 6, app: "svchost.exe"}) {
		t.Fatal("control DNS was broadened or mutated by caller")
	}
	previous := append([]rule(nil), f.rules...)
	f.fail = true
	if m.UpdateControlDNS([]DNSEndpoint{{Endpoint: netip.MustParseAddrPort("8.8.8.8:53"), Transport: "udp"}}) == nil || !reflect.DeepEqual(f.rules, previous) || m.policy.ControlDNS[0].Endpoint.String() != "1.1.1.1:53" {
		t.Fatal("failed refresh reported or retained a new permission")
	}
	f.fail = false
	if err := m.UpdateControlDNS(nil); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "1.1.1.1", remote: 53, protocol: 6, app: "service.exe"}) {
		t.Fatal("control DNS replacement accumulated old endpoints")
	}
}

func TestControlDNSRequiresCommittedOwnedProtection(t *testing.T) {
	m, s, f := testManager(t)
	dns := []DNSEndpoint{{Endpoint: netip.MustParseAddrPort("9.9.9.9:53"), Transport: "udp"}}
	if m.UpdateControlDNS(dns) == nil || len(*s.events) != 0 {
		t.Fatal("inactive control DNS installed filters")
	}
	arm(t, m)
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.UpdateControlDNS(dns); err != nil {
		t.Fatal("recovery cannot resolve fixed API", err)
	}
	if restarted.armed || restarted.policy.Tunnel != 0 || !restarted.RecoveryRequired() || restarted.OwnerSID() != testSID {
		t.Fatal("DNS recovered business permission")
	}
	before := append([]rule(nil), f.rules...)
	for _, endpoint := range []string{"[fe80::1]:53", "169.254.1.1:53", "198.18.0.1:53"} {
		if restarted.UpdateControlDNS([]DNSEndpoint{{Endpoint: netip.MustParseAddrPort(endpoint), Transport: "udp"}}) == nil || !reflect.DeepEqual(before, f.rules) {
			t.Fatal("unenforceable resolver accepted", endpoint)
		}
	}
}
