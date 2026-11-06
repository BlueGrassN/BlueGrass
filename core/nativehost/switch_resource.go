package nativehost

import (
	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
)

type logicalSessionSwitcher interface {
	SwitchLogicalSession(engine.ResourceKey, engine.ResourceKey, string) error
}

type switchContextRecovery struct {
	Version         int                    `json:"version"`
	Reason          string                 `json:"reason"`
	RequestID       string                 `json:"requestId"`
	PreviousContext control.ControlContext `json:"previousContext"`
	ResourceKey     engine.ResourceKey     `json:"resourceKey"`
}

func retainedSwitchProtection(snapshot map[string]any, runtime string) bool {
	if _, present := snapshot["context"]; present {
		return false
	}
	return snapshot["runtimeEpoch"] == runtime && snapshot["connection"] == "paused" && snapshot["actualMode"] == "tun" &&
		snapshot["protection"] == "wfp_blocking" && snapshot["protectionIntent"] == true && snapshot["gateKnown"] == true &&
		snapshot["businessGateOpen"] == false && snapshot["systemProxyApplied"] == false && snapshot["proxyRecoveryRequired"] == false
}

// The proxy manager separately verifies ownership. Reusing its transaction is
// allowed only for a paused/failed, known-closed attempt of this exact owner/key.
func (h *Host) canRetryOwnedProxy(key engine.ResourceKey) bool {
	if h.controller == nil || h.context.ResourceKey != key || h.context.LogicalSessionID == "" ||
		h.owner.Environment != key.Environment || h.owner.DataSource != key.DataSource || h.owner.AccountKey != key.AccountKey || h.owner.ClientEpoch != h.context.ClientEpoch {
		return false
	}
	s := h.controller.Snapshot()
	return s.Context != nil && *s.Context == h.context && s.ProtectionIntent && s.GateKnown && !s.BusinessGateOpen &&
		(s.Connection == "paused" || s.Connection == "failed")
}

// A resource switch is scoped to the exact current authenticated owner/context.
// It never supplies a Windows SID or accepts a caller-selected new logical ID.
func (h *Host) validateResourceSwitch(r Request, p engine.Profile) (logicalSessionSwitcher, string) {
	if r.Context == nil || h.controller == nil || h.context.LogicalSessionID == "" {
		return nil, "INVALID_CONTEXT"
	}
	s := h.controller.Snapshot()
	if *r.Context != h.context || s.Context == nil || *s.Context != h.context {
		return nil, "STALE_CONTEXT"
	}
	old := h.context.ResourceKey
	if p.Key.Environment != old.Environment || p.Key.DataSource != old.DataSource || p.Key.AccountKey != old.AccountKey ||
		h.owner.Environment != old.Environment || h.owner.DataSource != old.DataSource || h.owner.AccountKey != old.AccountKey || h.owner.ClientEpoch != h.context.ClientEpoch {
		return nil, "OWNER_MISMATCH"
	}
	if p.Key == old {
		return nil, "INVALID_PARAMETERS"
	}
	if !s.ProtectionIntent {
		return nil, "INVALID_CONTEXT"
	}
	if h.tunnel != nil {
		switcher, ok := h.tunnel.(logicalSessionSwitcher)
		if !ok {
			return nil, "UNSUPPORTED_COMMAND"
		}
		return switcher, ""
	}
	return nil, ""
}
