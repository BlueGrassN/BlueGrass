package winproxy

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

var errInjected = errors.New("injected failure")

// These fakes never open sockets or access OS proxy settings.
type fakeBackend struct {
	current                      Settings
	queryErr, setErr, notifyErr  error
	sets, queries, notifications int
	onQuery                      func(int)
	onSet                        func(Settings)
	onNotify                     func()
}

func (b *fakeBackend) Query() (Settings, error) {
	b.queries++
	if b.onQuery != nil {
		b.onQuery(b.queries)
	}
	return b.current, b.queryErr
}
func (b *fakeBackend) Set(s Settings) error {
	b.sets++
	if b.onSet != nil {
		b.onSet(s)
	} else if b.setErr == nil {
		b.current = s
	}
	return b.setErr
}
func (b *fakeBackend) Notify() error {
	b.notifications++
	if b.onNotify != nil {
		b.onNotify()
	}
	return b.notifyErr
}

type fakeStore struct {
	record                      *Record
	loadErr, saveErr, deleteErr error
	onSave                      func(Record)
}

func (s *fakeStore) Load() (*Record, error) {
	if s.record == nil {
		return nil, s.loadErr
	}
	r := *s.record
	return &r, s.loadErr
}
func (s *fakeStore) Save(r Record) error {
	if s.onSave != nil {
		s.onSave(r)
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.record = &r
	return nil
}
func (s *fakeStore) Delete() error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.record = nil
	return nil
}

func originalSettings() Settings {
	return Settings{Flags: 15, Server: "old.invalid:3128", Bypass: "<local>;*.old.invalid", AutoConfigURL: "https://old.invalid/pac"}
}
func targetSettings() Settings {
	return Settings{Flags: 3, Server: "127.0.0.1:49152", Bypass: "<local>"}
}
func setup(t *testing.T) (*Manager, *fakeBackend, *fakeStore) {
	t.Helper()
	b, s := &fakeBackend{current: originalSettings()}, &fakeStore{}
	m, err := NewWithDependencies(b, s)
	if err != nil {
		t.Fatal(err)
	}
	return m, b, s
}
func apply(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Apply("127.0.0.1:49152", "<local>"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPersistsCompleteSnapshotBeforeWriteAndVerifies(t *testing.T) {
	m, b, s := setup(t)
	b.onSet = func(got Settings) {
		if s.record == nil || s.record.Original != originalSettings() || s.record.Applied != targetSettings() || s.record.Phase != "prepared" {
			t.Fatal("write occurred without complete durable prepared record")
		}
		b.current = got
	}
	apply(t, m)
	if b.current != targetSettings() || b.notifications != 1 || b.queries < 3 {
		t.Fatalf("settings not applied, notified and verified: %+v", b)
	}
	if s.record.Phase != "applied" {
		t.Fatal("not committed")
	}
	if active, err := m.State(); !active || err != nil {
		t.Fatalf("State=%v,%v", active, err)
	}
}

func TestRestoreRetainsOriginalPACAndAutoDetection(t *testing.T) {
	m, b, s := setup(t)
	apply(t, m)
	if err := m.Restore(); err != nil {
		t.Fatal(err)
	}
	if b.current != originalSettings() || s.record != nil || b.notifications != 2 {
		t.Fatal("incomplete restore")
	}
	if err := m.Restore(); err != nil {
		t.Fatal(err)
	}
	if active, err := m.State(); active || err != nil {
		t.Fatalf("State=%v,%v", active, err)
	}
}

func TestExternalChangesNeverOverwritten(t *testing.T) {
	changes := map[string]func(*Settings){
		"flags":    func(s *Settings) { s.Flags ^= 8 },
		"server":   func(s *Settings) { s.Server = "external.invalid:90" },
		"bypass":   func(s *Settings) { s.Bypass = "external.invalid" },
		"pac":      func(s *Settings) { s.AutoConfigURL = "https://external.invalid/pac" },
		"original": func(s *Settings) { *s = originalSettings() },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			m, b, s := setup(t)
			apply(t, m)
			change(&b.current)
			external := b.current
			record := *s.record
			for _, call := range []func() error{m.Restore, m.Recover, func() error { return m.Apply("127.0.0.1:49152", "<local>") }} {
				if err := call(); !errors.Is(err, ErrOwnershipLost) {
					t.Fatalf("want ownership loss, got %v", err)
				}
			}
			if active, err := m.State(); active || !errors.Is(err, ErrOwnershipLost) {
				t.Fatalf("State=%v,%v", active, err)
			}
			if b.current != external || b.sets != 1 || s.record == nil || *s.record != record {
				t.Fatal("external settings or recovery record changed")
			}
		})
	}
}

