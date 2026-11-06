package winprotect

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
)

// Keep this assertion at runtime so the pre-fix implementation fails the
// lifecycle regression without preventing the package from compiling.
func relayRemover(t *testing.T, m *Manager) interface{ RemoveUDPRelay(netip.AddrPort) error } {
	t.Helper()
	r, ok := any(m).(interface{ RemoveUDPRelay(netip.AddrPort) error })
	if !ok {
		t.Fatal("closed SOCKS associations cannot release their relay permission")
	}
	return r
}

func TestUDPRelayReferenceCountRetainsOtherAssociations(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.8.8:50001")
	packet := packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}
	for range 2 {
		if err := m.AllowUDPRelay(relay); err != nil {
			t.Fatal(err)
		}
	}
	r := relayRemover(t, m)
	transactions := len(*f.events)
	if err := r.RemoveUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if !permits(f.rules, packet) || len(*f.events) != transactions {
		t.Fatal("one closed association revoked a shared relay or rewrote WFP")
	}
	if err := r.RemoveUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, packet) || len(*f.events) != transactions+1 {
		t.Fatal("last closed association retained relay permission")
	}
	transactions = len(*f.events)
	if err := r.RemoveUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if len(*f.events) != transactions {
		t.Fatal("absent relay release changed policy")
	}
}

func TestUDPRelayClosedAssociationsDoNotExhaustActiveCap(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	r := relayRemover(t, m)
	for port := uint16(50000); port < 50130; port++ {
		endpoint := netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), port)
		if err := m.AllowUDPRelay(endpoint); err != nil {
			t.Fatalf("association %d: %v", port-50000, err)
		}
		if err := r.RemoveUDPRelay(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	for port := uint16(51000); port < 51064; port++ {
		if err := m.AllowUDPRelay(netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), port)); err != nil {
			t.Fatal(err)
		}
	}
	if m.AllowUDPRelay(netip.MustParseAddrPort("8.8.8.8:51064")) == nil {
		t.Fatal("more than 64 concurrently active endpoints accepted")
	}
	if err := r.RemoveUDPRelay(netip.MustParseAddrPort("8.8.8.8:51000")); err != nil {
		t.Fatal(err)
	}
	if err := m.AllowUDPRelay(netip.MustParseAddrPort("8.8.8.8:51064")); err != nil {
		t.Fatal("closed slot was not reusable", err)
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 51000, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("reclaimed slot kept its old permission")
	}
}

func TestUDPRelayRemovalFailureKeepsPermissionForRecovery(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.8.8:50001")
	if err := m.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	r := relayRemover(t, m)
	f.fail = true
	if r.RemoveUDPRelay(relay) == nil || !m.RecoveryRequired() {
		t.Fatal("failed removal hid retained permission")
	}
	if !permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("failed WFP transaction unexpectedly committed")
	}
	f.fail = false
	if m.AllowUDPRelay(relay) == nil {
		t.Fatal("failed removal allowed new business authorization")
	}
	m.verifyTunnel = func(uint64) error { return errors.New("TUN already gone") }
	if err := r.RemoveUDPRelay(relay); err != nil {
		t.Fatal("cleanup could not retry after TUN disappeared", err)
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) {
		t.Fatal("retry retained stale relay")
	}
	if !m.RecoveryRequired() {
		t.Fatal("relay cleanup manufactured fresh session authorization")
	}
}

func TestUDPRelayReferencesResetAfterBlockOrRelease(t *testing.T) {
	for _, reset := range []string{"block", "release"} {
		t.Run(reset, func(t *testing.T) {
			m, _, f := testManager(t)
			arm(t, m)
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
			relay := netip.MustParseAddrPort("8.8.8.8:50001")
			for range 2 {
				if err := m.AllowUDPRelay(relay); err != nil {
					t.Fatal(err)
				}
			}
			r := relayRemover(t, m)
			var err error
			if reset == "block" {
				err = m.Block()
			} else {
				err = m.Release()
			}
			if err != nil {
				t.Fatal(err)
			}
			arm(t, m)
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
			if err := m.AllowUDPRelay(relay); err != nil {
				t.Fatal(err)
			}
			if err := r.RemoveUDPRelay(relay); err != nil {
				t.Fatal(err)
			}
			if permits(f.rules, packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}) {
				t.Fatal("new session inherited old association references")
			}
		})
	}
}

func TestUDPRelayConcurrentAssociationsReleaseLastReference(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.8.8:50001")
	r := relayRemover(t, m)
	if err := m.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.AllowUDPRelay(relay); err != nil {
				t.Error(err)
				return
			}
			if err := r.RemoveUDPRelay(relay); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	p := packet{addr: "8.8.8.8", remote: 50001, protocol: 17, app: "service.exe", luid: 7}
	if !permits(f.rules, p) {
		t.Fatal("concurrent cleanup revoked surviving association")
	}
	if err := r.RemoveUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	if permits(f.rules, p) {
		t.Fatal("concurrent cleanup leaked association references")
	}
}
