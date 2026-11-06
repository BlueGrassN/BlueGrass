package winprotect

import (
	"net/netip"
	"slices"
	"testing"
)

type localProtection interface {
	ArmLocalDNS(string, []DNSEndpoint, bool) error
	BeginLocalRuntime(string) error
	AllowLocalTunnel(string, uint64) error
}

func localManager(t *testing.T, m *Manager) localProtection {
	t.Helper()
	l, ok := any(m).(localProtection)
	if !ok {
		t.Fatal("separate local-only lifecycle is missing")
	}
	return l
}

func TestLocalOnlyRequiresCleanJournalAndNeverGrantsBusiness(t *testing.T) {
	m, s, f := testManager(t)
	l := localManager(t, m)
	bindJournal(t, m)
	dns := []DNSEndpoint{{netip.MustParseAddrPort("192.168.1.1:53"), "tcp"}}
	if l.AllowLocalTunnel(testSID, 42) == nil || l.BeginLocalRuntime(testSID) == nil {
		t.Fatal("local runtime admitted before blocking policy")
	}
	if err := l.ArmLocalDNS(testSID, dns, true); err != nil {
		t.Fatal(err)
	}
	events := *s.events
	if !slices.Equal(events[len(events)-2:], []string{"save", "replace"}) {
		t.Fatal("local policy preceded durable intent", events)
	}
	if l.AllowLocalTunnel(testSID, 42) == nil {
		t.Fatal("local permit preceded runtime checkpoint")
	}
	if err := l.BeginLocalRuntime(testSID); err != nil {
		t.Fatal(err)
	}
	if !s.record.CleanupRequired || s.record.Phase != "local" {
		t.Fatal("local runtime cleanup not durable")
	}
	if l.AllowLocalTunnel("S-1-5-21-9-9-9-1001", 42) == nil || l.AllowLocalTunnel(testSID, 43) == nil {
		t.Fatal("unowned local permit")
	}
	if err := l.AllowLocalTunnel(testSID, 42); err != nil {
		t.Fatal(err)
	}
	if m.AllowTunnel(42) == nil || m.AllowUDPRelay(testEntry) == nil || m.Release() == nil {
		t.Fatal("local runtime granted business/release proof")
	}
	for _, tc := range []struct {
		p    packet
		want bool
	}{
		{packet{addr: "172.31.255.2", protocol: 17, remote: 53, luid: 42}, true},
		{packet{addr: "fdfe:dcba:9876::2", protocol: 6, remote: 53, luid: 42}, true},
		{packet{addr: "192.168.1.1", protocol: 6, remote: 53, luid: 7, app: "service.exe"}, true},
		{packet{addr: "192.168.1.1", protocol: 17, remote: 53, luid: 7, app: "service.exe"}, false},
		{packet{addr: "192.168.1.1", protocol: 6, remote: 53, luid: 7, app: "other"}, false},
		{packet{addr: "8.8.8.8", protocol: 6, remote: 443, luid: 42, app: "other"}, false},
		{packet{addr: "192.168.1.99", protocol: 6, remote: 80, luid: 42}, true},
		{packet{addr: "198.18.0.1", protocol: 6, remote: 80, luid: 42}, false},
		{packet{addr: "172.31.255.2", protocol: 6, remote: 53, luid: 7}, false},
	} {
		if got := permits(f.rules, tc.p); got != tc.want {
			t.Fatalf("%+v allowed=%v", tc.p, got)
		}
	}
	for _, r := range f.rules {
		if r.Kind == "local-tunnel-dns" && (r.Persistent || r.Boot || r.LUID != 42 || r.RemotePort != 53) {
			t.Fatal("local TUN permit persisted or broadened")
		}
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet{addr: "192.168.1.1", protocol: 6, remote: 53, luid: 7, app: "service.exe"}) || permits(f.rules, packet{addr: "172.31.255.2", protocol: 17, remote: 53, luid: 42}) {
		t.Fatal("Block retained old local resolver/interface")
	}
	if permits(f.rules, packet{addr: "192.168.1.99", protocol: 6, remote: 80, luid: 7}) {
		t.Fatal("Block retained old local physical snapshot")
	}
	if l.ArmLocalDNS(testSID, dns, true) == nil {
		t.Fatal("unconfirmed local cleanup replaced")
	}
	confirmJournalCleanup(t, m)
	if err := l.ArmLocalDNS(testSID, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestLocalJournalCrashAndWriteFailuresStayBlocked(t *testing.T) {
	m, s, f := testManager(t)
	l := localManager(t, m)
	bindJournal(t, m)
	s.fail = true
	if l.ArmLocalDNS(testSID, nil, true) == nil || len(f.rules) != 0 {
		t.Fatal("local filters activated without journal")
	}
	s.fail = false
	if err := l.ArmLocalDNS(testSID, nil, true); err != nil {
		t.Fatal(err)
	}
	s.fail = true
	if l.BeginLocalRuntime(testSID) == nil || s.record.CleanupRequired {
		t.Fatal("local runtime started without journal")
	}
	s.fail = false
	if err := l.ArmLocalDNS(testSID, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := l.BeginLocalRuntime(testSID); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	if l.AllowLocalTunnel(testSID, 42) == nil || permits(f.rules, packet{addr: "172.31.255.2", protocol: 17, remote: 53, luid: 42}) {
		t.Fatal("failed transaction falsely allowed local TUN")
	}
	f.fail = false
	wire, err := encodeIntent(*s.record)
	if err != nil {
		t.Fatal(err)
	}
	s.record, err = decodeIntent(wire)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if !r.CleanupRequired() || !r.RecoveryRequired() || r.Release() == nil || localManager(t, r).AllowLocalTunnel(testSID, 42) == nil {
		t.Fatal("restart restored local runtime authority")
	}
	if id, err := r.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "logical-1" {
		t.Fatal("local runtime lost original logical session")
	}
}
