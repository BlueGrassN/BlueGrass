package control

import (
	"bluegrass/native/core/clock"
	"bluegrass/native/core/engine"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	mono  int64
	epoch string
}

func (f *fakeClock) Read() clock.TimeReading {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clock.TimeReading{EffectiveUTC: f.now, MonotonicNanos: f.mono, RuntimeEpoch: f.epoch, Confidence: clock.Server}
}
func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	f.mono += int64(d)
}

type probeCall struct {
	ctx   context.Context
	reply chan engine.ProbeResult
}
type fakeEngine struct {
	mu           sync.Mutex
	calls        []string
	probes       chan probeCall
	shutdown     chan struct{}
	ignoreCancel bool
	fail         string
	active       atomic.Int32
	overlap      atomic.Bool
}

func (f *fakeEngine) enter(name string) func() {
	if f.active.Add(1) != 1 {
		f.overlap.Store(true)
	}
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	return func() { f.active.Add(-1) }
}
func (f *fakeEngine) err(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail == name {
		return errors.New("FAKE_PRIVATE_VALUE must not escape")
	}
	return nil
}
func (f *fakeEngine) Prepare(ctx context.Context, p engine.Profile) error {
	defer f.enter("prepare")()
	return f.err("prepare")
}
func (f *fakeEngine) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	defer f.enter("probe")()
	if err := f.err("probe"); err != nil {
		return engine.ProbeResult{}, err
	}
	call := probeCall{ctx, make(chan engine.ProbeResult, 1)}
	f.probes <- call
	var cancel <-chan struct{} = ctx.Done()
	if f.ignoreCancel {
		cancel = nil
	}
	select {
	case r := <-call.reply:
		return r, nil
	case <-cancel:
		return engine.ProbeResult{}, ctx.Err()
	case <-f.shutdown:
		return engine.ProbeResult{}, errors.New("test shutdown")
	}
}
func (f *fakeEngine) SetBusinessGate(open bool) error {
	name := "close"
	if open {
		name = "open"
	}
	defer f.enter(name)()
	return f.err(name)
}
func (f *fakeEngine) Stop(ctx context.Context) error { defer f.enter("stop")(); return f.err("stop") }
func (f *fakeEngine) history() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}
func owner() Owner { return Owner{"test", "fake-v1", "account-a", "client-a"} }
func contextA() ControlContext {
	return ControlContext{1, "client-a", "runtime-a", "session-a", "attempt-a", ResourceKey{Environment: "test", DataSource: "fake-v1", AccountKey: "account-a", Kind: "subscription", SourceID: "1"}}
}
func profile() ConnectionProfile {
	until := baseTime.Add(time.Hour)
	return ConnectionProfile{Key: contextA().ResourceKey, Protocol: "socks5", Entry: engine.Entry{Host: "entry.invalid", Port: 1080}, ExpectedExitIPv4: "8.8.8.8", ValidUntilUTC: &until, ConfigVersion: 1, ConfigFingerprint: "fixture-v1", Mode: "system_proxy"}
}
func request(cmd Command, id string, seq uint64) ControlRequest {
	r := ControlRequest{Context: contextA(), Command: cmd, RequestID: id, Sequence: seq}
	if cmd == Connect {
		p := profile()
		r.Params.Profile = &p
	}
	return r
}
func setup(t *testing.T, ignore bool) (*Controller, *fakeEngine, *fakeClock) {
	t.Helper()
	f := &fakeEngine{probes: make(chan probeCall, 16), shutdown: make(chan struct{}), ignoreCancel: ignore}
	clk := &fakeClock{now: baseTime, epoch: "runtime-a"}
	c, err := New(f, clk, "runtime-a", owner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(f.shutdown)
		_ = c.Close()
		if f.overlap.Load() {
			t.Error("engine calls overlapped")
		}
	})
	return c, f, clk
}
func apply(t *testing.T, c *Controller, r ControlRequest) CommandAck {
	t.Helper()
	a, err := c.Apply(context.Background(), r)
	if err != nil || a.Disposition != "applied" {
		t.Fatalf("command %s: disposition=%s error=%v code=%s", r.Command, a.Disposition, err, a.ErrorCode)
	}
	return a
}
func nextProbe(t *testing.T, f *fakeEngine) probeCall {
	t.Helper()
	select {
	case p := <-f.probes:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("probe not started")
		return probeCall{}
	}
}
func waitState(t *testing.T, c *Controller, want string) NativeSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := c.Snapshot()
		if s.Connection == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %s got %s (%s)", want, s.Connection, s.ErrorCode)
		}
		time.Sleep(time.Millisecond)
	}
}
func success(p probeCall) {
	p.reply <- engine.ProbeResult{IPv4: "8.8.8.8", Authenticated: true, Reachable: true}
}
func connect(t *testing.T, c *Controller, f *fakeEngine) {
	t.Helper()
	apply(t, c, request(Connect, "connect", 1))
	success(nextProbe(t, f))
	c.workers.Wait()
	waitState(t, c, "connected")
}

