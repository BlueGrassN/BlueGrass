package nativehost

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
)

func TestSwitchContextRecoveryMarkerBindsFailedReplyAndDoesNotPersist(t *testing.T) {
	h, e, _, _ := switchHost(t, "tun")
	if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	r := switchRequest(t, h, "commit-then-expire", "tun", "resource-B")
	previous := *r.Context
	var p engine.Profile
	_ = json.Unmarshal(r.Profile, &p)
	until := time.Now().UTC().Add(time.Hour)
	p.ValidUntilUTC = &until
	r.Profile, _ = json.Marshal(p)
	e.afterSwitch = func() {
		_ = h.clock.Calibrate(until.Add(time.Minute))
		// The accepted old context must be a value frozen before engine calls,
		// never a later read of the caller's mutable Request.Context pointer.
		r.Context.AttemptID = "mutated-after-admission"
	}
	reply := h.Handle(r)
	if reply.OK || reply.ID != r.ID || reply.Error != "ELIG-003" {
		t.Fatal("expected failed post-commit reply")
	}
	for key, expected := range map[string]any{
		"runtimeEpoch": previous.RuntimeEpoch, "connection": "paused", "actualMode": "tun",
		"protection": "wfp_blocking", "protectionIntent": true, "gateKnown": true,
		"businessGateOpen": false, "systemProxyApplied": false, "proxyRecoveryRequired": false,
	} {
		if reply.Snapshot[key] != expected {
			t.Fatalf("recovery snapshot has incorrect %s", key)
		}
	}
	if _, present := reply.Snapshot["context"]; present {
		t.Fatal("recovery snapshot retained a context")
	}
	data, _ := json.Marshal(reply.Snapshot["contextRecovery"])
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 5 {
		t.Fatal("missing or malformed one-reply context recovery marker")
	}
	var marker struct {
		Version         int                    `json:"version"`
		Reason          string                 `json:"reason"`
		RequestID       string                 `json:"requestId"`
		PreviousContext control.ControlContext `json:"previousContext"`
		ResourceKey     engine.ResourceKey     `json:"resourceKey"`
	}
	if json.Unmarshal(data, &marker) != nil || marker.Version != 1 || marker.Reason != "resource_switch_committed" ||
		marker.RequestID != r.ID || marker.PreviousContext != previous || marker.ResourceKey != p.Key {
		t.Fatal("context recovery marker lost its frozen request/owner/resource binding")
	}
	for _, command := range []string{"GetStatus", "Hello", "Unsupported"} {
		later := h.Handle(Request{ID: "later-" + command, Command: command})
		if _, present := later.Snapshot["contextRecovery"]; present {
			t.Fatal("context recovery marker escaped its failed SwitchResource reply")
		}
	}
	p.ValidUntilUTC = nil
	r.Profile, _ = json.Marshal(p)
	r.Command, r.ID, r.Context = "Connect", "fresh-source", nil
	reconnected := h.Handle(r)
	if !reconnected.OK || reconnected.Snapshot["contextRecovery"] != nil {
		t.Fatal("recovery marker leaked into the fresh Connect reply")
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
}

func TestSwitchContextRecoveryMarkerAbsentForOtherOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "journal-failure", "stale-context", "same-resource"} {
		t.Run(outcome, func(t *testing.T) {
			h, e, _, _ := switchHost(t, "tun")
			if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
				t.Fatal(r.Error)
			}
			proveSwitch(t, h, nextSwitchProbe(t, e))
			r := switchRequest(t, h, "switch", "tun", "resource-B")
			switch outcome {
			case "journal-failure":
				e.switchErr = errors.New("private failure")
			case "stale-context":
				r.Context.AttemptID = "stale"
			case "same-resource":
				r = switchRequest(t, h, "same-resource", "tun", h.context.ResourceKey.SourceID)
			}
			reply := h.Handle(r)
			if reply.OK != (outcome == "success") {
				t.Fatal("unexpected switch outcome")
			}
			if _, present := reply.Snapshot["contextRecovery"]; present {
				t.Fatal("non-recovery outcome emitted a context recovery marker")
			}
			if reply.OK {
				proveSwitch(t, h, nextSwitchProbe(t, e))
			}
		})
	}
}

func TestSwitchContextRecoveryMarkerRequiresRetainedProtection(t *testing.T) {
	h, e, _, _ := switchHost(t, "tun")
	if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	r := switchRequest(t, h, "unconfirmed-protection", "tun", "resource-B")
	var p engine.Profile
	_ = json.Unmarshal(r.Profile, &p)
	until := time.Now().UTC().Add(time.Hour)
	p.ValidUntilUTC = &until
	r.Profile, _ = json.Marshal(p)
	e.afterSwitch = func() {
		_ = h.clock.Calibrate(until.Add(time.Minute))
		// Called by the fixture while it holds its journal lock. A broken engine
		// cannot authorize recovery by reporting no retained protection.
		e.journal.active = false
	}
	reply := h.Handle(r)
	if reply.OK || reply.Error != "ELIG-003" || reply.Snapshot["contextRecovery"] != nil {
		t.Fatal("unconfirmed protection emitted a recovery authorization")
	}
}
