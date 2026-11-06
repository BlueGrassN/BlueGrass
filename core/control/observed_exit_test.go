package control

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"bluegrass/native/core/engine"
)

func observedExit(t *testing.T, s NativeSnapshot) string {
	t.Helper()
	b, _ := json.Marshal(s)
	var wire struct {
		IPv4 string `json:"verifiedExitIpv4"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.IPv4
}

func TestObservedExitOptionalLegacyFieldDoesNotControlAdmission(t *testing.T) {
	for _, legacy := range []string{"", "1.1.1.1", "not-an-ip"} {
		t.Run(legacy, func(t *testing.T) {
			c, f, _ := setup(t, false)
			r := request(Connect, "connect", 1)
			r.Params.Profile.ExpectedExitIPv4 = legacy
			wire, _ := json.Marshal(r)
			var raw map[string]any
			_ = json.Unmarshal(wire, &raw)
			if legacy == "" {
				delete(raw["params"].(map[string]any)["profile"].(map[string]any), "expectedExitIpv4")
			}
			wire, _ = json.Marshal(raw)
			decoded, err := DecodeRequest(wire)
			if err != nil {
				t.Fatal("optional legacy field blocked wire admission", err)
			}
			apply(t, c, decoded)
			probe := nextProbe(t, f)
			s := c.Snapshot()
			if s.Connection != "connected" || !s.BusinessGateOpen || observedExit(t, s) != "" {
				t.Fatal("connection must commit before the advisory probe", s.Connection)
			}
			if !reflect.DeepEqual(f.history(), []string{"close", "prepare", "open", "probe"}) {
				t.Fatal("wrong preparation/gate/probe order", f.history())
			}
			probe.reply <- engine.ProbeResult{IPv4: "8.8.8.8", Authenticated: true, Reachable: true}
			c.workers.Wait()
			s = c.Snapshot()
			if s.Connection != "connected" || !s.BusinessGateOpen || s.ErrorCode != "" || observedExit(t, s) != "8.8.8.8" {
				t.Fatal("observed exit was compared to legacy expectation", s)
			}
		})
	}
}

func TestObservedExitFailureOrInvalidObservationKeepsConnection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  engine.ProbeResult
		failure bool
	}{
		{name: "http-failure", failure: true},
		{name: "unreachable"},
		{name: "unauthenticated", result: engine.ProbeResult{IPv4: "8.8.8.8", Reachable: true}},
		{name: "malformed", result: engine.ProbeResult{IPv4: "private diagnostic text", Authenticated: true, Reachable: true}},
		{name: "private", result: engine.ProbeResult{IPv4: "10.0.0.1", Authenticated: true, Reachable: true}},
		{name: "ipv6", result: engine.ProbeResult{IPv4: "2001:4860:4860::8888", Authenticated: true, Reachable: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f, _ := setup(t, false)
			if tc.failure {
				f.fail = "probe"
			}
			apply(t, c, request(Connect, "connect", 1))
			if !tc.failure {
				nextProbe(t, f).reply <- tc.result
			}
			c.workers.Wait()
			s := c.Snapshot()
			if s.Connection != "connected" || !s.BusinessGateOpen || !s.GateKnown || s.ErrorCode != "" || observedExit(t, s) != "" {
				t.Fatal("advisory probe failed the connection or leaked an invalid IP", s)
			}
		})
	}
}

func TestObservedExitCancellationCannotPublishLateAddress(t *testing.T) {
	c, f, _ := setup(t, true)
	apply(t, c, request(Connect, "connect", 1))
	probe := nextProbe(t, f)
	done := make(chan CommandAck, 1)
	go func() { ack, _ := c.Apply(context.Background(), request(Pause, "pause", 2)); done <- ack }()
	<-probe.ctx.Done()
	probe.reply <- engine.ProbeResult{IPv4: "8.8.8.8", Authenticated: true, Reachable: true}
	if ack := <-done; ack.Disposition != "applied" {
		t.Fatal(ack)
	}
	s := c.Snapshot()
	if s.Connection != "paused" || s.BusinessGateOpen || observedExit(t, s) != "" {
		t.Fatal("late observation survived cancellation", s)
	}
}

type expiryDuringOpen struct {
	*fakeEngine
	clock *fakeClock
}

func (e *expiryDuringOpen) SetBusinessGate(open bool) error {
	if open {
		e.clock.advance(time.Hour)
	}
	return e.fakeEngine.SetBusinessGate(open)
}

func TestObservedExitCannotCommitExpiredConnectionAfterSlowGateApply(t *testing.T) {
	clk := &fakeClock{now: baseTime, epoch: "runtime-a"}
	f := &fakeEngine{probes: make(chan probeCall, 1), shutdown: make(chan struct{})}
	c, err := New(&expiryDuringOpen{fakeEngine: f, clock: clk}, clk, "runtime-a", owner())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(f.shutdown); _ = c.Close() })
	apply(t, c, request(Connect, "slow-open", 1))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := c.Snapshot()
		if s.Connection == "paused" && s.Expired && !s.BusinessGateOpen {
			select {
			case <-f.probes:
				t.Fatal("expired connection still probed")
			default:
			}
			return
		}
		select {
		case <-f.probes:
			t.Fatal("connection expired while applying gate but committed and started probe")
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expired gate apply did not close forwarding")
}
