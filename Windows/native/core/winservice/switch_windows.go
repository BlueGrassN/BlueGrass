//go:build windows

package winservice

import (
	"errors"

	"bluegrass/native/core/engine"
)

// SwitchLogicalSession is invoked only after nativehost authenticates the exact
// old context and new profile. SID still comes from this pipe's trusted owner.
// It changes no eligibility/proof and never clears protection or FakeIP cache.
func (e *protectedTunnel) SwitchLogicalSession(oldKey, newKey engine.ResourceKey, newID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if e.boundLogicalID == "" || e.boundKey != oldKey || oldKey == newKey || newID == "" || newID == e.boundLogicalID ||
		oldKey.Environment != newKey.Environment || oldKey.DataSource != newKey.DataSource || oldKey.AccountKey != newKey.AccountKey {
		return errors.New("SESSION_SWITCH_REJECTED")
	}
	id, err := e.protection.ResumeLogicalSession(e.sid, oldKey)
	if err != nil || id != e.boundLogicalID {
		return errors.New("SESSION_SWITCH_REJECTED")
	}
	e.stopLocalWatcher()
	if err := e.block(); err != nil {
		return err
	}
	if err := e.protection.SwitchLogicalSession(oldKey, newKey, newID); err != nil {
		return err
	}
	e.boundKey, e.boundLogicalID = newKey, newID
	return nil
}
