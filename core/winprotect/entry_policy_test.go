package winprotect

import (
	"net/netip"
	"testing"
)

func TestTypedEntryPolicyPermitsOnlyExactRequiredTransports(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry EntryPolicy
	}{
		{"socks", EntryPolicy{TCP: true, AuthenticatedSOCKSRelay: true}},
		{"ss", EntryPolicy{TCP: true, UDP: true}},
		{"quic", EntryPolicy{UDP: true}},
		{"tcp", EntryPolicy{TCP: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, f := testManager(t)
			if err := m.ArmWithEntryPolicy(testSID, "account-1", "resource-1", testEntry, tc.entry, nil, false); err != nil {
				t.Fatal(err)
			}
			for _, transport := range []struct {
				number uint8
				want   bool
			}{{6, tc.entry.TCP}, {17, tc.entry.UDP}} {
				p := packet{addr: testEntry.Addr().String(), remote: testEntry.Port(), protocol: transport.number, app: "service.exe", luid: 7}
				if permits(f.rules, p) != transport.want {
					t.Fatal("entry transport broadened", transport)
				}
				p.remote++
				if permits(f.rules, p) {
					t.Fatal("entry port widened")
				}
				p.remote--
				p.app = "other.exe"
				if permits(f.rules, p) {
					t.Fatal("entry process widened")
				}
			}
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
			err := m.AllowUDPRelay(netip.MustParseAddrPort("8.8.8.8:50001"))
			if (err == nil) != tc.entry.AuthenticatedSOCKSRelay {
				t.Fatal("non-SOCKS session got dynamic relay", err)
			}
		})
	}
}