func TestApplyIdempotentButCannotReplaceOriginal(t *testing.T) {
	m, b, s := setup(t)
	apply(t, m)
	apply(t, m)
	if b.sets != 1 || s.record.Original != originalSettings() {
		t.Fatal("idempotent apply changed original")
	}
	if err := m.Apply("127.0.0.1:49153", ""); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("got %v", err)
	}
	if b.sets != 1 {
		t.Fatal("changed target without restore")
	}
}

func TestApplyFailuresRetainRecordAndReturnError(t *testing.T) {
	for _, stage := range []string{"load", "query", "save", "set", "notify", "verify", "commit"} {
		t.Run(stage, func(t *testing.T) {
			m, b, s := setup(t)
			switch stage {
			case "load":
				s.loadErr = errInjected
			case "query":
				b.queryErr = errInjected
			case "save":
				s.saveErr = errInjected
			case "set":
				b.setErr = errInjected
			case "notify":
				b.notifyErr = errInjected
			case "verify":
				b.onNotify = func() { b.current.Bypass = "external.invalid" }
			case "commit":
				s.onSave = func(r Record) {
					if r.Phase == "applied" {
						s.saveErr = errInjected
					}
				}
			}
			if err := m.Apply("127.0.0.1:49152", "<local>"); err == nil {
				t.Fatal("failure reported success")
			}
			if stage == "load" || stage == "query" || stage == "save" {
				if b.sets != 0 {
					t.Fatal("wrote without durable backup")
				}
			} else if s.record == nil {
				t.Fatal("lost recovery record")
			}
		})
	}
}

func TestRestoreFailuresNeverReportSuccess(t *testing.T) {
	for _, stage := range []string{"load", "query", "save", "set", "notify", "verify", "delete"} {
		t.Run(stage, func(t *testing.T) {
			m, b, s := setup(t)
			apply(t, m)
			switch stage {
			case "load":
				s.loadErr = errInjected
			case "query":
				b.queryErr = errInjected
			case "save":
				s.saveErr = errInjected
			case "set":
				b.setErr = errInjected
			case "notify":
				b.notifyErr = errInjected
			case "verify":
				b.onNotify = func() { b.current.Server = "external.invalid:91" }
			case "delete":
				s.deleteErr = errInjected
			}
			if err := m.Restore(); err == nil {
				t.Fatal("restore failure reported success")
			}
			if s.record == nil {
				t.Fatal("lost recovery record")
			}
			if (stage == "load" || stage == "query" || stage == "save") && b.sets != 1 {
				t.Fatal("restored after failed prerequisite")
			}
			if stage == "notify" || stage == "delete" {
				b.notifyErr = nil
				s.deleteErr = nil
				fresh, _ := NewWithDependencies(b, s)
				if err := fresh.Recover(); err != nil {
					t.Fatal(err)
				}
				if s.record != nil || b.sets != 2 {
					t.Fatal("recovery rewrote settings or left record")
				}
			}
		})
	}
}

func TestCrashRecoveryPreparedAndRestoring(t *testing.T) {
	for _, phase := range []string{"prepared", "applied", "restoring"} {
		t.Run(phase, func(t *testing.T) {
			m, b, s := setup(t)
			b.current = targetSettings()
			s.record = &Record{Version: 1, Original: originalSettings(), Applied: targetSettings(), Phase: phase}
			if err := m.Recover(); err != nil {
				t.Fatal(err)
			}
			if b.current != originalSettings() || s.record != nil {
				t.Fatal("incomplete crash recovery")
			}
		})
	}
}

