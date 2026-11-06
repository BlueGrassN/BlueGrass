package nativehost

import (
	"errors"
	"strings"
	"unicode"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/mactun"
	"bluegrass/native/core/winprotect"
)

// Implemented only by the authenticated service wrapper. Windows ownership is
// derived there; the private IPC request never contains an owner or logical ID.
type logicalSessionJournal interface {
	ResumeLogicalSession(engine.ResourceKey) (string, error)
	BindLogicalSession(engine.ResourceKey, string) error
}

func logicalSessionError(err error) string {
	if errors.Is(err, winprotect.ErrOwner) || errors.Is(err, mactun.ErrOwner) {
		return "OWNER_MISMATCH"
	}
	return "RECOVERY_REQUIRED"
}

// A journal ID carries continuity only. The caller must still bind it durably
// before a new controller attempt, and the engine must prepare a fresh session.
func (h *Host) selectLogicalSession(key engine.ResourceKey) (id string, resumed bool, journal logicalSessionJournal, code string) {
	if h.tunnel == nil {
		if h.proxy != nil && h.canRetryOwnedProxy(key) {
			return h.context.LogicalSessionID, false, nil, ""
		}
		return randomID(), false, nil, ""
	}
	journal, supported := h.tunnel.(logicalSessionJournal)
	if !supported {
		if h.tunnel.Active() {
			// Older in-process engines may retain an already-known session, but
			// cannot invent an identity for protection recovered from a restart.
			if h.context.ResourceKey != key || h.context.LogicalSessionID == "" {
				return "", false, nil, "RECOVERY_REQUIRED"
			}
			return h.context.LogicalSessionID, false, nil, ""
		}
		return randomID(), false, nil, ""
	}
	id, err := journal.ResumeLogicalSession(key)
	if err != nil {
		return "", false, nil, logicalSessionError(err)
	}
	if id == "" {
		if h.tunnel.Active() {
			return "", false, nil, "RECOVERY_REQUIRED"
		}
		return randomID(), false, journal, ""
	}
	// Native-generated IDs are bounded ASCII hex. Accept earlier opaque IDs,
	// but keep them representable as the controller's bounded counter epoch.
	if len(id) > 128 || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return "", false, nil, "RECOVERY_REQUIRED"
	}
	return id, h.context.LogicalSessionID != id || h.context.ResourceKey != key, journal, ""
}
