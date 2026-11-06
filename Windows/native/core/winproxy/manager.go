package winproxy

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	ProxyTypeDirect       uint32 = 1
	ProxyTypeProxy        uint32 = 2
	ProxyTypeAutoProxyURL uint32 = 4
	ProxyTypeAutoDetect   uint32 = 8
)

var (
	ErrOwnershipLost      = errors.New("OWNERSHIP_LOST")
	ErrRecoveryRequired   = errors.New("RECOVERY_REQUIRED")
	ErrVerificationFailed = errors.New("SETTINGS_VERIFICATION_FAILED")
	ErrInvalidSettings    = errors.New("INVALID_SETTINGS")
	ErrInvalidRecord      = errors.New("INVALID_RECORD")
	ErrUnsupported        = errors.New("winproxy: Windows is required")
)

type Settings struct {
	Flags                         uint32
	Server, Bypass, AutoConfigURL string
}

type Record struct {
	Version  int
	Original Settings
	Applied  Settings
	Phase    string
}

type Backend interface {
	Query() (Settings, error)
	Set(Settings) error
	Notify() error
}

type Store interface {
	Load() (*Record, error)
	Save(Record) error
	Delete() error
}

// Manager serializes one user's proxy transactions. Do not copy it.
type Manager struct {
	mu      sync.Mutex
	backend Backend
	store   Store
}

func NewWithDependencies(backend Backend, store Store) (*Manager, error) {
	if backend == nil || store == nil {
		return nil, errors.New("winproxy: backend and store are required")
	}
	return &Manager{backend: backend, store: store}, nil
}

func (m *Manager) Apply(server, bypass string) error {
	target := Settings{Flags: ProxyTypeDirect | ProxyTypeProxy, Server: server, Bypass: bypass}
	if !validTarget(target) {
		return ErrInvalidSettings
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.load()
	if err != nil {
		return err
	}
	current, err := m.backend.Query()
	if err != nil {
		return fmt.Errorf("query before apply: %w", err)
	}
	if r != nil {
		if current != r.Applied {
			return ErrOwnershipLost
		}
		if r.Phase != "applied" || target != r.Applied {
			return ErrRecoveryRequired
		}
		return nil
	}
	if !validSettings(current) {
		return ErrInvalidSettings
	}
	r = &Record{Version: 1, Original: current, Applied: target, Phase: "prepared"}
	if err := m.store.Save(*r); err != nil {
		return fmt.Errorf("save backup: %w", err)
	}
	if err := m.requireCurrent(current); err != nil {
		return err
	}
	if err := m.writeAndVerify(target); err != nil {
		return err
	}
	r.Phase = "applied"
	if err := m.store.Save(*r); err != nil {
		return fmt.Errorf("commit apply: %w", err)
	}
	return nil
}

func (m *Manager) Restore() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.load()
	if err != nil || r == nil {
		return err
	}
	current, err := m.backend.Query()
	if err != nil {
		return fmt.Errorf("query before restore: %w", err)
	}
	// A previous restore may have written the original value before its
	// notification, verification or record deletion failed. Never write again.
	if (r.Phase == "restoring" || r.Phase == "prepared") && current == r.Original {
		if err := m.notifyAndVerify(r.Original); err != nil {
			return err
		}
		return m.deleteRecord()
	}
	if current != r.Applied {
		return ErrOwnershipLost
	}
	r.Phase = "restoring"
	if err := m.store.Save(*r); err != nil {
		return fmt.Errorf("save restore intent: %w", err)
	}
	if err := m.requireCurrent(r.Applied); err != nil {
		return err
	}
	if err := m.writeAndVerify(r.Original); err != nil {
		return err
	}
	return m.deleteRecord()
}

// Recover is explicit, ownership-checked startup restoration. New never calls it.
func (m *Manager) Recover() error { return m.Restore() }

// Relinquish is an explicit release of obsolete ownership, never a restoration
// claim. It must not forget a backup while Windows still uses our applied value.
func (m *Manager) Relinquish() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.load()
	if err != nil || r == nil {
		return err
	}
	current, err := m.backend.Query()
	if err != nil {
		return err
	}
	if current == r.Applied {
		return ErrRecoveryRequired
	}
	if err := m.requireCurrent(current); err != nil {
		return err
	}
	return m.deleteRecord()
}

// State reports verified ownership of a committed applied record, not connectivity.
func (m *Manager) State() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.load()
	if err != nil || r == nil {
		return false, err
	}
	current, err := m.backend.Query()
	if err != nil {
		return false, fmt.Errorf("query state: %w", err)
	}
	if r.Phase == "restoring" && current == r.Original {
		return false, ErrRecoveryRequired
	}
	if current != r.Applied {
		return false, ErrOwnershipLost
	}
	if r.Phase != "applied" {
		return false, ErrRecoveryRequired
	}
	return true, nil
}

func (m *Manager) load() (*Record, error) {
	r, err := m.store.Load()
	if err != nil {
		return nil, fmt.Errorf("load recovery record: %w", err)
	}
	if r != nil && !validRecord(*r) {
		return nil, ErrInvalidRecord
	}
	return r, nil
}

func (m *Manager) requireCurrent(expected Settings) error {
	current, err := m.backend.Query()
	if err != nil {
		return fmt.Errorf("recheck ownership: %w", err)
	}
	if current != expected {
		return ErrOwnershipLost
	}
	return nil
}

func (m *Manager) writeAndVerify(target Settings) error {
	if err := m.backend.Set(target); err != nil {
		return fmt.Errorf("set proxy: %w", err)
	}
	return m.notifyAndVerify(target)
}

func (m *Manager) notifyAndVerify(expected Settings) error {
	if err := m.backend.Notify(); err != nil {
		return fmt.Errorf("notify proxy: %w", err)
	}
	current, err := m.backend.Query()
	if err != nil {
		return fmt.Errorf("verify proxy: %w", err)
	}
	if current != expected {
		return errors.Join(ErrVerificationFailed, ErrOwnershipLost)
	}
	return nil
}

func (m *Manager) deleteRecord() error {
	if err := m.store.Delete(); err != nil {
		return fmt.Errorf("delete recovery record: %w", err)
	}
	return nil
}

func validSettings(s Settings) bool {
	for _, v := range []string{s.Server, s.Bypass, s.AutoConfigURL} {
		if strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}

func validTarget(s Settings) bool {
	return validSettings(s) && strings.TrimSpace(s.Server) != "" &&
		s.Flags == ProxyTypeDirect|ProxyTypeProxy && s.AutoConfigURL == ""
}

func validRecord(r Record) bool {
	return r.Version == 1 && validSettings(r.Original) && validTarget(r.Applied) &&
		(r.Phase == "prepared" || r.Phase == "applied" || r.Phase == "restoring")
}
