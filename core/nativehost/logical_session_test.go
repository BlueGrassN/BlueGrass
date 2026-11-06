package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/winprotect"
)

// Simulate the service's authenticated journal boundary, retaining only durable
// identity across new engine/host instances. No OS or journal file is touched.
type logicalJournalFixture struct {
	mu     sync.Mutex
	active bool
	owner  string
	key    engine.ResourceKey
	id     string
}
type logicalEngineFixture struct {
	journal                                        *logicalJournalFixture
	owner                                          string
	mu                                             sync.Mutex
	bound                                          string
	resumeCalls, bindCalls, prepareCalls, releases int
	bindErr                                        error
	prepared                                       chan struct{}
	proof                                          chan engine.ProbeResult
}

func newLogicalEngine(j *logicalJournalFixture, owner string) *logicalEngineFixture {
	return &logicalEngineFixture{journal: j, owner: owner, prepared: make(chan struct{}, 4), proof: make(chan engine.ProbeResult, 4)}
}
func (e *logicalEngineFixture) ResumeLogicalSession(key engine.ResourceKey) (string, error) {
	e.mu.Lock()
	e.resumeCalls++
	e.mu.Unlock()
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	j := e.journal
	if !j.active {
		return "", nil
	}
	if j.owner != e.owner {
		return "", winprotect.ErrOwner
	}
	if j.id == "" {
		return "", winprotect.ErrRecoveryRequired
	}
	if j.key != key {
		return "", winprotect.ErrOwner
	}
	return j.id, nil
}
func (e *logicalEngineFixture) BindLogicalSession(key engine.ResourceKey, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bindCalls++
	if e.bindErr != nil {
		return e.bindErr
	}
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	j := e.journal
	if j.active && (j.owner != e.owner || j.key != key || j.id != id) {
		return winprotect.ErrOwner
	}
	j.active, j.owner, j.key, j.id = true, e.owner, key, id
	e.bound = id
	return nil
}
func (e *logicalEngineFixture) Prepare(_ context.Context, p engine.Profile) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.prepareCalls++
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	if e.bound == "" || e.bound != e.journal.id || p.Key != e.journal.key || e.owner != e.journal.owner {
		return errors.New("unbound prepare")
	}
	e.prepared <- struct{}{}
	return nil
}
func (e *logicalEngineFixture) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	select {
	case p := <-e.proof:
		return p, nil
	case <-ctx.Done():
		return engine.ProbeResult{}, ctx.Err()
	}
}
func (*logicalEngineFixture) SetBusinessGate(bool) error { return nil }
func (*logicalEngineFixture) Stop(context.Context) error { return nil }
func (*logicalEngineFixture) Close() error               { return nil }
func (*logicalEngineFixture) Address() string            { return "" }
func (*logicalEngineFixture) VerifiedIPv4() string       { return "1.1.1.1" }
func (*logicalEngineFixture) Counters() (uint64, uint64) { return 7, 11 }
func (*logicalEngineFixture) SetAllowLAN(bool) error     { return nil }
func (e *logicalEngineFixture) Active() bool {
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	return e.journal.active
}
func (e *logicalEngineFixture) Release() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releases++
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	e.journal.active = false
	e.journal.id = ""
	return nil
}
func (e *logicalEngineFixture) calls() (int, int, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resumeCalls, e.bindCalls, e.prepareCalls, e.releases
}

func logicalConnect(t *testing.T, h *Host, e *logicalEngineFixture, id string) control.ControlContext {
	t.Helper()
	r := h.Handle(dnsRequest(t, id, "tun", nil))
	if !r.OK {
		t.Fatalf("Connect rejected: %s", r.Error)
	}
	select {
	case <-e.prepared:
	case <-time.After(time.Second):
		t.Fatal("Prepare did not follow journal binding")
	}
	s := h.controller.Snapshot()
	if s.VerifiedExitIPv4 != "" || s.Connection != "starting" && s.Connection != "connected" {
		t.Fatal("durable identity reused an observation or bypassed preparation")
	}
	return *s.Context
}
func logicalProve(t *testing.T, h *Host, e *logicalEngineFixture) {
	t.Helper()
	e.proof <- engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s := h.controller.Snapshot(); s.BusinessGateOpen && s.VerifiedExitIPv4 == "1.1.1.1" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fresh observation was not published")
}

func TestLogicalSessionRestartAndReplayRequireFreshPreparationAndObservation(t *testing.T) {
	j := &logicalJournalFixture{}
	e := newLogicalEngine(j, "sid-a")
	h := dnsHost(t, e, "tun")
	first := logicalConnect(t, h, e, "first")
	if j.id == "" || first.LogicalSessionID != j.id {
		t.Fatal("native ID was not durably bound")
	}
	logicalProve(t, h, e)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	e2 := newLogicalEngine(j, "sid-a")
	h2 := dnsHost(t, e2, "tun")
	hello := h2.Handle(Request{ID: "hello-after-restart", Command: "Hello"})
	if hello.Snapshot["connection"] != "paused" || hello.Snapshot["protectionIntent"] != true || hello.Snapshot["context"] != nil {
		t.Fatal("restart fabricated a connected context")
	}
	// Even replaying the same request ID after service restart requires a new
	// attempt and new proof; a durable ID is not a cached command receipt.
	second := logicalConnect(t, h2, e2, "first")
	if second.LogicalSessionID != first.LogicalSessionID || second.ResourceKey != first.ResourceKey || second.RuntimeEpoch == first.RuntimeEpoch || second.ClientEpoch == first.ClientEpoch || second.AttemptID == first.AttemptID {
		t.Fatal("restart did not preserve only logical identity")
	}
	logicalProve(t, h2, e2)
	traffic := responseTraffic(t, h2.Handle(Request{ID: "restarted-status", Command: "GetStatus"}))
	if !traffic.Available || traffic.Complete {
		t.Fatal("restart claimed complete counters without durable totals")
	}
	if r := h2.Handle(Request{ID: "pause", Command: "Pause"}); !r.OK {
		t.Fatal(r.Error)
	}
	third := logicalConnect(t, h2, e2, "replay")
	if third.LogicalSessionID != first.LogicalSessionID || third.AttemptID == second.AttemptID {
		t.Fatal("replay changed logical identity or reused attempt")
	}
	logicalProve(t, h2, e2)
	if r := h2.Handle(Request{ID: "release", Command: "Disconnect"}); !r.OK {
		t.Fatal(r.Error)
	}
	fourth := logicalConnect(t, h2, e2, "after-release")
	if fourth.LogicalSessionID == first.LogicalSessionID {
		t.Fatal("explicit release reused the retired logical session")
	}
}

