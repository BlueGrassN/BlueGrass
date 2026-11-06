package winprotect

import (
	"bytes"
	"errors"
	"testing"

	"bluegrass/native/core/engine"
)

func journalKey() engine.ResourceKey {
	return engine.ResourceKey{Environment: "test", DataSource: "api-v2", AccountKey: "account-1", Kind: "node", SourceID: "resource-1"}
}

func TestJournalCodecReplayAtEveryRuntimeBoundary(t *testing.T) {
	for _, stage := range []string{"bound", "bootstrap", "armed", "running", "maintenance", "clean"} {
		t.Run(stage, func(t *testing.T) {
			m, s, f := testManager(t)
			bindJournal(t, m)
			if stage != "bound" {
				if err := m.ArmDNS(testSID, "account-1", "resource-1", nil, false); err != nil {
					t.Fatal(err)
				}
			}
			if stage != "bound" && stage != "bootstrap" {
				arm(t, m)
			}
			if stage == "running" || stage == "maintenance" || stage == "clean" {
				if err := m.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "maintenance" {
				if _, err := m.BeginMaintenance(testSID); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "clean" {
				confirmJournalCleanup(t, m)
			}
			wire, err := encodeIntent(*s.record)
			if err != nil {
				t.Fatal("invalid committed journal", err)
			}
			for _, forbidden := range []string{"password", "token", "endpoint", "luid", "observed", "proof"} {
				if bytes.Contains(bytes.ToLower(wire), []byte(forbidden)) {
					t.Fatal("runtime secret/proof persisted", forbidden)
				}
			}
			s.record, err = decodeIntent(wire)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
			if err != nil {
				t.Fatal(err)
			}
			wantDirty := stage == "running" || stage == "maintenance"
			if replay.CleanupRequired() != wantDirty || !replay.Active() || !replay.RecoveryRequired() {
				t.Fatal("crash boundary lost blocking semantics", stage)
			}
			if id, err := replay.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "logical-1" {
				t.Fatal("crash boundary lost binding", stage, id, err)
			}
			if replay.AllowTunnel(42) == nil {
				t.Fatal("persisted phase restored proof")
			}
		})
	}
}

func TestJournalReleaseTombstoneFailureKeepsSessionForReplay(t *testing.T) {
	m, s, f := testManager(t)
	bindJournal(t, m)
	arm(t, m)
	s.fail = true
	if m.Release() == nil || !m.Active() || !m.RecoveryRequired() {
		t.Fatal("release save failure reported success")
	}
	if id, err := m.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "logical-1" {
		t.Fatal("failed release erased checkpoint")
	}
	s.fail = false
	replay, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Active() || len(f.rules) == 0 {
		t.Fatal("failed release lost blocking after restart")
	}
	if err := replay.Release(); err != nil {
		t.Fatal(err)
	}
}
func bindJournal(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.BindLogicalSession(testSID, journalKey(), "logical-1"); err != nil {
		t.Fatal(err)
	}
}
func startJournalRuntime(t *testing.T, m *Manager) {
	t.Helper()
	bindJournal(t, m)
	arm(t, m)
	if err := m.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
		t.Fatal(err)
	}
}
func confirmJournalCleanup(t *testing.T, m *Manager) {
	t.Helper()
	ticket, err := m.BeginMaintenance(testSID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmOwnedCleanup(ticket); err != nil {
		t.Fatal(err)
	}
}

func TestJournalCrashReplayKeepsBindingAndRequiresConfirmedCleanup(t *testing.T) {
	m, s, f := testManager(t)
	startJournalRuntime(t, m)
	if !s.record.CleanupRequired || s.record.Version != 2 {
		t.Fatal("runtime preceded durable cleanup intent")
	}
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	id, err := restarted.ResumeLogicalSession(testSID, journalKey())
	if err != nil || id != "logical-1" {
		t.Fatal("lost logical session", id, err)
	}
	if !restarted.CleanupRequired() || restarted.Release() == nil || restarted.Arm(testSID, "account-1", "resource-1", testEntry, nil, false) == nil || restarted.AllowTunnel(42) == nil {
		t.Fatal("replay authorized ordinary release/rearm")
	}
	if err := restarted.Block(); err != nil {
		t.Fatal(err)
	}
	if !restarted.CleanupRequired() {
		t.Fatal("WFP block was treated as adapter cleanup proof")
	}
	confirmJournalCleanup(t, restarted)
	if restarted.CleanupRequired() {
		t.Fatal("confirmed cleanup not durable")
	}
	if id, err := restarted.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "logical-1" {
		t.Fatal("cleanup erased logical session")
	}
	arm(t, restarted)
	if restarted.AllowTunnel(42) == nil {
		t.Fatal("fresh Arm reused proof without runtime checkpoint")
	}
	if err := restarted.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
}

func TestJournalFullKeyAndOwnerCannotBeReplacedBeforeRelease(t *testing.T) {
	m, _, _ := testManager(t)
	bindJournal(t, m)
	for _, change := range []func(*engine.ResourceKey){func(k *engine.ResourceKey) { k.Environment = "other" }, func(k *engine.ResourceKey) { k.DataSource = "other" }, func(k *engine.ResourceKey) { k.AccountKey = "other" }, func(k *engine.ResourceKey) { k.Kind = "other" }, func(k *engine.ResourceKey) { k.SourceID = "other" }} {
		key := journalKey()
		change(&key)
		if id, err := m.ResumeLogicalSession(testSID, key); err == nil || id != "" {
			t.Fatal("cross-key checkpoint disclosed")
		}
		if m.BindLogicalSession(testSID, key, "logical-1") == nil {
			t.Fatal("replaced full key")
		}
	}
	if id, err := m.ResumeLogicalSession("S-1-5-21-9-9-9-1001", journalKey()); err == nil || id != "" {
		t.Fatal("other SID got checkpoint")
	}
	if m.BindLogicalSession(testSID, journalKey(), "logical-2") == nil {
		t.Fatal("GUI overrode service logical ID")
	}
	if err := m.Release(); err != nil {
		t.Fatal(err)
	}
	if id, err := m.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "" {
		t.Fatal("release retained checkpoint", id, err)
	}
}

func TestJournalWriteFailureAndStaleCleanupNeverClearPending(t *testing.T) {
	m, s, _ := testManager(t)
	bindJournal(t, m)
	arm(t, m)
	s.fail = true
	if m.BeginRuntime(testSID, journalKey(), "logical-1") == nil || s.record.CleanupRequired {
		t.Fatal("runtime admitted without write")
	}
	s.fail = false
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	arm(t, m)
	if err := m.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
		t.Fatal(err)
	}
	ticket, err := m.BeginMaintenance(testSID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	s.fail = true
	if m.ConfirmOwnedCleanup(ticket) == nil || !m.CleanupRequired() {
		t.Fatal("failed cleanup write cleared pending")
	}
	s.fail = false
	if err := m.ConfirmOwnedCleanup(ticket); err != nil {
		t.Fatal(err)
	}
	arm(t, m)
	if err := m.BeginRuntime(testSID, journalKey(), "logical-1"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.ConfirmOwnedCleanup(ticket), ErrRecoveryRequired) || !m.CleanupRequired() {
		t.Fatal("stale cleanup receipt erased new runtime")
	}
}

func TestJournalV1MigrationIsBlockingAndCannotInventFullSession(t *testing.T) {
	m, s, f := testManager(t)
	s.record = &intent{Version: 1, ProtectIntent: true, OwnerSID: testSID, AccountKey: "account-1", ResourceID: "resource-1", Mode: "tun", Since: "2026-10-12T00:00:00Z"}
	r, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if s.record.Version != 2 || !r.CleanupRequired() || !r.Active() {
		t.Fatal("v1 did not migrate blocked")
	}
	if _, err := r.ResumeLogicalSession(testSID, journalKey()); err == nil {
		t.Fatal("invented v1 logical ID")
	}
	if r.BindLogicalSession(testSID, journalKey(), "new-id") == nil || r.Release() == nil {
		t.Fatal("v1 recovery bypassed cleanup")
	}
	confirmJournalCleanup(t, r)
	if r.BindLogicalSession(testSID, journalKey(), "new-id") == nil {
		t.Fatal("legacy binding replaced without explicit release")
	}
	if err := r.Release(); err != nil {
		t.Fatal("same SID release required business login", err)
	}
}
