package winprotect

import (
	"errors"
	"net/netip"
	"sync"
	"testing"
)

var testEntry = netip.MustParseAddrPort("203.0.113.9:8443")

const testSID = "S-1-5-21-100-200-300-1001"

type memoryStore struct {
	record  *intent
	fail    bool
	loadErr error
	events  *[]string
}

func (s *memoryStore) Load() (*intent, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.record == nil {
		return nil, nil
	}
	v := *s.record
	return &v, nil
}
func (s *memoryStore) Save(v intent) error {
	*s.events = append(*s.events, "save")
	if s.fail {
		return errors.New("disk failure")
	}
	s.record = &v
	return nil
}
func (s *memoryStore) Close() error { return nil }

// This fake is the OS boundary. The real manager and policy compiler run in tests.
type memoryFirewall struct {
	rules  []rule
	fail   bool
	closed bool
	events *[]string
}

func (f *memoryFirewall) Present() (bool, error) { return len(f.rules) != 0, nil }
func (f *memoryFirewall) Replace(r []rule) error {
	*f.events = append(*f.events, "replace")
	if f.fail {
		return errors.New("transaction failure")
	}
	f.rules = append([]rule(nil), r...)
	return nil
}
func (f *memoryFirewall) Remove() error {
	*f.events = append(*f.events, "remove")
	if f.fail {
		return errors.New("delete failure")
	}
	f.rules = nil
	return nil
}
func (f *memoryFirewall) Close() error { f.closed = true; return nil }