func TestGateOpenBeforeAdvisoryIPv4Observation(t *testing.T) {
	for _, tc := range []struct {
		name, ip              string
		auth, reach, observed bool
	}{{"same", "8.8.8.8", true, true, true}, {"different", "1.1.1.1", true, true, true}, {"IPv6", "2001:db8::1", true, true, false}, {"mapped", "::ffff:8.8.8.8", true, true, false}, {"invalid", "junk", true, true, false}, {"auth", "8.8.8.8", false, true, false}, {"reach", "8.8.8.8", true, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, _ := setup(t, false)
			apply(t, c, request(Connect, "connect", 1))
			p := nextProbe(t, f)
			if s := c.Snapshot(); !s.BusinessGateOpen || s.Connection != "connected" {
				t.Fatal("connection delayed by exit probe")
			}
			h := f.history()
			if !reflect.DeepEqual(h, []string{"close", "prepare", "open", "probe"}) {
				t.Fatalf("unsafe order %v", h)
			}
			p.reply <- engine.ProbeResult{IPv4: tc.ip, Authenticated: tc.auth, Reachable: tc.reach}
			c.workers.Wait()
			s := waitState(t, c, "connected")
			if !s.BusinessGateOpen || !s.ProtectionIntent || (s.VerifiedExitIPv4 != "") != tc.observed {
				t.Fatal("incorrect gate or protection intent")
			}
		})
	}
}
func TestLateProbeCannotReleaseNewAttempt(t *testing.T) {
	c, f, _ := setup(t, true)
	apply(t, c, request(Connect, "first", 1))
	old := nextProbe(t, f)
	newer := request(Connect, "second", 2)
	newer.Context.AttemptID = "attempt-b"
	done := make(chan CommandAck, 1)
	go func() { a, _ := c.Apply(context.Background(), newer); done <- a }()
	select {
	case <-old.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old attempt not cancelled")
	}
	success(old)
	select {
	case a := <-done:
		if a.Disposition != "applied" {
			t.Fatal(a.Disposition)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new connect blocked")
	}
	current := nextProbe(t, f)
	if s := c.Snapshot(); !s.BusinessGateOpen || s.VerifiedExitIPv4 != "" {
		t.Fatal("replacement was blocked or inherited a late observation")
	}
	opens := 0
	for _, v := range f.history() {
		if v == "open" {
			opens++
		}
	}
	if opens != 2 {
		t.Fatal("a late probe changed the gate", f.history())
	}
	success(current)
	s := waitState(t, c, "connected")
	if s.Context.AttemptID != "attempt-b" {
		t.Fatal("wrong context")
	}
}
func TestExpiryPausesWithoutRelease(t *testing.T) {
	c, f, clk := setup(t, false)
	connect(t, c, f)
	clk.advance(time.Hour)
	if err := c.CheckExpiry(); err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot()
	if s.Connection != "paused" || !s.Expired || s.BusinessGateOpen || !s.ProtectionIntent || s.Protection != "intent_blocking" {
		t.Fatalf("expiry state: %+v", s)
	}
	h := f.history()
	if h[len(h)-2] != "close" || h[len(h)-1] != "stop" {
		t.Fatalf("expiry side effects %v", h)
	}
}
func TestStopRequiresCurrentOwner(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	before := f.history()
	for _, mutate := range []func(*ControlRequest){func(r *ControlRequest) { r.Context.ClientEpoch = "old" }, func(r *ControlRequest) { r.Context.ResourceKey.AccountKey = "other" }, func(r *ControlRequest) { r.Context.AttemptID = "old" }, func(r *ControlRequest) { r.Context.RuntimeEpoch = "old" }} {
		r := request(Disconnect, "bad", 2)
		mutate(&r)
		a, _ := c.Apply(context.Background(), r)
		if a.Disposition == "applied" {
			t.Fatal("nonowner stopped session")
		}
	}
	if !reflect.DeepEqual(before, f.history()) || !c.Snapshot().BusinessGateOpen {
		t.Fatal("rejected stop changed engine")
	}
	apply(t, c, request(Disconnect, "stop", 2))
	s := c.Snapshot()
	if s.Connection != "disconnected" || s.ProtectionIntent || s.BusinessGateOpen {
		t.Fatal("owner disconnect failed")
	}
}
func TestDuplicateRequestIsIdempotent(t *testing.T) {
	c, f, _ := setup(t, false)
	r := request(Connect, "once", 1)
	a := apply(t, c, r)
	p := nextProbe(t, f)
	before := f.history()
	b := apply(t, c, r)
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(before, f.history()) {
		t.Fatal("duplicate changed result or engine")
	}
	success(p)
	waitState(t, c, "connected")
	d := apply(t, c, r)
	if !reflect.DeepEqual(a, d) {
		t.Fatal("duplicate acknowledgment changed after completion")
	}
}
func TestSameRequestDifferentPayloadRejected(t *testing.T) {
	c, f, _ := setup(t, false)
	r := request(Connect, "same", 1)
	apply(t, c, r)
	nextProbe(t, f)
	r.Params.Profile.ExpectedExitIPv4 = "1.1.1.1"
	a, _ := c.Apply(context.Background(), r)
	if a.Disposition != "rejected" || a.ErrorCode != "REQUEST_CONFLICT" {
		t.Fatalf("conflict not rejected: %s %s", a.Disposition, a.ErrorCode)
	}
}
func TestUpdateExpiryCannotReviveCancelledAttempt(t *testing.T) {
	c, f, clk := setup(t, true)
	apply(t, c, request(Connect, "start", 1))
	p := nextProbe(t, f)
	clk.advance(time.Hour)
	done := make(chan error, 1)
	go func() { done <- c.CheckExpiry() }()
	select {
	case <-p.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expiry didn't cancel probe")
	}
	success(p)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	until := baseTime.Add(2 * time.Hour)
	r := request(ApplyResourceUpdate, "extend", 2)
	r.Params.Update = &ResourceUpdate{Revision: 2, ValidUntilUTC: &until}
	apply(t, c, r)
	s := c.Snapshot()
	if s.BusinessGateOpen || s.Connection != "paused" {
		t.Fatal("renewal revived cancelled attempt")
	}
	r = request(Connect, "reuse-attempt", 3)
	a, _ := c.Apply(context.Background(), r)
	if a.Disposition != "stale" {
		t.Fatal("cancelled attempt identity reused")
	}
}
func TestUntaggedCounterIsIgnored(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	s := TrafficSnapshot{CounterEpoch: "count-a", Sequence: 1, MonotonicNanos: 1, UploadBytes: "9007199254740993", DownloadBytes: "18446744073709551615", Available: true, Complete: true}
	if c.AcceptTraffic(s) {
		t.Fatal("untagged accepted")
	}
	s.Context = contextA()
	before := c.Snapshot().EventSequence
	if !c.AcceptTraffic(s) {
		t.Fatal("tagged counter rejected")
	}
	got := c.Snapshot()
	if got.Traffic.DownloadBytes != "18446744073709551615" || got.Traffic.UploadBytes != "9007199254740993" || got.EventSequence != before {
		t.Fatal("precision lost or event sequence polluted")
	}
	if c.AcceptTraffic(s) {
		t.Fatal("duplicate sample accepted")
	}
	s.Sequence = 2
	s.MonotonicNanos = 2
	s.DownloadBytes = "18446744073709551616"
	if c.AcceptTraffic(s) {
		t.Fatal("overflow accepted")
	}
	s.DownloadBytes = "1"
	if c.AcceptTraffic(s) {
		t.Fatal("counter reset without new epoch accepted")
	}
	s.CounterEpoch = "count-b"
	if !c.AcceptTraffic(s) {
		t.Fatal("new counter epoch rejected")
	}
	s.CounterEpoch = "count-a"
	s.Sequence = 3
	s.MonotonicNanos = 3
	if c.AcceptTraffic(s) {
		t.Fatal("retired counter epoch accepted")
	}
}
func TestProfileLegacyExitIgnoredWithoutEntryFallback(t *testing.T) {
	for _, ip := range []string{"", "bad", "2001:db8::1", "::ffff:8.8.8.8", "127.0.0.1", "0.0.0.0", "224.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			c, f, _ := setup(t, false)
			r := request(Connect, "invalid", 1)
			r.Params.Profile.Entry.Host = "8.8.8.8"
			r.Params.Profile.ExpectedExitIPv4 = ip
			apply(t, c, r)
			p := nextProbe(t, f)
			if s := c.Snapshot(); !s.BusinessGateOpen || s.VerifiedExitIPv4 != "" {
				t.Fatal("legacy expected exit gated or entry became the observation")
			}
			p.reply <- engine.ProbeResult{}
			c.workers.Wait()
		})
	}
}
func TestPauseCancelsLateProbeAndRetainsProtection(t *testing.T) {
	c, f, _ := setup(t, true)
	apply(t, c, request(Connect, "start", 1))
	p := nextProbe(t, f)
	done := make(chan CommandAck, 1)
	go func() { a, _ := c.Apply(context.Background(), request(Pause, "pause", 2)); done <- a }()
	select {
	case <-p.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("not cancelled")
	}
	success(p)
	a := <-done
	if a.Disposition != "applied" {
		t.Fatal(a.Disposition)
	}
	s := c.Snapshot()
	if s.Connection != "paused" || s.BusinessGateOpen || !s.ProtectionIntent {
		t.Fatal("late probe released pause")
	}
}
func TestSequenceAndContextAreEnforced(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	r := request(Pause, "old-seq", 1)
	a, _ := c.Apply(context.Background(), r)
	if a.Disposition != "stale" {
		t.Fatal("old sequence accepted")
	}
	r = request(Connect, "wrong-resource", 2)
	r.Context.ResourceKey.SourceID = "2"
	a, _ = c.Apply(context.Background(), r)
	if a.Disposition != "rejected" {
		t.Fatal("profile key mismatch accepted")
	}
	r = request(Pause, "old-session", 2)
	r.Context.LogicalSessionID = "old"
	a, _ = c.Apply(context.Background(), r)
	if a.Disposition != "stale" {
		t.Fatal("old session accepted")
	}
}
func TestNullExpiryHasNoDeadline(t *testing.T) {
	c, f, clk := setup(t, false)
	r := request(Connect, "null", 1)
	r.Params.Profile.ValidUntilUTC = nil
	apply(t, c, r)
	success(nextProbe(t, f))
	waitState(t, c, "connected")
	clk.advance(365 * 24 * time.Hour)
	if err := c.CheckExpiry(); err != nil {
		t.Fatal(err)
	}
	if !c.Snapshot().BusinessGateOpen || c.Snapshot().ValidUntilUTC != nil {
		t.Fatal("invented deadline")
	}
}
func TestHelloDoesNotRequireAttemptOrTakeOwnership(t *testing.T) {
	c, _, _ := setup(t, false)
	r := ControlRequest{Context: ControlContext{SchemaVersion: 1, ClientEpoch: "client-a"}, RequestID: "hello", Sequence: 1, Command: Hello}
	a := apply(t, c, r)
	if a.Snapshot.RuntimeEpoch != "runtime-a" || a.Snapshot.Context != nil {
		t.Fatal("incorrect hello")
	}
	r.Context.ClientEpoch = "other"
	r.RequestID = "takeover"
	r.Sequence = 2
	a, _ = c.Apply(context.Background(), r)
	if a.Disposition != "stale" {
		t.Fatal("hello took ownership")
	}
}
func TestRebindRejectsOldOwnerAndCrossAccount(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	next := owner()
	next.AccountKey = "other"
	if c.RebindOwner(context.Background(), owner(), next) == nil {
		t.Fatal("cross-account takeover")
	}
	next = owner()
	next.ClientEpoch = "client-b"
	if err := c.RebindOwner(context.Background(), owner(), next); err != nil {
		t.Fatal(err)
	}
	a, _ := c.Apply(context.Background(), request(Disconnect, "old-stop", 2))
	if a.Disposition != "stale" || !c.Snapshot().ProtectionIntent || c.Snapshot().BusinessGateOpen {
		t.Fatal("old owner accepted or protection lost")
	}
	if c.RebindOwner(context.Background(), next, owner()) == nil {
		t.Fatal("old client epoch reused")
	}
}
func TestExpiryTimerRunsWithoutUI(t *testing.T) {
	f := &fakeEngine{probes: make(chan probeCall, 1), shutdown: make(chan struct{})}
	clk := clock.New("runtime-a", baseTime, clock.Server)
	c, err := New(f, clk, "runtime-a", owner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(f.shutdown); _ = c.Close() })
	r := request(Connect, "timer", 1)
	until := baseTime.Add(80 * time.Millisecond)
	r.Params.Profile.ValidUntilUTC = &until
	apply(t, c, r)
	success(nextProbe(t, f))
	waitState(t, c, "connected")
	// The timer publishes paused before acquiring engineMu to close the gate.
	// Wait for the complete expiry result, not that intermediate snapshot.
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := c.Snapshot()
		if s.Connection == "paused" && s.Expired && s.GateKnown && !s.BusinessGateOpen && s.ProtectionIntent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timer did not enforce expiry: connection=%s expired=%v gateKnown=%v gateOpen=%v intent=%v", s.Connection, s.Expired, s.GateKnown, s.BusinessGateOpen, s.ProtectionIntent)
		}
		time.Sleep(time.Millisecond)
	}
	history := f.history()
	if len(history) < 2 || !reflect.DeepEqual(history[len(history)-2:], []string{"close", "stop"}) {
		t.Fatalf("timer did not close and stop engine: %v", history)
	}
}
func TestFailuresAreSanitizedAndDoNotClaimProtection(t *testing.T) {
	for _, fail := range []string{"close", "prepare", "open"} {
		t.Run(fail, func(t *testing.T) {
			c, f, _ := setup(t, false)
			f.fail = fail
			a, err := c.Apply(context.Background(), request(Connect, "fail", 1))
			if err != nil && strings.Contains(err.Error(), "FAKE_PRIVATE_VALUE") {
				t.Fatal("error leaked credential")
			}
			if strings.Contains(a.ErrorCode, "FAKE_PRIVATE_VALUE") {
				t.Fatal("ack leaked credential")
			}
			s := waitState(t, c, "failed")
			if s.BusinessGateOpen || !s.ProtectionIntent || s.Protection == "enforced" {
				t.Fatal("failure claimed live protection")
			}
		})
	}
}
func TestConcurrentDuplicatesHaveOnePrepare(t *testing.T) {
	c, f, _ := setup(t, false)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			a, err := c.Apply(context.Background(), request(Connect, "same", 1))
			if err != nil || a.Disposition != "applied" {
				t.Error("duplicate rejected")
			}
		})
	}
	wg.Wait()
	p := nextProbe(t, f)
	success(p)
	waitState(t, c, "connected")
	n := 0
	for _, s := range f.history() {
		if s == "prepare" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("prepared %d times", n)
	}
}
func TestSnapshotsCannotMutateController(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	s := c.Snapshot()
	s.Context.AttemptID = "corrupt"
	*s.ValidUntilUTC = baseTime.Add(-time.Hour)
	if c.Snapshot().Context.AttemptID != "attempt-a" || c.Snapshot().ValidUntilUTC.Before(baseTime) {
		t.Fatal("snapshot aliases mutable state")
	}
}
