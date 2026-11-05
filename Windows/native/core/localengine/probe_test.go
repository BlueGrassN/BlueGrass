package localengine

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/engine"
)

func probeLocal(t *testing.T) *Local {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e := &Local{observed: "1.1.1.1", gate: true, session: ctx, cancel: cancel, conns: make(map[net.Conn]struct{}),
		dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fixture unavailable") }}
	t.Cleanup(func() { _ = e.Stop(context.Background()); e.cancel() })
	return e
}

func TestLocalPauseRequiresNewPreparedSession(t *testing.T) {
	e := probeLocal(t)
	_ = e.SetBusinessGate(false)
	if e.SetBusinessGate(true) == nil {
		t.Fatal("pause retained previous outbound")
	}
}

func TestLocalChangedObservationKeepsSessionAndEstablishedConnections(t *testing.T) {
	for _, observed := range []string{"9.9.9.9", "8.8.4.4"} {
		e := probeLocal(t)
		peer, conn := net.Pipe()
		defer peer.Close()
		e.conns[conn] = struct{}{}
		result, err := e.acceptProbe(context.Background(), e.generation, e.session, observed)
		if err != nil || result.IPv4 != observed || !result.Authenticated || !result.Reachable {
			t.Fatal("observation was hidden")
		}
		if e.VerifiedIPv4() != observed || !e.gate || len(e.conns) != 1 {
			t.Fatal("observation retired active session")
		}
	}
}

func TestLocalStaleMismatchCannotCloseReplacement(t *testing.T) {
	e := probeLocal(t)
	oldEpoch, oldSession := e.generation, e.session
	_ = e.SetBusinessGate(false)
	e.observed, e.gate = "1.1.1.1", true
	if _, err := e.acceptProbe(context.Background(), oldEpoch, oldSession, "9.9.9.9"); err == nil {
		t.Fatal("stale observation accepted")
	}
	if !e.gate || e.VerifiedIPv4() != "1.1.1.1" {
		t.Fatal("old mismatch closed replacement")
	}
}

func TestLocalProbeUsesSharedFallbackAndPreservesProofOnTransientFailure(t *testing.T) {
	e := probeLocal(t)
	var mu sync.Mutex
	var targets []string
	e.dial = func(_ context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		targets = append(targets, network+" "+address)
		mu.Unlock()
		return nil, errors.New("fixture network error")
	}
	if _, err := e.ProbeExit(context.Background()); err == nil {
		t.Fatal("all failed endpoints accepted")
	}
	mu.Lock()
	got := strings.Join(targets, ",")
	mu.Unlock()
	if got != "tcp api.ipify.org:443,tcp icanhazip.com:443,tcp checkip.amazonaws.com:443" {
		t.Fatal("not using shared endpoint sequence", got)
	}
	if !e.gate || e.VerifiedIPv4() != "1.1.1.1" {
		t.Fatal("transient diagnostic failure revoked current proof")
	}
}

func TestLocalGateCloseCancelsProbeWithoutFallback(t *testing.T) {
	e := probeLocal(t)
	entered := make(chan struct{}, 3)
	e.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, err := e.ProbeExit(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	_ = e.SetBusinessGate(false)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled probe succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("gate close did not cancel controlled probe")
	}
	if len(entered) != 0 {
		t.Fatal("session cancellation started fallback")
	}
}

func TestLocalFailedPrepareRetiresPreviousProofAndOutbound(t *testing.T) {
	e := probeLocal(t)
	if err := e.Prepare(context.Background(), engine.Profile{}); err == nil {
		t.Fatal("invalid profile prepared")
	}
	if e.VerifiedIPv4() != "" || e.SetBusinessGate(true) == nil || e.dial != nil {
		t.Fatal("failed Prepare retained previous proof or outbound")
	}
}

type closeBarrier struct {
	net.Conn
	entered, release chan struct{}
}

func (c closeBarrier) Close() error {
	close(c.entered)
	<-c.release
	return nil
}

func TestLocalPrepareWithdrawsOldOutboundAndCannotOutrunStop(t *testing.T) {
	e := probeLocal(t)
	barrier := closeBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	e.conns[barrier] = struct{}{}
	p := engine.Profile{Protocol: "socks5", Mode: "system_proxy", ExpectedExitIPv4: "1.1.1.1"}
	p.Entry.Host, p.Entry.Port = "8.8.8.8", 1080
	done := make(chan error, 1)
	go func() { done <- e.Prepare(context.Background(), p) }()
	<-barrier.entered // old connection closure pauses Prepare before constructing the replacement
	e.mu.Lock()
	oldAvailable := e.dial != nil
	e.mu.Unlock()
	_ = e.Stop(context.Background())
	close(barrier.release)
	err := <-done
	if oldAvailable {
		t.Error("Prepare left the previous outbound available for a new probe")
	}
	if err == nil || e.dial != nil || e.gate {
		t.Fatal("Prepare finishing after Stop restored an obsolete outbound")
	}
}

func TestLocalPrepareClearsObservationAndRejectsPreviousSessionResult(t *testing.T) {
	e := probeLocal(t)
	oldEpoch, oldSession := e.generation, e.session
	p := engine.Profile{Protocol: "socks5", Mode: "system_proxy", ExpectedExitIPv4: "1.1.1.1"}
	p.Entry.Host, p.Entry.Port = "8.8.8.8", 1080
	if err := e.Prepare(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if e.dial == nil || e.gate || e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("replacement retained old observation or failed to open its outbound")
	}
	if _, err := e.acceptProbe(context.Background(), oldEpoch, oldSession, "1.1.1.1"); err == nil {
		t.Fatal("predecessor evidence verified the replacement")
	}
	if _, err := e.acceptProbe(context.Background(), e.generation, e.session, "1.1.1.1"); err != nil || e.SetBusinessGate(true) != nil {
		t.Fatal("current observation failed")
	}
}
