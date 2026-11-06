package control

import (
	"bluegrass/native/core/engine"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func changeWireField(t *testing.T, r ControlRequest, path string, value any) []byte {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	object := root
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		child, ok := object[part].(map[string]any)
		if !ok {
			t.Fatalf("missing test object %s", part)
		}
		object = child
	}
	object[parts[len(parts)-1]] = value
	data, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNullUnavailableCannotRemoveDeadline(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	before := c.Snapshot()
	calls := len(f.history())
	r := request(ApplyResourceUpdate, "null-unavailable", 2)
	r.Params.Update = &ResourceUpdate{Revision: 2, ValidUntilUTC: nil}
	decoded, err := DecodeRequest(changeWireField(t, r, "params.update.unavailable", nil))
	if err == nil {
		a, applyErr := c.Apply(context.Background(), decoded)
		t.Fatalf("null unavailable accepted: disposition=%s applyError=%v deadlineRemoved=%t", a.Disposition, applyErr, c.Snapshot().ValidUntilUTC == nil)
	}
	after := c.Snapshot()
	if after.ValidUntilUTC == nil || !after.ValidUntilUTC.Equal(*before.ValidUntilUTC) || after.Connection != "connected" || len(f.history()) != calls {
		t.Fatal("invalid wire update changed live deadline or engine")
	}
}

func TestWireRejectsNullNonnullableFields(t *testing.T) {
	r := request(Connect, "null-fields", 1)
	for _, path := range []string{
		"context", "requestId", "sequence", "command", "params", "params.profile",
		"context.schemaVersion", "context.clientEpoch", "context.runtimeEpoch", "context.logicalSessionId", "context.attemptId", "context.resourceKey",
		"context.resourceKey.environment", "context.resourceKey.dataSource", "context.resourceKey.accountKey", "context.resourceKey.kind", "context.resourceKey.sourceId",
		"params.profile.key", "params.profile.protocol", "params.profile.entry", "params.profile.auth", "params.profile.expectedExitIpv4", "params.profile.configVersion", "params.profile.configFingerprint", "params.profile.mode",
		"params.profile.key.environment", "params.profile.key.dataSource", "params.profile.key.accountKey", "params.profile.key.kind", "params.profile.key.sourceId",
		"params.profile.entry.host", "params.profile.entry.port", "params.profile.auth.username", "params.profile.auth.password",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := DecodeRequest(changeWireField(t, r, path, nil)); err == nil {
				t.Fatal("nonnullable field accepted null")
			}
		})
	}
	u := request(ApplyResourceUpdate, "null-update", 2)
	p := profile()
	u.Params.Update = &ResourceUpdate{Revision: 2, Profile: &p}
	for _, path := range []string{"params.update", "params.update.revision", "params.update.unavailable", "params.update.profile", "params.update.profile.auth.username"} {
		t.Run(path, func(t *testing.T) {
			if _, err := DecodeRequest(changeWireField(t, u, path, nil)); err == nil {
				t.Fatal("nonnullable update field accepted null")
			}
		})
	}
	for _, cmd := range []Command{Hello, GetStatus} {
		t.Run(string(cmd)+"/minimal_context", func(t *testing.T) {
			data := []byte(`{"context":{"schemaVersion":1,"clientEpoch":"client-a"},"requestId":"handshake","sequence":1,"command":"` + string(cmd) + `","params":{}}`)
			decoded, err := DecodeRequest(data)
			if err != nil || decoded.Command != cmd || decoded.Context.AttemptID != "" {
				t.Fatal("unbound handshake with omitted optional context rejected")
			}
		})
		h := request(cmd, "handshake", 1)
		for _, path := range []string{"context.schemaVersion", "context.clientEpoch", "context.runtimeEpoch"} {
			t.Run(string(cmd)+"/"+path, func(t *testing.T) {
				if _, err := DecodeRequest(changeWireField(t, h, path, nil)); err == nil {
					t.Fatal("handshake scalar accepted null")
				}
			})
		}
	}
}

func TestUpdateRequiresJSONBooleanAndAllowsNullDeadline(t *testing.T) {
	r := request(ApplyResourceUpdate, "typed-update", 2)
	r.Params.Update = &ResourceUpdate{Revision: 2, ValidUntilUTC: nil}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"null", nil}, {"string", "false"}, {"number", 0}, {"array", []bool{false}}, {"object", map[string]bool{"value": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRequest(changeWireField(t, r, "params.update.unavailable", tc.value)); err == nil {
				t.Fatal("non-boolean unavailable accepted")
			}
		})
	}
	for _, unavailable := range []bool{false, true} {
		name := "false"
		if unavailable {
			name = "true"
		}
		t.Run(name, func(t *testing.T) {
			c, f, _ := setup(t, false)
			connect(t, c, f)
			decoded, err := DecodeRequest(changeWireField(t, r, "params.update.unavailable", unavailable))
			if err != nil {
				t.Fatal("JSON boolean with explicit null deadline rejected")
			}
			apply(t, c, decoded)
			s := c.Snapshot()
			want := "connected"
			if unavailable {
				want = "paused"
			}
			if s.ValidUntilUTC != nil || s.Connection != want || s.BusinessGateOpen == unavailable {
				t.Fatal("valid boolean/null-deadline update applied incorrectly")
			}
		})
	}
}

