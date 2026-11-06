package nativehost

import (
	"errors"
	"reflect"
	"testing"

	"bluegrass/native/core/winproxy"
)

type repairStub struct {
	calls                               []string
	restoreErr, relinquishErr, stateErr error
	active                              bool
}

func (m *repairStub) Restore() error { m.calls = append(m.calls, "restore"); return m.restoreErr }
func (m *repairStub) Relinquish() error {
	m.calls = append(m.calls, "relinquish")
	return m.relinquishErr
}
func (m *repairStub) State() (bool, error) {
	m.calls = append(m.calls, "state")
	return m.active, m.stateErr
}

func TestRepairProxyRequiresVerifiedClearState(t *testing.T) {
	failure := errors.New("storage unavailable")
	for _, tc := range []struct {
		name      string
		manager   repairStub
		preserved bool
		err       error
		calls     []string
	}{
		{"restored", repairStub{}, false, nil, []string{"restore", "state"}},
		{"external preserved", repairStub{restoreErr: errors.Join(winproxy.ErrOwnershipLost, winproxy.ErrVerificationFailed)}, true, nil, []string{"restore", "relinquish", "state"}},
		{"restore failed", repairStub{restoreErr: failure}, false, failure, []string{"restore"}},
		{"relinquish failed", repairStub{restoreErr: winproxy.ErrOwnershipLost, relinquishErr: failure}, false, failure, []string{"restore", "relinquish"}},
		{"still active", repairStub{active: true}, false, winproxy.ErrRecoveryRequired, []string{"restore", "state"}},
		{"state unreadable", repairStub{stateErr: failure}, false, failure, []string{"restore", "state"}},
		{"preservation unconfirmed", repairStub{restoreErr: winproxy.ErrOwnershipLost, active: true}, false, winproxy.ErrRecoveryRequired, []string{"restore", "relinquish", "state"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preserved, err := repairProxy(&tc.manager)
			if preserved != tc.preserved || !errors.Is(err, tc.err) || !reflect.DeepEqual(tc.manager.calls, tc.calls) {
				t.Fatalf("got preserved=%v err=%v calls=%v", preserved, err, tc.manager.calls)
			}
		})
	}
}

type repairBackend struct {
	settings winproxy.Settings
	writes   int
}

func (b *repairBackend) Query() (winproxy.Settings, error) { return b.settings, nil }
func (b *repairBackend) Set(s winproxy.Settings) error     { b.writes++; b.settings = s; return nil }
func (*repairBackend) Notify() error                       { return nil }

type repairStore struct{ record *winproxy.Record }

func (s *repairStore) Load() (*winproxy.Record, error) {
	if s.record == nil {
		return nil, nil
	}
	r := *s.record
	return &r, nil
}
func (s *repairStore) Save(r winproxy.Record) error { s.record = &r; return nil }
func (s *repairStore) Delete() error                { s.record = nil; return nil }

func TestRepairProxyRestoresExactOriginalOrPreservesExternal(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "external"}[external], func(t *testing.T) {
			original := winproxy.Settings{Flags: winproxy.ProxyTypeAutoProxyURL | winproxy.ProxyTypeAutoDetect, Server: "original:90", Bypass: "*.example", AutoConfigURL: "https://example.test/proxy.pac"}
			b, store := &repairBackend{settings: original}, &repairStore{}
			manager, err := winproxy.NewWithDependencies(b, store)
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.Apply("127.0.0.1:3210", "localhost"); err != nil {
				t.Fatal(err)
			}
			want := original
			if external {
				want = winproxy.Settings{Flags: winproxy.ProxyTypeDirect}
				b.settings = want
			}
			b.writes = 0
			preserved, err := repairProxy(manager)
			if err != nil || preserved != external || b.settings != want || store.record != nil {
				t.Fatalf("repair: preserved=%v err=%v backend=%+v journal=%+v", preserved, err, b, store.record)
			}
			if external && b.writes != 0 {
				t.Fatal("external proxy was written")
			}
			if _, err = repairProxy(manager); err != nil {
				t.Fatal("repeat repair failed", err)
			}
		})
	}
}
