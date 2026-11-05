package tunengine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"bluegrass/native/core/engine"
)

func testProfile() engine.Profile {
	return engine.Profile{Protocol: "socks5", Mode: "tun", Entry: engine.Entry{Host: "8.8.8.8", Port: 1080}, ExpectedExitIPv4: "9.9.9.9"}
}

func TestEntryAlwaysRequiresPublicIPv4Literal(t *testing.T) {
	for _, host := range []string{"relay.example", "localhost", "127.0.0.1", "192.168.1.2", "0.0.0.0", "198.18.0.2", "::ffff:8.8.8.8", "2001:4860:4860::8888"} {
		t.Run(host, func(t *testing.T) {
			c := testConfig()
			networkReads, builds := 0, 0
			provider := c.Network
			c.Network = func(ctx context.Context) (NetworkSnapshot, error) { networkReads++; return provider(ctx) }
			e, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			e.factory = func(context.Context, preparedConfig, *sessionGate) (runtimeEngine, error) {
				builds++
				return &fakeRuntime{}, nil
			}
			p := testProfile()
			p.Entry.Host = host
			if err := e.Prepare(context.Background(), p); err == nil || networkReads != 0 || builds != 0 {
				t.Fatal("invalid entry reached network/runtime preparation")
			}
		})
	}
	// Already-resolved public IPv4 still prepares with the business gate closed.
	e := newTestEngine(t, &fakeRuntime{})
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	if e.ResolvedEntry() != netip.MustParseAddrPort("8.8.8.8:1080") || e.gate.isOpen() {
		t.Fatal("literal entry or closed gate assertion changed")
	}
}

func TestLateOldNetworkProbeCannotStopReplacement(t *testing.T) {
	var generation atomic.Uint64
	generation.Store(1)
	c := testConfig()
	base := c.Network
	c.Network = func(ctx context.Context) (NetworkSnapshot, error) {
		n, err := base(ctx)
		n.Generation = generation.Load()
		return n, err
	}
	e, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	started, finish := make(chan struct{}), make(chan struct{})
	old := &fakeRuntime{probe: func(context.Context) (string, error) { close(started); <-finish; return "9.9.9.9", nil }}
	next := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e.factory = func(context.Context, preparedConfig, *sessionGate) (runtimeEngine, error) { return old, nil }
	if err = e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = e.ProbeExit(context.Background()); close(done) }()
	<-started
	generation.Store(2)
	e.factory = func(context.Context, preparedConfig, *sessionGate) (runtimeEngine, error) { return next, nil }
	if err = e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(finish)
	<-done
	if err = e.SetBusinessGate(true); err != nil {
		t.Fatal("late old probe killed replacement:", err)
	}
}

func testConfig() Config {
	return Config{Network: func(context.Context) (NetworkSnapshot, error) {
		return NetworkSnapshot{Generation: 1, Routes: []LocalRoute{
			{Prefix: netip.MustParsePrefix("192.168.1.0/24"), Interface: "Ethernet", Connected: true},
		}}, nil
	}}
}

type fakeRuntime struct {
	mu            sync.Mutex
	closed        bool
	startErr      error
	closeErr      error
	enableErr     error
	activated     bool
	gate          *sessionGate
	enableSawOpen bool
	probe         func(context.Context) (string, error)
}

func (r *fakeRuntime) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = false
	r.activated = false
	return r.startErr
}
func (r *fakeRuntime) EnableTunnel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gate != nil {
		r.enableSawOpen = r.gate.isOpen()
	}
	if r.enableErr != nil {
		return r.enableErr
	}
	r.activated = true
	return nil
}
func (r *fakeRuntime) InterfaceLUID() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activated && !r.closed {
		return 42
	}
	return 0
}
func (r *fakeRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return r.closeErr
}
func (r *fakeRuntime) Probe(ctx context.Context) (string, error) { return r.probe(ctx) }

func newTestEngine(t *testing.T, r *fakeRuntime) *Tun {
	t.Helper()
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.factory = func(_ context.Context, _ preparedConfig, g *sessionGate) (runtimeEngine, error) {
		r.gate = g
		return r, nil
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// Preparation permits activation without an observation; pause still retires it.
func TestGateRequiresPreparedSessionAndClosesActiveTCP(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	if e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("prepared session required a detector result")
	}
	if _, err := e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	if !e.gate.track(a) {
		t.Fatal("verified gate rejected connection")
	}
	if err := e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("closed")); !errors.Is(err, net.ErrClosed) && err == nil {
		t.Fatal("active connection survived pause")
	}
	if e.VerifiedIPv4() != "" || e.SetBusinessGate(true) == nil {
		t.Fatal("reused proof after pause")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if !closed {
		t.Fatal("TUN runtime survived pause")
	}
}

func TestChangedObservationAcceptedButFailedPrepareRetiresSession(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "8.8.4.4", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	result, err := e.ProbeExit(context.Background())
	if err != nil || result.IPv4 != "8.8.4.4" {
		t.Fatalf("must surface reliable mismatch: %v", err)
	}
	if e.SetBusinessGate(true) != nil || e.VerifiedIPv4() != "8.8.4.4" {
		t.Fatal("observation prevented opening gate")
	}
	p := testProfile()
	p.Mode = "system_proxy"
	if e.Prepare(context.Background(), p) == nil {
		t.Fatal("accepted wrong mode")
	}
	if _, err := e.ProbeExit(context.Background()); err == nil {
		t.Fatal("failed prepare kept old session")
	}
}

func TestPrepareContextCancellationAndNetworkInvalidation(t *testing.T) {
	r := &fakeRuntime{probe: func(ctx context.Context) (string, error) { <-ctx.Done(); return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Prepare(ctx, testProfile()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if e.SetBusinessGate(true) == nil {
		t.Fatal("cancelled session opened gate")
	}
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.ProbeExit(context.Background()); done <- err }()
	if err := e.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("late network proof accepted")
	}
	if e.SetBusinessGate(true) == nil {
		t.Fatal("network change preserved proof")
	}
}
