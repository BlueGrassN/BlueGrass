package winprotect

import (
	"errors"
	"math"
	"time"

	"bluegrass/native/core/engine"
)

// SessionCheckpoint contains no credentials, endpoint, LUID or proof. Revision
// also binds a cleanup receipt: a late completion cannot clear a newer runtime.
type SessionCheckpoint struct {
	OwnerSID         string
	ResourceKey      engine.ResourceKey
	LogicalSessionID string
	Phase            string
	CleanupRequired  bool
	Revision         uint64
}

func validResourceKey(k engine.ResourceKey) bool {
	return validID(k.Environment) && validID(k.DataSource) && validID(k.AccountKey) && validID(k.Kind) && validID(k.SourceID)
}

// saveState runs with mu held. Memory advances only after durable commit.
func (m *Manager) saveState(next intent) error {
	if m.state.Revision == math.MaxUint64 {
		return errors.New("protection journal revision exhausted")
	}
	next.Version, next.Revision = 2, m.state.Revision+1
	if err := m.store.Save(next); err != nil {
		return err
	}
	m.state = next
	return nil
}
func (m *Manager) checkpoint() SessionCheckpoint {
	return SessionCheckpoint{m.state.OwnerSID, m.state.ResourceKey, m.state.LogicalSessionID, m.state.Phase, m.state.CleanupRequired, m.state.Revision}
}
func (m *Manager) SessionCheckpoint() (SessionCheckpoint, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return SessionCheckpoint{}, false, ErrClosed
	}
	return m.checkpoint(), m.state.ProtectIntent, nil
}
func (m *Manager) CleanupRequired() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.CleanupRequired
}

// ResumeLogicalSession reveals only a same-owner/full-key identifier. Returning
// it grants no cleanup, eligibility, connection or forwarding permission.
func (m *Manager) ResumeLogicalSession(sid string, key engine.ResourceKey) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", ErrClosed
	}
	if !validSID(sid) || !validResourceKey(key) {
		return "", ErrOwner
	}
	if !m.state.ProtectIntent {
		return "", nil
	}
	if m.state.OwnerSID != sid {
		return "", ErrOwner
	}
	if m.state.LogicalSessionID == "" {
		return "", ErrRecoveryRequired
	}
	if m.state.ResourceKey != key {
		return "", ErrOwner
	}
	return m.state.LogicalSessionID, nil
}

// Bind never replaces an unreleased logical session, including across upgrade.
// A matching dirty binding is readable/idempotent; BeginRuntime still rejects
// it until the service confirms exact cleanup using its current receipt.
func (m *Manager) BindLogicalSession(sid string, key engine.ResourceKey, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !validSID(sid) || !validResourceKey(key) || !validID(id) {
		return ErrOwner
	}
	if m.state.ProtectIntent {
		if m.state.OwnerSID != sid {
			return ErrOwner
		}
		if m.state.LogicalSessionID == "" {
			return ErrRecoveryRequired
		}
		if m.state.ResourceKey != key || m.state.LogicalSessionID != id {
			return ErrOwner
		}
		return nil
	}
	return m.saveState(intent{Version: 2, ProtectIntent: true, OwnerSID: sid, AccountKey: key.AccountKey, ResourceID: key.SourceID,
		Mode: "tun", Since: time.Now().UTC().Format(time.RFC3339Nano), ResourceKey: key, LogicalSessionID: id, Phase: "blocked"})
}

// BeginRuntime must commit before runtime construction, after a fresh full Arm.
func (m *Manager) BeginRuntime(sid string, key engine.ResourceKey, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.state.ProtectIntent || m.state.OwnerSID != sid || m.state.ResourceKey != key || m.state.LogicalSessionID != id || id == "" {
		return ErrOwner
	}
	if m.state.CleanupRequired || !m.armed || m.state.Recovery {
		return ErrRecoveryRequired
	}
	next := m.state
	next.CleanupRequired = true
	next.Phase = "running"
	if err := m.saveState(next); err != nil {
		m.armed = false
		m.state.Recovery = true
		return err
	}
	m.runtimeOwned = true
	return nil
}

// BeginMaintenance is trusted service-only, not a client acknowledgment. Even
// failed cleanup or upgrade must leave the receipt pending across a restart.
func (m *Manager) BeginMaintenance(sid string) (SessionCheckpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return SessionCheckpoint{}, ErrClosed
	}
	if !m.state.ProtectIntent || !validSID(sid) || m.state.OwnerSID != sid {
		return SessionCheckpoint{}, ErrOwner
	}
	next := m.state
	next.CleanupRequired = true
	if next.LogicalSessionID != "" {
		next.Phase = "maintenance"
	} else {
		next.Phase = "legacy"
	}
	if err := m.saveState(next); err != nil {
		return SessionCheckpoint{}, err
	}
	m.armed = false
	return m.checkpoint(), nil
}

// ConfirmOwnedCleanup is called only after exact owned runtime/adapter cleanup
// and successful WFP Block. Ordinary Block alone never supplies this evidence.
func (m *Manager) ConfirmOwnedCleanup(receipt SessionCheckpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if !m.state.ProtectIntent || !m.state.CleanupRequired || receipt != m.checkpoint() || !validSID(receipt.OwnerSID) || m.armed || !m.rebindReady || m.policy.Tunnel != 0 || m.policy.LocalTunnel != 0 {
		return ErrRecoveryRequired
	}
	next := m.state
	next.CleanupRequired = false
	if next.LogicalSessionID != "" {
		next.Phase = "blocked"
	}
	if err := m.saveState(next); err != nil {
		return err
	}
	m.runtimeOwned = false
	return nil
}