func TestLogicalSessionRejectsLegacyAndMismatchedIdentityWithoutPrepare(t *testing.T) {
	r := dnsRequest(t, "resume", "tun", nil)
	var p engine.Profile
	_ = json.Unmarshal(r.Profile, &p)
	for _, variant := range []string{"legacy", "owner", "environment", "data-source", "account", "kind", "source", "bind-error"} {
		t.Run(variant, func(t *testing.T) {
			j := &logicalJournalFixture{active: true, owner: "sid-a", key: p.Key, id: "persisted-logical-id"}
			want := "OWNER_MISMATCH"
			switch variant {
			case "legacy":
				j.id = ""
				want = "RECOVERY_REQUIRED"
			case "owner":
				j.owner = "sid-b"
			case "environment":
				j.key.Environment = "other"
			case "data-source":
				j.key.DataSource = "other"
			case "account":
				j.key.AccountKey = "other"
			case "kind":
				j.key.Kind = "subscription"
			case "source":
				j.key.SourceID = "other"
			case "bind-error":
				want = "RECOVERY_REQUIRED"
			}
			e := newLogicalEngine(j, "sid-a")
			if variant == "bind-error" {
				e.bindErr = errors.New("private disk error")
			}
			h := dnsHost(t, e, "tun")
			reply := h.Handle(r)
			_, bind, prepare, releases := e.calls()
			if reply.OK || reply.Error != want || prepare != 0 || releases != 0 || variant != "bind-error" && bind != 0 || h.controller != nil || reply.Snapshot["businessGateOpen"] != false || reply.Snapshot["protectionIntent"] != true {
				t.Fatalf("recovery denial changed protected state: ok=%v code=%s", reply.OK, reply.Error)
			}
		})
	}
}

func TestLogicalSessionValidatesEligibilityBeforeJournalAccess(t *testing.T) {
	for _, variant := range []string{"expired", "calibrated-expired", "bad-key", "bad-entry", "bad-revision", "bad-schema"} {
		t.Run(variant, func(t *testing.T) {
			e := newLogicalEngine(&logicalJournalFixture{}, "sid-a")
			h := dnsHost(t, e, "tun")
			r := dnsRequest(t, "invalid", "tun", nil)
			var p engine.Profile
			_ = json.Unmarshal(r.Profile, &p)
			switch variant {
			case "expired":
				until := time.Now().Add(-time.Hour)
				p.ValidUntilUTC = &until
			case "calibrated-expired":
				until := time.Now().Add(time.Hour)
				server := until.Add(time.Hour)
				p.ValidUntilUTC = &until
				r.ServerTimeUTC = &server
			case "bad-key":
				p.Key.AccountKey = ""
			case "bad-entry":
				p.Entry.Port = 0
			case "bad-revision":
				p.ConfigVersion = 0
			}
			r.Profile, _ = json.Marshal(p)
			if variant == "bad-schema" {
				r.Profile = json.RawMessage(`{"mode":"tun"}`)
			}
			reply := h.Handle(r)
			resume, bind, prepare, _ := e.calls()
			if reply.OK || resume != 0 || bind != 0 || prepare != 0 || h.controller != nil {
				t.Fatal("invalid profile reached journal/controller")
			}
		})
	}
}

func TestLogicalSessionNoGUISuppliedIdentityOrUnsupportedRecovery(t *testing.T) {
	for _, field := range []string{"logicalSessionId", "resumeLogicalSessionId", "ownerSid"} {
		wire, _ := json.Marshal(map[string]any{"id": "override", "command": "Connect", field: "untrusted"})
		if _, err := Decode(wire); err == nil {
			t.Fatalf("caller supplied %s", field)
		}
	}
	e := newLogicalEngine(&logicalJournalFixture{}, "sid-a")
	private := dnsHost(t, e, "tun")
	override := dnsRequest(t, "context-override", "tun", nil)
	override.Context = &control.ControlContext{LogicalSessionID: "untrusted"}
	if r := private.Handle(override); r.OK || r.Error != "INVALID_PARAMETERS" {
		t.Fatal("Connect accepted a caller-supplied context")
	}
	if resume, bind, prepare, _ := e.calls(); resume != 0 || bind != 0 || prepare != 0 {
		t.Fatal("GUI override reached the journal")
	}
	h := dnsHost(t, &tunnelStub{active: true}, "tun")
	r := h.Handle(dnsRequest(t, "unsupported-recovery", "tun", nil))
	if r.OK || r.Error != "RECOVERY_REQUIRED" || h.controller != nil || r.Snapshot["protectionIntent"] != true {
		t.Fatal("missing durable identity downgraded to a new session")
	}
}
