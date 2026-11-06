package control

import (
	"bluegrass/native/core/engine"
	"bluegrass/native/core/gate"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The repository vectors are mandatory, never silently skipped or copied.
func fixture(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
func fixtureController(t *testing.T, x ControlContext, now time.Time) (*Controller, *fakeEngine, *fakeClock) {
	t.Helper()
	f := &fakeEngine{probes: make(chan probeCall, 16), shutdown: make(chan struct{})}
	clk := &fakeClock{now: now, epoch: x.RuntimeEpoch}
	k := x.ResourceKey
	c, err := New(f, clk, x.RuntimeEpoch, Owner{k.Environment, k.DataSource, k.AccountKey, x.ClientEpoch})
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
	return c, f, clk
}
func TestSharedControlRequests(t *testing.T) {
	var v struct {
		NowUTC           time.Time         `json:"nowUtc"`
		Requests         []json.RawMessage `json:"requests"`
		ExpectedCommands []Command         `json:"expectedCommands"`
	}
	fixture(t, "connection/control-requests.json", &v)
	first, err := DecodeRequest(v.Requests[0])
	if err != nil {
		t.Fatal(err)
	}
	c, f, _ := fixtureController(t, first.Context, v.NowUTC)
	want := []string{"connected", "paused", "paused", "disconnected", "disconnected"}
	if len(v.Requests) != len(want) {
		t.Fatal("fixture sequence changed: review native expectations")
	}
	for i, raw := range v.Requests {
		r, err := DecodeRequest(raw)
		if err != nil {
			t.Fatal(err)
		}
		if r.Command != v.ExpectedCommands[i] {
			t.Fatal("fixture command mismatch")
		}
		apply(t, c, r)
		if r.Command == Connect {
			nextProbe(t, f)
		}
		s := c.Snapshot()
		if s.Connection != want[i] || s.BusinessGateOpen != (i == 0) {
			t.Fatalf("step %d unsafe state %s", i, s.Connection)
		}
	}
}
func TestSharedInvalidMessages(t *testing.T) {
	var v struct {
		Cases []struct {
			CaseID        string          `json:"caseId"`
			Input         json.RawMessage `json:"input"`
			ExpectedValid bool            `json:"expectedValid"`
		} `json:"cases"`
	}
	fixture(t, "connection/invalid-messages.json", &v)
	for _, tc := range v.Cases {
		t.Run(tc.CaseID, func(t *testing.T) {
			r, err := DecodeRequest(tc.Input)
			if err != nil {
				return
			}
			c, f, _ := fixtureController(t, r.Context, baseTime)
			a, _ := c.Apply(context.Background(), r)
			if a.Disposition == "applied" || len(f.history()) > 0 {
				t.Fatal("invalid common request applied")
			}
		})
	}
}
func TestSharedEligibility(t *testing.T) {
	var v struct {
		Key    ResourceKey `json:"key"`
		NowUTC time.Time   `json:"nowUtc"`
		Cases  []struct {
			CaseID        string     `json:"caseId"`
			ExpectedExit  *string    `json:"expectedExitIpv4"`
			Expiry        *time.Time `json:"expiresAtUtc"`
			Protocol      string     `json:"protocol"`
			Port          *int       `json:"port"`
			IsForwarded   *bool      `json:"isForwarded"`
			ExpectedError *string    `json:"expectedError"`
		} `json:"cases"`
		Invalid []string `json:"invalidObservedIps"`
	}
	fixture(t, "eligibility/cases.json", &v)
	for _, tc := range v.Cases {
		if tc.IsForwarded != nil && !*tc.IsForwarded {
			continue
		}
		t.Run(tc.CaseID, func(t *testing.T) {
			p := profile()
			p.Key = v.Key
			p.ValidUntilUTC = tc.Expiry
			p.ExpectedExitIPv4 = ""
			if tc.ExpectedExit != nil {
				p.ExpectedExitIPv4 = *tc.ExpectedExit
			}
			if tc.Protocol != "" {
				p.Protocol = tc.Protocol
			}
			if tc.Port != nil {
				p.Entry.Port = *tc.Port
			}
			want := ""
			if tc.ExpectedError != nil {
				want = *tc.ExpectedError
			}
			if got := ValidateProfile(p, v.NowUTC); got != want {
				t.Fatalf("want %s got %s", want, got)
			}
		})
	}
	for _, ip := range v.Invalid {
		if gate.PublicIPv4(ip) {
			t.Errorf("accepted special-use/malformed IPv4 %q", ip)
		}
	}
}
func TestSharedMonotonicTime(t *testing.T) {
	var v struct {
		Anchor      time.Time `json:"anchorUtc"`
		Epoch       string    `json:"runtimeEpoch"`
		AnchorNanos string    `json:"anchorNanos"`
		SampleNanos string    `json:"sampleNanos"`
		Expected    time.Time `json:"expectedUtc"`
	}
	fixture(t, "time/clock.json", &v)
	x := contextA()
	x.RuntimeEpoch = v.Epoch
	f := &fakeEngine{probes: make(chan probeCall, 1), shutdown: make(chan struct{})}
	anchor, err := strconv.ParseInt(v.AnchorNanos, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := strconv.ParseInt(v.SampleNanos, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{now: v.Anchor, mono: anchor, epoch: v.Epoch}
	c, err := New(f, clk, v.Epoch, owner())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clk.mu.Lock()
	clk.mono = sample
	clk.mu.Unlock()
	if !c.Snapshot().Time.EffectiveUTC.Equal(v.Expected) {
		t.Fatal("monotonic elapsed time was lost")
	}
}
func TestSharedConnectionGenerations(t *testing.T) {
	var v struct {
		Now   time.Time      `json:"nowUtc"`
		A     ControlContext `json:"contextA"`
		B     ControlContext `json:"contextB"`
		Exit  string         `json:"expectedExitIpv4"`
		Cases []struct {
			ID     string `json:"caseId"`
			Events []struct {
				Type    string `json:"type"`
				Context string `json:"context"`
				Phase   string `json:"phase"`
				Exit    string `json:"verifiedExitIpv4"`
			} `json:"events"`
			Phase    string  `json:"expectedPhase"`
			Attempt  string  `json:"expectedAttempt"`
			Observed *string `json:"expectedObservedIpv4"`
		} `json:"cases"`
	}
	fixture(t, "connection/generations.json", &v)
	for _, tc := range v.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			c, f, _ := fixtureController(t, v.A, v.Now)
			contexts := map[string]ControlContext{"A": v.A, "B": v.B}
			probes := map[string]probeCall{}
			current := ""
			var seq uint64
			for _, event := range tc.Events {
				switch event.Type {
				case "connect":
					seq++
					current = event.Context
					p := profile()
					p.Key = contexts[current].ResourceKey
					p.ExpectedExitIPv4 = v.Exit
					p.ValidUntilUTC = nil
					apply(t, c, ControlRequest{Context: contexts[current], RequestID: fmt.Sprint(seq), Sequence: seq, Command: Connect, Params: Parameters{Profile: &p}})
					probes[current] = nextProbe(t, f)
				case "disconnect":
					seq++
					apply(t, c, ControlRequest{Context: contexts[current], RequestID: fmt.Sprint(seq), Sequence: seq, Command: Disconnect})
				case "confirmed":
					p, ok := probes[event.Context]
					if !ok {
						t.Fatal("fixture confirms unknown attempt")
					}
					if p.ctx.Err() != nil {
						continue
					}
					p.reply <- engine.ProbeResult{IPv4: event.Exit, Authenticated: event.Phase == "connected", Reachable: true}
					c.workers.Wait()
					waitState(t, c, "connected")
				default:
					t.Fatal("unhandled fixture event")
				}
			}
			// Dart awaits an explicit native snapshot; this harness has already
			// waited for Prepare and the connected commit before each probe.
			mapped := map[string]string{"connecting": "connected", "connected": "connected", "stopping": "disconnected", "pausing": "paused"}[tc.Phase]
			s := c.Snapshot()
			observed := ""
			if tc.Observed != nil {
				observed = *tc.Observed
			}
			if s.Connection != mapped || s.Context == nil || s.Context.AttemptID != tc.Attempt || s.VerifiedExitIPv4 != observed || s.BusinessGateOpen != (mapped == "connected") {
				t.Fatalf("phase=%s expected=%s", s.Connection, mapped)
			}
		})
	}
}
func TestSharedCounterValidation(t *testing.T) {
	var v struct {
		Context ControlContext    `json:"context"`
		Base    TrafficSnapshot   `json:"base"`
		Invalid []json.RawMessage `json:"invalidCounters"`
		Cases   []struct {
			ID        string          `json:"caseId"`
			Patch     json.RawMessage `json:"patch"`
			Complete  bool            `json:"expectedComplete"`
			Available bool            `json:"expectedAvailable"`
		} `json:"cases"`
	}
	fixture(t, "traffic/counters.json", &v)
	for _, bad := range v.Invalid {
		var s string
		if json.Unmarshal(bad, &s) == nil {
			if _, ok := decimal(s); ok {
				t.Fatalf("invalid decimal accepted: %s", bad)
			}
		}
	}
	for _, tc := range v.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			c, f, _ := fixtureController(t, v.Context, baseTime)
			p := profile()
			p.Key = v.Context.ResourceKey
			apply(t, c, ControlRequest{Context: v.Context, RequestID: "start", Sequence: 1, Command: Connect, Params: Parameters{Profile: &p}})
			success(nextProbe(t, f))
			waitState(t, c, "connected")
			base := v.Base
			base.Context = v.Context
			if !c.AcceptTraffic(base) {
				t.Fatal("base counter rejected")
			}
			next := base
			if err := json.Unmarshal(tc.Patch, &next); err != nil {
				t.Fatal(err)
			}
			accepted := c.AcceptTraffic(next)
			if tc.ID == "duplicate_sample_is_not_zero_speed" || tc.ID == "counter_regression_is_incomplete" {
				if accepted {
					t.Fatal("duplicate/regression accepted")
				}
				return
			}
			if !accepted {
				t.Fatal("tagged sample rejected")
			}
			s := c.Snapshot().Traffic
			if s.Complete != tc.Complete || s.Available != tc.Available {
				t.Fatal("availability/completeness wrong")
			}
			if s.UploadBytes != next.UploadBytes || s.DownloadBytes != next.DownloadBytes {
				t.Fatal("counter precision lost")
			}
		})
	}
}
