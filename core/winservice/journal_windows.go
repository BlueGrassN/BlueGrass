//go:build windows

package winservice

import (
	"errors"

	"bluegrass/native/core/engine"
)

// ResumeLogicalSession returns only the authenticated owner's exact full-key
// checkpoint. It grants no forwarding/cleanup permission and never invents an ID.
func (e *protectedTunnel) ResumeLogicalSession(key engine.ResourceKey) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", errors.New("ENGINE_CLOSED")
	}
	return e.protection.ResumeLogicalSession(e.sid, key)
}

// BindLogicalSession accepts only a new or identical durable binding. The SID
// is exclusively the authenticated service owner, never supplied by a profile.
func (e *protectedTunnel) BindLogicalSession(key engine.ResourceKey, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if err := e.protection.BindLogicalSession(e.sid, key, id); err != nil {
		return err
	}
	e.boundKey, e.boundLogicalID = key, id
	return nil
}
