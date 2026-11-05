package nativehost

import (
	"encoding/json"
	"testing"
	"time"

	"bluegrass/native/core/engine"
)

func connectedClockHost(t *testing.T) (*Host, time.Time) {
	t.Helper()
	h, e := reviewHost(t)
	anchor := time.Now().UTC().Add(time.Hour)
	until := anchor.Add(5 * time.Minute)
	var p engine.Profile
	if err := json.Unmarshal(reviewProfile("clock-resource"), &p); err != nil {
		t.Fatal(err)
	}
	p.ValidUntilUTC = &until
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if reply := h.Handle(Request{ID: "connect-clock", Command: "Connect", Profile: data, ServerTimeUTC: &anchor}); !reply.OK {
		t.Fatal(reply.Error)
	}
	select {
	case <-e.opened:
	case <-time.After(time.Second):
		t.Fatal("verified gate did not open")
	}
	return h, anchor
}

func clockUpdate(t *testing.T, id string, revision int, server, until time.Time) Request {
	t.Helper()
	data, err := json.Marshal(map[string]any{"revision": revision, "validUntilUtc": until, "unavailable": false})
	if err != nil {
		t.Fatal(err)
	}
	return Request{ID: id, Command: "ApplyResourceUpdate", Update: data, ServerTimeUTC: &server}
}

func TestResourceClockUpdateChecksOldDeadlineBeforeExtension(t *testing.T) {
	h, anchor := connectedClockHost(t)
	corrected, renewed := anchor.Add(10*time.Minute), anchor.Add(time.Hour)
	reply := h.Handle(clockUpdate(t, "renew-after-old-expiry", 2, corrected, renewed))
	if !reply.OK {
		t.Fatalf("calibrated resource update rejected: %s", reply.Error)
	}
	s := h.controller.Snapshot()
	if s.Time.EffectiveUTC.Before(corrected) || s.Connection != "paused" || s.BusinessGateOpen || !s.GateKnown || !s.ProtectionIntent {
		t.Fatalf("new server time did not close the expired attempt: %+v", s)
	}
	if s.ValidUntilUTC == nil || !s.ValidUntilUTC.Equal(renewed) {
		t.Fatal("renewed deadline was not recorded for a fresh connection")
	}
}

func TestResourceClockUpdateAdvancesLiveClockAndNeverRollsItBack(t *testing.T) {
	h, anchor := connectedClockHost(t)
	corrected, renewed := anchor.Add(time.Minute), anchor.Add(time.Hour)
	if reply := h.Handle(clockUpdate(t, "fresh-clock", 2, corrected, renewed)); !reply.OK {
		t.Fatal(reply.Error)
	}
	before := h.controller.Snapshot()
	if before.Time.EffectiveUTC.Before(corrected) || !before.BusinessGateOpen {
		t.Fatal("live update did not advance time while preserving eligibility")
	}
	if reply := h.Handle(clockUpdate(t, "older-clock", 3, anchor, renewed)); !reply.OK {
		t.Fatal(reply.Error)
	}
	after := h.controller.Snapshot()
	if after.Time.EffectiveUTC.Before(before.Time.EffectiveUTC) || !after.BusinessGateOpen {
		t.Fatal("older server sample rolled time back or stopped an eligible session")
	}
}

func TestResourceClockUpdateExpiresEvenWhenRevisionIsStale(t *testing.T) {
	h, anchor := connectedClockHost(t)
	corrected, renewed := anchor.Add(10*time.Minute), anchor.Add(time.Hour)
	reply := h.Handle(clockUpdate(t, "stale-revision-fresh-clock", 1, corrected, renewed))
	if reply.OK || reply.Error != "STALE_RESOURCE_REVISION" {
		t.Fatalf("expected revision rejection, got %+v", reply)
	}
	s := h.controller.Snapshot()
	if s.BusinessGateOpen || !s.Expired || !s.ProtectionIntent || !s.ValidUntilUTC.Equal(anchor.Add(5*time.Minute)) {
		t.Fatal("stale revision suppressed expiry under the newly trusted time")
	}
}

func TestResourceClockUpdateRejectsInvalidClockWithoutApplyingDeadline(t *testing.T) {
	h, anchor := connectedClockHost(t)
	reply := h.Handle(clockUpdate(t, "invalid-clock", 2, time.Time{}, anchor.Add(time.Hour)))
	if reply.OK || reply.Error != "INVALID_CLOCK" {
		t.Fatalf("invalid clock not rejected: %+v", reply)
	}
	s := h.controller.Snapshot()
	if !s.BusinessGateOpen || !s.ValidUntilUTC.Equal(anchor.Add(5*time.Minute)) {
		t.Fatal("invalid calibration changed the current resource deadline")
	}
}