func testManager(t *testing.T) (*Manager, *memoryStore, *memoryFirewall) {
	t.Helper()
	events := []string{}
	s := &memoryStore{events: &events}
	f := &memoryFirewall{events: &events}
	m, err := newManager("service.exe", "ui.exe", s, f, func() ([]link, error) {
		return []link{{LUID: 7, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}}}, nil
	}, func(luid uint64) error {
		if luid != 42 {
			return errors.New("not product TUN")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, s, f
}
func arm(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Arm(testSID, "account-1", "resource-1", testEntry, []netip.AddrPort{netip.MustParseAddrPort("9.9.9.9:53")}, true); err != nil {
		t.Fatal(err)
	}
}

func TestIntentPrecedesActivationAndCloseNeverReleases(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	if got := *s.events; len(got) < 2 || got[len(got)-2] != "save" || got[len(got)-1] != "replace" {
		t.Fatalf("unsafe activation order: %v", got)
	}
	if !m.Active() || m.OwnerSID() != testSID {
		t.Fatal("missing committed owner/protection")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if len(f.rules) == 0 || !s.record.ProtectIntent || !f.closed {
		t.Fatal("close released protection")
	}
	if m.Release() == nil {
		t.Fatal("closed manager accepted mutation")
	}
}

func TestFailuresKeepDurableIntentAndNeverInstallTunnel(t *testing.T) {
	m, s, f := testManager(t)
	s.fail = true
	if m.Arm(testSID, "a", "r", testEntry, nil, false) == nil || len(f.rules) != 0 {
		t.Fatal("activated without durable intent")
	}
	s.fail = false
	f.fail = true
	if m.Arm(testSID, "a", "r", testEntry, nil, false) == nil || s.record == nil || !s.record.ProtectIntent {
		t.Fatal("lost intent on activation failure")
	}
	if m.AllowTunnel(42) == nil {
		t.Fatal("permit after failed activation")
	}
	if !errors.Is(m.Arm(testSID, "a", "other-resource", testEntry, nil, false), ErrNotBlocked) {
		t.Fatal("replaced failed active intent without successful Block")
	}
	f.fail = false
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if !m.Active() {
		t.Fatal("could not retry blocking")
	}
}

func TestOwnerAndGenerationGate(t *testing.T) {
	m, _, _ := testManager(t)
	arm(t, m)
	if !m.Authorized(testSID, "account-1", "resource-1") || m.Authorized(testSID, "account-2", "resource-1") {
		t.Fatal("identity mismatch")
	}
	if m.Arm("S-1-5-21-100-200-300-1002", "b", "r", testEntry, nil, false) == nil {
		t.Fatal("other owner stole intent")
	}
	if m.AllowTunnel(0) == nil || m.AllowTunnel(7) == nil {
		t.Fatal("unverified interface accepted")
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if m.Arm(testSID, "b", "r", testEntry, nil, false) == nil {
		t.Fatal("replaced resource while tunnel allowed")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if err := m.Arm(testSID, "b", "r", testEntry, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestRestartBlocksAndRetainsOwner(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.OwnerSID() != testSID || !restarted.Active() || !restarted.RecoveryRequired() {
		t.Fatal("lost recovery identity")
	}
	for _, r := range f.rules {
		if r.Kind == "tunnel" {
			t.Fatal("restored stale LUID")
		}
	}
	if restarted.AllowTunnel(42) == nil {
		t.Fatal("resumed without new Arm and verification")
	}
	// Explicit same-owner recovery can release without business login.
	if restarted.Release() == nil {
		t.Fatal("release skipped restart cleanup")
	}
	confirmJournalCleanup(t, restarted)
	if err := restarted.Release(); err != nil {
		t.Fatal(err)
	}
	if restarted.Active() || s.record.ProtectIntent || len(f.rules) != 0 {
		t.Fatal("release incomplete")
	}
}

func TestResidueWithoutIntentStaysBlocked(t *testing.T) {
	m, s, f := testManager(t)
	f.rules = []rule{{Kind: "residue"}}
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Active() || !restarted.RecoveryRequired() || !s.record.ProtectIntent {
		t.Fatal("silently released residue")
	}
	if restarted.OwnerSID() != "" || restarted.Release() == nil {
		t.Fatal("unowned residue needs administrator Repair")
	}
	if restarted.Arm(testSID, "a", "r", testEntry, nil, false) == nil {
		t.Fatal("claimed unknown residue")
	}
}

func TestReleaseFailureDoesNotClearIntent(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	f.fail = true
	if m.Release() == nil || !m.Active() || !s.record.ProtectIntent {
		t.Fatal("delete failure lost protection")
	}
	f.fail = false
	s.fail = true
	if m.Release() == nil || !m.Active() || !m.RecoveryRequired() || !s.record.ProtectIntent {
		t.Fatal("clear failure not reported accurately")
	}
	s.fail = false
	if !errors.Is(m.Arm(testSID, "account-1", "other-resource", testEntry, nil, false), ErrNotBlocked) {
		t.Fatal("clear failure allowed rearm before explicit recovery")
	}
	if err := m.Release(); err != nil {
		t.Fatal(err)
	}
	if s.record.ProtectIntent {
		t.Fatal("retry failed")
	}
}

func TestConcurrentStatusAndBlock(t *testing.T) {
	m, _, _ := testManager(t)
	arm(t, m)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = m.Block()
				_ = m.OwnerSID()
				_ = m.Active()
				_ = m.Authorized(testSID, "account-1", "resource-1")
			}
		}()
	}
	wg.Wait()
}

func TestFailedRevocationInvalidatesSessionUntilBlockAndRearm(t *testing.T) {
	for _, op := range []string{"block", "release"} {
		t.Run(op, func(t *testing.T) {
			m, _, f := testManager(t)
			arm(t, m)
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
			f.fail = true
			var err error
			if op == "block" {
				err = m.Block()
			} else {
				err = m.Release()
			}
			if err == nil || !m.RecoveryRequired() || !m.Active() {
				t.Fatal("failed revocation hid recovery")
			}
			f.fail = false
			if !errors.Is(m.AllowTunnel(42), ErrRecoveryRequired) {
				t.Fatal("failed revocation left business session authorized")
			}
			if !errors.Is(m.Arm(testSID, "account-1", "resource-1", testEntry, nil, false), ErrNotBlocked) {
				t.Fatal("rearmed without successful block")
			}
			if err := m.Block(); err != nil {
				t.Fatal(err)
			}
			if m.AllowTunnel(42) == nil {
				t.Fatal("block implicitly reauthorized session")
			}
			arm(t, m)
			if err := m.AllowTunnel(42); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTunnelCannotSwitchLUIDWithinVerifiedSession(t *testing.T) {
	m, _, _ := testManager(t)
	m.verifyTunnel = func(uint64) error { return nil }
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.AllowTunnel(43), ErrNotBlocked) {
		t.Fatal("switched verified tunnel without blocking and rearming")
	}
}

func TestStartupRevokesStaleTunnelEvenWhenRecoverySaveFails(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	s.fail = true
	if _, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel); err == nil {
		t.Fatal("hid recovery write failure")
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 80, protocol: 6, luid: 42}) {
		t.Fatal("left stale TUN permit on recovery write failure")
	}
	if !s.record.ProtectIntent || s.record.OwnerSID != testSID {
		t.Fatal("destroyed original recovery identity")
	}
}

func TestFailedTunnelInstallationRequiresRecovery(t *testing.T) {
	m, _, f := testManager(t)
	arm(t, m)
	f.fail = true
	if m.AllowTunnel(42) == nil || !m.RecoveryRequired() {
		t.Fatal("failed TUN installation did not require recovery")
	}
	f.fail = false
	if m.AllowTunnel(42) == nil {
		t.Fatal("retried session permit without recovery")
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 80, protocol: 6, luid: 42}) {
		t.Fatal("failed transaction installed tunnel permit")
	}
}

func TestUnreadableJournalDoesNotClaimOwnerOrDiscardProtection(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	s.loadErr = errors.New("corrupt journal")
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Active() || !restarted.RecoveryRequired() || restarted.OwnerSID() != "" || restarted.Release() == nil {
		t.Fatal("corrupt journal was trusted or discarded")
	}
	if permits(f.rules, packet{addr: "8.8.8.8", remote: 80, protocol: 6, luid: 7}) {
		t.Fatal("corrupt journal allowed ordinary traffic")
	}
}

func TestRestartRearmStillExcludesAllPinnedTunnelPools(t *testing.T) {
	m, s, f := testManager(t)
	arm(t, m)
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	confirmJournalCleanup(t, restarted)
	if err := restarted.Release(); err != nil {
		t.Fatal(err)
	}
	arm(t, restarted)
	for _, addr := range []string{"172.31.255.1", "172.30.255.2", "10.255.255.2", "fdfe:dcba:9876::2", "fdde:adbe:ef01::2", "198.18.0.1", "198.19.0.1"} {
		// UI TCP443 otherwise has a higher permit than the LAN rules. The
		// restart's default virtual blocks must outrank both exceptions.
		if permits(f.rules, packet{addr: addr, remote: 443, protocol: 6, luid: 7, app: "ui.exe"}) {
			t.Errorf("restarted policy leaked virtual address %s", addr)
		}
	}
}