func TestPrepareFailureDoesNotClaimRequestedMode(t *testing.T) {
	c, f, _ := setup(t, false)
	f.fail = "prepare"
	r := request(Connect, "prepare-fails", 1)
	r.Params.Profile.Mode = "tun"
	a := apply(t, c, r)
	s := waitState(t, c, "failed")
	if a.Snapshot.ActualMode != "unknown" || s.ActualMode != "unknown" {
		t.Fatalf("unprepared mode reported: ack=%s failed=%s", a.Snapshot.ActualMode, s.ActualMode)
	}
	if s.BusinessGateOpen || !s.ProtectionIntent {
		t.Fatal("failed preparation released traffic or protection intent")
	}
}

type controlledPrepare struct {
	*fakeEngine
	entered chan context.Context
	finish  chan error
}

func (f *controlledPrepare) Prepare(ctx context.Context, _ engine.Profile) error {
	defer f.enter("prepare")()
	f.entered <- ctx
	// Deliberately allow late success, even after cancellation, to test ownership.
	select {
	case err := <-f.finish:
		return err
	case <-f.shutdown:
		return errors.New("test shutdown")
	}
}

func setupControlledPrepare(t *testing.T) (*Controller, *controlledPrepare) {
	t.Helper()
	f := &controlledPrepare{fakeEngine: &fakeEngine{probes: make(chan probeCall, 2), shutdown: make(chan struct{})}, entered: make(chan context.Context, 2), finish: make(chan error, 2)}
	c, err := New(f, &fakeClock{now: baseTime, epoch: "runtime-a"}, "runtime-a", owner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(f.shutdown)
		_ = c.Close()
		if f.overlap.Load() {
			t.Error("engine overlap")
		}
	})
	return c, f
}

func awaitPrepare(t *testing.T, f *controlledPrepare) context.Context {
	t.Helper()
	select {
	case ctx := <-f.entered:
		return ctx
	case <-time.After(2 * time.Second):
		t.Fatal("Prepare not entered")
		return nil
	}
}

func TestActualModePublishedOnlyAfterPrepareSuccess(t *testing.T) {
	c, f := setupControlledPrepare(t)
	r := request(Connect, "prepare-mode", 1)
	r.Params.Profile.Mode = "tun"
	a := apply(t, c, r)
	awaitPrepare(t, f)
	if a.Snapshot.ActualMode != "unknown" || c.Snapshot().ActualMode != "unknown" {
		t.Fatal("pending Prepare claimed requested mode")
	}
	f.finish <- nil
	p := nextProbe(t, f.fakeEngine)
	s := c.Snapshot()
	if s.ActualMode != "tun" || s.Connection != "connected" || !s.BusinessGateOpen {
		t.Fatal("successful Prepare mode not reported independently of probe")
	}
	success(p)
	s = waitState(t, c, "connected")
	if s.ActualMode != "tun" {
		t.Fatal("verified mode lost")
	}
}

func TestLatePrepareCannotPublishModeForNewAttempt(t *testing.T) {
	c, f := setupControlledPrepare(t)
	r := request(Connect, "old-prepare", 1)
	r.Params.Profile.Mode = "tun"
	apply(t, c, r)
	old := awaitPrepare(t, f)
	next := request(Connect, "new-prepare", 2)
	next.Context.AttemptID = "attempt-b"
	done := make(chan CommandAck, 1)
	go func() { a, _ := c.Apply(context.Background(), next); done <- a }()
	select {
	case <-old.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old Prepare not cancelled")
	}
	f.finish <- nil
	select {
	case a := <-done:
		if a.Disposition != "applied" {
			t.Fatal(a.Disposition)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new attempt blocked")
	}
	awaitPrepare(t, f)
	if s := c.Snapshot(); s.ActualMode != "unknown" || s.Context.AttemptID != "attempt-b" {
		t.Fatal("late Prepare published an unverified mode")
	}
	f.finish <- nil
	p := nextProbe(t, f.fakeEngine)
	if c.Snapshot().ActualMode != "system_proxy" {
		t.Fatal("new successful Prepare did not publish its own mode")
	}
	success(p)
	waitState(t, c, "connected")
}
