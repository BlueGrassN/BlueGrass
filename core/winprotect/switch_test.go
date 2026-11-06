package winprotect

import (
	"errors"
	"testing"

	"bluegrass/native/core/engine"
)

type sessionSwitcher interface {
	SwitchLogicalSession(engine.ResourceKey, engine.ResourceKey, string) error
}

func switchManager(t *testing.T, m *Manager) sessionSwitcher {
	t.Helper()
	s, ok := any(m).(sessionSwitcher)
	if !ok {
		t.Fatal("explicit protected switch is missing")
	}
	return s
}
func nextJournalKey() engine.ResourceKey { k := journalKey(); k.SourceID = "resource-2"; return k }

func TestJournalSwitchRequiresExactConfirmedCleanupAndPreservesIntent(t *testing.T) {
	m, s, f := testManager(t)
	sw := switchManager(t, m)
	startJournalRuntime(t, m)
	if err := m.AllowTunnel(42); err != nil {
		t.Fatal(err)
	}
	if sw.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2") == nil {
		t.Fatal("switched live runtime")
	}
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	if sw.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2") == nil {
		t.Fatal("Block substituted for runtime cleanup")
	}
	confirmJournalCleanup(t, m)
	if err := sw.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2"); err != nil {
		t.Fatal(err)
	}
	if !m.Active() || !s.record.ProtectIntent || s.record.OwnerSID != testSID || len(f.rules) == 0 || s.record.ResourceKey != nextJournalKey() || s.record.LogicalSessionID != "logical-2" {
		t.Fatal("switch released protection or lost new key")
	}
	if permits(f.rules, packet{addr: testEntry.Addr().String(), remote: testEntry.Port(), protocol: 6, app: "service.exe", luid: 7}) {
		t.Fatal("switch retained old entry permission")
	}
	if m.AllowTunnel(42) == nil || m.AllowUDPRelay(testEntry) == nil {
		t.Fatal("switch retained old proof")
	}
	r, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := r.ResumeLogicalSession(testSID, nextJournalKey()); err != nil || id != "logical-2" {
		t.Fatal("switch lost after crash", id, err)
	}
	if _, err := r.ResumeLogicalSession(testSID, journalKey()); !errors.Is(err, ErrOwner) {
		t.Fatal("old key still resumed")
	}
}

func TestJournalSwitchRejectsOwnerScopeAndWriteFailureKeepsOldBinding(t *testing.T) {
	m, s, f := testManager(t)
	sw := switchManager(t, m)
	bindJournal(t, m)
	arm(t, m)
	if err := m.Block(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*engine.ResourceKey){func(k *engine.ResourceKey) { k.AccountKey = "other" }, func(k *engine.ResourceKey) { k.Environment = "other" }, func(k *engine.ResourceKey) { k.DataSource = "other" }} {
		key := nextJournalKey()
		change(&key)
		if sw.SwitchLogicalSession(journalKey(), key, "logical-2") == nil {
			t.Fatal("switch changed owner scope")
		}
	}
	if sw.SwitchLogicalSession(nextJournalKey(), journalKey(), "logical-2") == nil || sw.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-1") == nil {
		t.Fatal("stale old binding or reused logical ID admitted")
	}
	s.fail = true
	if sw.SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2") == nil || !m.Active() || !m.RecoveryRequired() || s.record.ResourceKey != journalKey() || s.record.LogicalSessionID != "logical-1" || len(f.rules) == 0 {
		t.Fatal("failed write lost old binding/block")
	}
	s.fail = false
	r, err := newManager("service.exe", "ui.exe", s, f, m.network, m.verifyTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := r.ResumeLogicalSession(testSID, journalKey()); err != nil || id != "logical-1" {
		t.Fatal("write failure replay lost old binding")
	}
	f.fail = true
	if switchManager(t, r).SwitchLogicalSession(journalKey(), nextJournalKey(), "logical-2") == nil || s.record.LogicalSessionID != "logical-1" {
		t.Fatal("failed filter transaction committed new binding")
	}
}