func TestPreparedWithoutOwnershipIsRetained(t *testing.T) {
	m, b, s := setup(t)
	s.record = &Record{Version: 1, Original: originalSettings(), Applied: targetSettings(), Phase: "prepared"}
	b.current.Server = "external.invalid:8080"
	if err := m.Recover(); !errors.Is(err, ErrOwnershipLost) {
		t.Fatal(err)
	}
	if s.record == nil || b.sets != 0 {
		t.Fatal("unowned prepared transaction changed")
	}
}

func TestRechecksAfterDurableSave(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "restore"}[restore], func(t *testing.T) {
			m, b, s := setup(t)
			if restore {
				apply(t, m)
			}
			before := b.sets
			s.onSave = func(Record) { b.current.Server = "external.invalid:1234" }
			var err error
			if restore {
				err = m.Restore()
			} else {
				err = m.Apply("127.0.0.1:49152", "<local>")
			}
			if !errors.Is(err, ErrOwnershipLost) || b.sets != before || s.record == nil {
				t.Fatalf("missed competing writer: %v", err)
			}
		})
	}
}

func TestValidationAndCorruptRecordFailClosed(t *testing.T) {
	for _, server := range []string{"", "   ", "abc\x00def"} {
		m, b, s := setup(t)
		if err := m.Apply(server, ""); !errors.Is(err, ErrInvalidSettings) {
			t.Fatal(err)
		}
		if b.queries != 0 || s.record != nil {
			t.Fatal("invalid request caused side effects")
		}
	}
	m, b, s := setup(t)
	s.record = &Record{Version: 99, Phase: "applied"}
	if err := m.Restore(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatal(err)
	}
	if b.sets != 0 || s.record == nil {
		t.Fatal("corrupt record accepted")
	}
	if _, err := NewWithDependencies(nil, s); err == nil {
		t.Fatal("nil backend accepted")
	}
	if _, err := NewWithDependencies(b, nil); err == nil {
		t.Fatal("nil store accepted")
	}
}

func TestConcurrentApplyIsSerialized(t *testing.T) {
	m, b, s := setup(t)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := m.Apply("127.0.0.1:49152", "<local>"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if b.sets != 1 || s.record == nil || !reflect.DeepEqual(s.record.Original, originalSettings()) {
		t.Fatal("concurrent apply replaced backup")
	}
}

func TestPartialWriteFailureKeepsRecoverableSnapshot(t *testing.T) {
	m, b, s := setup(t)
	// Native setters may change state even when they ultimately report failure.
	b.setErr = errInjected
	b.onSet = func(value Settings) { b.current = value }
	if err := m.Apply("127.0.0.1:49152", "<local>"); !errors.Is(err, errInjected) {
		t.Fatalf("partial apply returned %v", err)
	}
	if s.record == nil || s.record.Phase != "prepared" {
		t.Fatal("partial write lost backup")
	}
	if active, err := m.State(); active || !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("State=%v,%v", active, err)
	}
	b.setErr = nil
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	if b.current != originalSettings() || s.record != nil {
		t.Fatal("partial write not recovered")
	}
}

func TestReadFailuresAfterSaveAndAfterWriteRetainRecord(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, afterWrite := range []bool{false, true} {
			m, b, s := setup(t)
			if restore {
				apply(t, m)
			}
			before := b.sets
			if afterWrite {
				b.onNotify = func() { b.queryErr = errInjected }
			} else {
				s.onSave = func(Record) { b.queryErr = errInjected }
			}
			var err error
			if restore {
				err = m.Restore()
			} else {
				err = m.Apply("127.0.0.1:49152", "<local>")
			}
			if !errors.Is(err, errInjected) || s.record == nil {
				t.Fatalf("read failure lost: %v", err)
			}
			if !afterWrite && b.sets != before {
				t.Fatal("wrote after failed ownership read")
			}
			if active, stateErr := m.State(); active || !errors.Is(stateErr, errInjected) {
				t.Fatalf("State=%v,%v", active, stateErr)
			}
		}
	}
}
