package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInvalidClockCannotOpenGate(t *testing.T) {
	for _, kind := range []string{"wrong_epoch", "backwards_monotonic", "missing_time"} {
		t.Run(kind, func(t *testing.T) {
			c, f, clk := setup(t, false)
			apply(t, c, request(Connect, "start", 1))
			p := nextProbe(t, f)
			clk.mu.Lock()
			switch kind {
			case "wrong_epoch":
				clk.epoch = "runtime-old"
			case "backwards_monotonic":
				clk.mono = -1
			case "missing_time":
				clk.now = time.Time{}
			}
			clk.mu.Unlock()
			success(p)
			s := waitState(t, c, "failed")
			if s.BusinessGateOpen || s.ErrorCode != "INVALID_CLOCK" {
				t.Fatal("invalid clock opened gate")
			}
		})
	}
}
func TestWallClockRollbackCannotExtendDeadline(t *testing.T) {
	c, f, clk := setup(t, false)
	connect(t, c, f)
	clk.mu.Lock()
	clk.now = baseTime.Add(-24 * time.Hour)
	clk.mono = int64(time.Hour)
	clk.mu.Unlock()
	if err := c.CheckExpiry(); err != nil {
		t.Fatal(err)
	}
	if !c.Snapshot().Expired || c.Snapshot().BusinessGateOpen {
		t.Fatal("wall rollback extended lease")
	}
}
func TestCallerMutationCannotChangeAcceptedProfile(t *testing.T) {
	c, f, _ := setup(t, false)
	r := request(Connect, "start", 1)
	apply(t, c, r)
	p := nextProbe(t, f)
	r.Params.Profile.ExpectedExitIPv4 = "1.1.1.1"
	*r.Params.Profile.ValidUntilUTC = baseTime.Add(-time.Hour)
	success(p)
	s := waitState(t, c, "connected")
	if !s.ValidUntilUTC.Equal(baseTime.Add(time.Hour)) {
		t.Fatal("caller owns controller deadline")
	}
}
func TestUnknownFieldsAndMissingExpiryRejected(t *testing.T) {
	c, _, _ := setup(t, false)
	_ = c
	r := request(Connect, "decode", 1)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(data), `"params":{`, `"params":{"rawConfig":"evil",`, 1), strings.Replace(string(data), `"validUntilUtc":"2026-10-11T13:00:00Z",`, "", 1), string(data) + ` {}`} {
		_, err := DecodeRequest([]byte(bad))
		if err == nil {
			t.Fatal("unsafe wire request accepted")
		}
	}
	r.Params.Profile.ValidUntilUTC = nil
	data, _ = json.Marshal(r)
	got, err := DecodeRequest(data)
	if err != nil || got.Params.Profile.ValidUntilUTC != nil {
		t.Fatal("explicit null deadline rejected")
	}
}
func TestUpdateRevisionAndConfigurationInvalidateProbe(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	until := baseTime.Add(2 * time.Hour)
	r := request(ApplyResourceUpdate, "extend", 2)
	r.Params.Update = &ResourceUpdate{Revision: 2, ValidUntilUTC: &until}
	apply(t, c, r)
	before := f.history()
	r = request(ApplyResourceUpdate, "stale-detail", 3)
	old := baseTime.Add(-time.Hour)
	r.Params.Update = &ResourceUpdate{Revision: 1, ValidUntilUTC: &old}
	a, _ := c.Apply(context.Background(), r)
	if a.Disposition != "stale" || !c.Snapshot().BusinessGateOpen || len(before) != len(f.history()) {
		t.Fatal("old resource response overwrote current")
	}
	p := profile()
	p.ConfigVersion = 3
	p.ExpectedExitIPv4 = "1.1.1.1"
	r = request(ApplyResourceUpdate, "config", 4)
	r.Params.Update = &ResourceUpdate{Revision: 3, ValidUntilUTC: &until, Profile: &p}
	apply(t, c, r)
	s := c.Snapshot()
	if s.BusinessGateOpen || s.Connection != "paused" || !s.ProtectionIntent {
		t.Fatal("config change reused old verification")
	}
}
func TestSameSessionCannotSwitchResource(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	r := request(Connect, "different-resource", 2)
	r.Context.ResourceKey.SourceID = "2"
	r.Context.AttemptID = "attempt-b"
	r.Params.Profile.Key = r.Context.ResourceKey
	a, _ := c.Apply(context.Background(), r)
	if a.Disposition != "rejected" {
		t.Fatal("resource switch reused logical session")
	}
}
func TestStopFailureRetainsProtectionIntent(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	f.mu.Lock()
	f.fail = "stop"
	f.mu.Unlock()
	a, err := c.Apply(context.Background(), request(Disconnect, "stop-fails", 2))
	if err == nil || a.Disposition != "rejected" || !c.Snapshot().ProtectionIntent || c.Snapshot().BusinessGateOpen {
		t.Fatal("failed stop released intent")
	}
}
func TestCancelledRequestDoesNotReachEngine(t *testing.T) {
	c, f, _ := setup(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, err := c.Apply(ctx, request(Connect, "cancelled", 1))
	if err == nil || a.Disposition != "rejected" || len(f.history()) > 0 {
		t.Fatal("cancelled request affected engine")
	}
}
func TestNilEngineFailsClosed(t *testing.T) {
	_, _, clk := setup(t, false)
	if _, err := New(nil, clk, "runtime-a", owner()); err == nil {
		t.Fatal("missing native engine considered available")
	}
}

func TestLateRenewalCannotAvoidElapsedDeadline(t *testing.T) {
	c, f, clk := setup(t, false)
	connect(t, c, f)
	clk.advance(time.Hour)
	// Simulate a refresh winning the command lock before the queued timer.
	r := request(ApplyResourceUpdate, "late-renewal", 2)
	until := baseTime.Add(2 * time.Hour)
	r.Params.Update = &ResourceUpdate{Revision: 2, ValidUntilUTC: &until}
	apply(t, c, r)
	s := c.Snapshot()
	if s.Connection != "paused" || s.BusinessGateOpen || !s.ProtectionIntent {
		t.Fatal("renewal bypassed elapsed deadline")
	}
}

func TestWireRejectsDuplicateKeysAndMissingRequiredFields(t *testing.T) {
	r := request(Connect, "wire", 1)
	data, _ := json.Marshal(r)
	for _, bad := range []string{
		strings.Replace(string(data), `"command":"Connect"`, `"command":"Pause","command":"Connect"`, 1),
		strings.Replace(string(data), `"auth":{"username":"","password":""},`, "", 1),
		strings.Replace(string(data), `"params":`, `"Params":`, 1),
	} {
		if _, err := DecodeRequest([]byte(bad)); err == nil {
			t.Fatal("ambiguous or incomplete wire message accepted")
		}
	}
}
