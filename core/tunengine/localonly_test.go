package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func localOnlyFixture() LocalOnlyConfig {
	n := NetworkSnapshot{Generation: 7,
		Routes: []LocalRoute{{Prefix: netip.MustParsePrefix("192.168.40.0/24"), Interface: "Ethernet", Connected: true}},
		DNS:    []LocalDNS{{Server: netip.MustParseAddrPort("192.168.40.1:53"), Interface: "Ethernet", Domains: []string{"lan", "home.arpa"}}},
	}
	return LocalOnlyConfig{AllowLAN: true, Network: n, CurrentNetwork: func(context.Context) (NetworkSnapshot, error) { return n, nil }}
}

func TestLocalOnlySlowUnchangedNetworkSnapshotDoesNotRetire(t *testing.T) {
	c := localOnlyFixture()
	c.CurrentNetwork = func(ctx context.Context) (NetworkSnapshot, error) {
		timer := time.NewTimer(1100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return c.Network, nil
		case <-ctx.Done():
			return NetworkSnapshot{}, ctx.Err()
		}
	}
	l, err := NewLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !l.current(t.Context()) || l.ctx.Err() != nil {
		t.Fatal("a slow unchanged snapshot retired the independent local runtime")
	}
}

type localOnlyRuntimeFixture struct {
	mu                       sync.Mutex
	started, enabled, closed bool
	startBlock               <-chan struct{}
	closeBlock               <-chan struct{}
	closeErr                 error
}

func (r *localOnlyRuntimeFixture) Start() error {
	if r.startBlock != nil {
		<-r.startBlock
	}
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	return nil
}
func (r *localOnlyRuntimeFixture) EnableTunnel() error {
	r.mu.Lock()
	r.enabled = true
	r.mu.Unlock()
	return nil
}
func (r *localOnlyRuntimeFixture) InterfaceLUID() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enabled && !r.closed {
		return 42
	}
	return 0
}
func (r *localOnlyRuntimeFixture) Close() error {
	if r.closeBlock != nil {
		<-r.closeBlock
	}
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.closeErr
}

func TestLocalOnlySurvivesStartupContextCancellationAndCannotRestartClosedInstance(t *testing.T) {
	l, err := NewLocalOnly(localOnlyFixture())
	if err != nil {
		t.Fatal(err)
	}
	r := &localOnlyRuntimeFixture{}
	l.factory = func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error) {
		return r, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if l.TunnelLUID() != 42 || !l.gate.isOpen() {
		t.Fatal("startup cancellation retired independent runtime")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if l.TunnelLUID() != 0 || l.gate.isOpen() || l.Start(context.Background()) == nil {
		t.Fatal("closed runtime reopened")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOnlyCloseTimeoutRetiresGateAndRetainsCleanupFailure(t *testing.T) {
	l, err := NewLocalOnly(localOnlyFixture())
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	r := &localOnlyRuntimeFixture{closeBlock: block, closeErr: errors.New("fixture failure")}
	l.factory = func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error) {
		return r, nil
	}
	l.closeTimeout = 20 * time.Millisecond
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err == nil || l.gate.isOpen() || l.TunnelLUID() != 0 {
		t.Fatal("unconfirmed cleanup reported success")
	}
	if l.Start(context.Background()) == nil {
		t.Fatal("restarted during cleanup")
	}
	close(block)
	select {
	case <-l.done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not complete")
	}
	if l.Close() == nil {
		t.Fatal("cleanup failure forgotten")
	}
}

func TestLocalOnlyCanceledStartupHasSingleOwnedCleanup(t *testing.T) {
	l, err := NewLocalOnly(localOnlyFixture())
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	r := &localOnlyRuntimeFixture{startBlock: block}
	l.factory = func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error) {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if l.Start(ctx) == nil || l.TunnelLUID() != 0 || l.gate.isOpen() {
		t.Fatal("timed out startup exposed ingress")
	}
	close(block)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOnlyNetworkChangeRetiresInstanceAndRejectsLateReplies(t *testing.T) {
	c := localOnlyFixture()
	original := c.Network
	var mu sync.Mutex
	c.CurrentNetwork = func(context.Context) (NetworkSnapshot, error) { mu.Lock(); defer mu.Unlock(); return original, nil }
	l, err := NewLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	r := &localOnlyRuntimeFixture{}
	l.factory = func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error) {
		return r, nil
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	original.Generation++
	mu.Unlock()
	if l.current(context.Background()) || l.gate.isOpen() || l.TunnelLUID() != 0 {
		t.Fatal("changed network kept ingress")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if l.Start(context.Background()) == nil {
		t.Fatal("old snapshot restarted")
	}
}

func TestLocalOnlyCanceledDNSCallerDoesNotRetireHealthyRuntime(t *testing.T) {
	c := localOnlyFixture()
	snapshot := c.Network
	var block atomic.Bool
	entered := make(chan struct{}, 1)
	c.CurrentNetwork = func(ctx context.Context) (NetworkSnapshot, error) {
		if block.Load() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return NetworkSnapshot{}, ctx.Err()
		}
		return snapshot, nil
	}
	l, err := NewLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	l.factory = func(context.Context, localOnlyPrepared, *sessionGate, func(context.Context) bool) (localOnlyRuntime, error) {
		return &localOnlyRuntimeFixture{}, nil
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- l.current(ctx) }()
	<-entered
	cancel()
	if <-done {
		t.Fatal("canceled query succeeded")
	}
	block.Store(false)
	if !l.gate.isOpen() || l.TunnelLUID() != 42 {
		t.Fatal("query cancellation retired independent runtime")
	}
}
