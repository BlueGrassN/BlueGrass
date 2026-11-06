package winproxy

import (
	"errors"
	"testing"
)

func TestRecoverPreparedWithoutWrite(t *testing.T) {
	m, b, s := setup(t)
	b.setErr = errInjected
	if m.Apply("127.0.0.1:49152", "<local>") == nil {
		t.Fatal("injected write failure not reported")
	}
	b.setErr = nil
	if err := m.Recover(); err != nil {
		t.Fatalf("unwritten prepared transaction cannot recover: %v", err)
	}
	if b.current != originalSettings() || b.sets != 1 || s.record != nil {
		t.Fatal("recovery must clear an unwritten intent without changing settings")
	}
}

func TestRecoverPreparedDoesNotOverwriteExternalChange(t *testing.T) {
	m, b, s := setup(t)
	s.record = &Record{Version: 1, Original: originalSettings(), Applied: targetSettings(), Phase: "prepared"}
	b.current.Server = "new.external.invalid:1234"
	if !errors.Is(m.Recover(), ErrOwnershipLost) || b.sets != 0 || s.record == nil {
		t.Fatal("unwritten intent must not overwrite a later user's choice")
	}
}

func TestRelinquishPreservesExternalSettings(t *testing.T) {
	m, b, s := setup(t)
	apply(t, m)
	b.current.Server = "external.invalid:8123"
	external := b.current
	if err := m.Relinquish(); err != nil {
		t.Fatal(err)
	}
	if b.current != external || b.sets != 1 || s.record != nil {
		t.Fatal("relinquishment overwrote external settings or kept obsolete ownership")
	}
	if err := m.Recover(); err != nil {
		t.Fatal("released ownership still prevents startup")
	}
}

func TestRelinquishCannotDiscardOwnedBackup(t *testing.T) {
	m, _, s := setup(t)
	apply(t, m)
	if !errors.Is(m.Relinquish(), ErrRecoveryRequired) || s.record == nil {
		t.Fatal("owned backup discarded without restoration")
	}
}
