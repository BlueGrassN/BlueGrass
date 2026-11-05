package control

import (
	"testing"
	"time"
)

func TestPublishObservedExitRequiresFullCurrentContext(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	before := c.Snapshot()
	for name, change := range map[string]func(*ControlContext){
		"schema":      func(x *ControlContext) { x.SchemaVersion++ },
		"client":      func(x *ControlContext) { x.ClientEpoch += "-stale" },
		"runtime":     func(x *ControlContext) { x.RuntimeEpoch += "-stale" },
		"session":     func(x *ControlContext) { x.LogicalSessionID += "-stale" },
		"attempt":     func(x *ControlContext) { x.AttemptID += "-stale" },
		"environment": func(x *ControlContext) { x.ResourceKey.Environment += "-stale" },
		"source":      func(x *ControlContext) { x.ResourceKey.DataSource += "-stale" },
		"account":     func(x *ControlContext) { x.ResourceKey.AccountKey += "-stale" },
		"kind":        func(x *ControlContext) { x.ResourceKey.Kind = "asset" },
		"resource":    func(x *ControlContext) { x.ResourceKey.SourceID += "-stale" },
	} {
		t.Run(name, func(t *testing.T) {
			x := contextA()
			change(&x)
			if c.PublishObservedExit(x, "1.1.1.1") {
				t.Fatal("mismatched context published an observation")
			}
		})
	}
	for _, ip := range []string{"", "not-an-ip", "10.0.0.1", "127.0.0.1", "2001:4860:4860::8888", "::ffff:1.1.1.1"} {
		if c.PublishObservedExit(contextA(), ip) {
			t.Fatal("invalid/nonpublic/non-IPv4 observation accepted", ip)
		}
	}
	if s := c.Snapshot(); s.VerifiedExitIPv4 != before.VerifiedExitIPv4 || s.EventSequence != before.EventSequence {
		t.Fatal("rejected publication changed observation or sequence", s)
	}
	if !c.PublishObservedExit(contextA(), "1.1.1.1") {
		t.Fatal("current observation rejected")
	}
	if s := c.Snapshot(); s.VerifiedExitIPv4 != "1.1.1.1" || s.EventSequence != before.EventSequence+1 || !s.BusinessGateOpen {
		t.Fatal("current observation did not commit one event", s)
	}
}

func TestPublishObservedExitRejectsInvalidatedSession(t *testing.T) {
	for _, transition := range []string{"pause", "disconnect", "close", "expiry", "clock", "replacement", "unknown-gate"} {
		t.Run(transition, func(t *testing.T) {
			c, f, clk := setup(t, false)
			connect(t, c, f)
			switch transition {
			case "pause":
				apply(t, c, request(Pause, "pause", 2))
			case "disconnect":
				apply(t, c, request(Disconnect, "disconnect", 2))
			case "close":
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				// Publication must independently check the old lease even when
				// its expiry timer has not yet processed the deadline.
				clk.advance(time.Hour)
			case "clock":
				clk.mu.Lock()
				clk.epoch = "invalid-runtime"
				clk.mu.Unlock()
			case "replacement":
				r := request(Connect, "replacement", 2)
				r.Context.AttemptID = "replacement"
				apply(t, c, r)
				success(nextProbe(t, f))
				c.workers.Wait()
			case "unknown-gate":
				c.mu.Lock()
				c.snap.GateKnown = false
				c.mu.Unlock()
			}
			before := c.Snapshot()
			if c.PublishObservedExit(contextA(), "1.1.1.1") {
				t.Fatal("invalidated session published an observation")
			}
			if s := c.Snapshot(); s.EventSequence != before.EventSequence || s.VerifiedExitIPv4 != before.VerifiedExitIPv4 {
				t.Fatal("rejected publication altered the snapshot", s)
			}
		})
	}
}

func TestPublishObservedExitWinsOverLateInitialProbe(t *testing.T) {
	c, f, _ := setup(t, false)
	apply(t, c, request(Connect, "connect", 1))
	probe := nextProbe(t, f)
	before := c.Snapshot().EventSequence
	if !c.PublishObservedExit(contextA(), "1.1.1.1") {
		t.Fatal("current diagnostic observation rejected while initial probe pending")
	}
	success(probe) // Initial probe observed 8.8.8.8 before the newer diagnostic.
	c.workers.Wait()
	if s := c.Snapshot(); s.VerifiedExitIPv4 != "1.1.1.1" || s.EventSequence != before+1 {
		t.Fatal("late initial probe overwrote the newer observation", s)
	}
}
