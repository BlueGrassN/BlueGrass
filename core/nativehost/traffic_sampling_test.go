package nativehost

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/clock"
	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
)

type trafficSamplingEngine struct {
	controlDNSEngine
	probe, opened    chan struct{}
	upload, download uint64
}

// Give each observation a distinct time without depending on Windows clock
// resolution or sleeping between requests.
type trafficSamplingClock struct {
	mu      sync.Mutex
	reading clock.TimeReading
}

func (c *trafficSamplingClock) Read() clock.TimeReading {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading.MonotonicNanos += int64(time.Millisecond)
	c.reading.EffectiveUTC = c.reading.EffectiveUTC.Add(time.Millisecond)
	return c.reading
}

func (e *trafficSamplingEngine) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	select {
	case <-e.probe:
		return engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}, nil
	case <-ctx.Done():
		return engine.ProbeResult{}, ctx.Err()
	}
}
func (e *trafficSamplingEngine) SetBusinessGate(open bool) error {
	if open {
		e.opened <- struct{}{}
	}
	return nil
}
func (e *trafficSamplingEngine) Counters() (uint64, uint64) {
	return e.upload, e.download
}

func trafficSamplingHost(t *testing.T) (*Host, *trafficSamplingEngine) {
	t.Helper()
	e := &trafficSamplingEngine{
		controlDNSEngine: controlDNSEngine{
			addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		},
		probe: make(chan struct{}), opened: make(chan struct{}, 1),
		upload: 100, download: 200,
	}
	h := dnsHost(t, e, "tun")
	h.owner = control.Owner{Environment: "test", DataSource: "fake", AccountKey: "account", ClientEpoch: h.client}
	var err error
	h.controller, err = control.New(e, &trafficSamplingClock{reading: h.clock.Read()}, h.runtime, h.owner)
	if err != nil {
		t.Fatal(err)
	}
	if reply := h.Handle(dnsRequest(t, "traffic-connect", "tun", nil)); !reply.OK {
		t.Fatal(reply.Error)
	}
	close(e.probe)
	select {
	case <-e.opened:
	case <-time.After(time.Second):
		t.Fatal("verified gate did not open")
	}
	// This fixture starts a new session; it is not unidentifiable protection
	// recovered from a prior service process.
	e.active = true
	return h, e
}

func responseTraffic(t *testing.T, reply Response) control.TrafficSnapshot {
	t.Helper()
	data, err := json.Marshal(reply.Snapshot["traffic"])
	if err != nil {
		t.Fatal(err)
	}
	var sample control.TrafficSnapshot
	if err := json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	return sample
}

func TestTrafficSamplingStatusRemainsContinuousAcrossOtherReplies(t *testing.T) {
	h, e := trafficSamplingHost(t)
	first := responseTraffic(t, h.Handle(Request{ID: "status-first", Command: "GetStatus"}))
	if first.Sequence != 1 || !first.Available || !first.Complete || first.UploadBytes != "100" || first.DownloadBytes != "200" {
		t.Fatalf("invalid first status sample: %+v", first)
	}
	e.upload, e.download = 300, 500
	now := time.Now().UTC()
	for _, tc := range []struct {
		request Request
		error   string
	}{
		{Request{ID: "hello", Command: "Hello"}, ""},
		{Request{ID: "dns", Command: "ResolveControlHost", Host: "client-api.sunip.vip"}, ""},
		{clockUpdate(t, "update", 2, now, now.Add(time.Hour)), ""},
		{Request{ID: "invalid-status", Command: "GetStatus", Host: "client-api.sunip.vip"}, "INVALID_PARAMETERS"},
		{Request{ID: "unknown-command", Command: "Unknown"}, "UNSUPPORTED_COMMAND"},
		{Request{ID: "untrusted-dns", Command: "ResolveControlHost", Host: "untrusted.example"}, "CONTROL_DNS_FAILED"},
	} {
		t.Run(tc.request.ID, func(t *testing.T) {
			reply := h.Handle(tc.request)
			if reply.OK != (tc.error == "") || reply.Error != tc.error {
				t.Fatalf("unexpected reply outcome: ok=%v error=%q", reply.OK, reply.Error)
			}
			if reply.Snapshot["connection"] != "connected" || reply.Snapshot["businessGateOpen"] != true {
				t.Fatal("non-status reply lost the live connection snapshot")
			}
			if got := responseTraffic(t, reply); got != first {
				t.Fatalf("non-status reply replaced the cached traffic sample: got %+v want %+v", got, first)
			}
		})
	}
	next := responseTraffic(t, h.Handle(Request{ID: "status-next", Command: "GetStatus"}))
	if next.Sequence != 2 || !next.Available || !next.Complete || next.UploadBytes != "300" || next.DownloadBytes != "500" || next.MonotonicNanos <= first.MonotonicNanos || next.Context != first.Context || next.CounterEpoch != first.CounterEpoch {
		t.Fatalf("status samples are not continuous after interleaved replies: %+v", next)
	}
}

func TestTrafficSamplingRejectedCounterDoesNotConsumeSequence(t *testing.T) {
	h, e := trafficSamplingHost(t)
	first := responseTraffic(t, h.Handle(Request{ID: "status-first", Command: "GetStatus"}))
	if first.Sequence != 1 || !first.Available || !first.Complete {
		t.Fatalf("invalid first status sample: %+v", first)
	}
	// A regressed cumulative counter is rejected by the real controller. The
	// cached sample and the next accepted sample must remain consecutive.
	e.upload, e.download = 99, 199
	if got := responseTraffic(t, h.Handle(Request{ID: "status-regressed", Command: "GetStatus"})); got != first {
		t.Fatalf("rejected counters replaced the cached sample: %+v", got)
	}
	e.upload, e.download = 300, 500
	next := responseTraffic(t, h.Handle(Request{ID: "status-recovered", Command: "GetStatus"}))
	if next.Sequence != 2 || !next.Available || !next.Complete || next.UploadBytes != "300" || next.DownloadBytes != "500" {
		t.Fatalf("rejected counters consumed a sequence: %+v", next)
	}
}
