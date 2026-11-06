package tunengine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// LocalOnly owns an independent DNS/LAN runtime, never a business session. It
// uses the same deterministic BlueGrass adapter as Tun: the host must confirm
// teardown/journal cleanup of one before starting the other. It changes no WFP
// policy and cannot grant its own interface permission. Hosts must authorize only
// the exact observed LUID's DNS ingress and existing LAN/control paths, with
// service-only physical resolver exceptions; NEVER business AllowTunnel.
type LocalOnly struct {
	mu                       sync.Mutex
	p                        localOnlyPrepared
	network                  func(context.Context) (NetworkSnapshot, error)
	factory                  localOnlyRuntimeFactory
	gate                     *sessionGate
	ctx                      context.Context
	cancel                   context.CancelFunc
	started, closed, running bool
	luid                     uint64
	done                     chan struct{}
	ready                    chan error
	cleanupErr               error
	networkErr               error
	closeTimeout             time.Duration
}

// NewLocalOnly validates and detaches a trusted network snapshot without opening
// an interface or socket. AllowLAN=false constructs a rejecting local resolver.
// There is no selected-resource identity, credential, public resolver or FakeIP.
func NewLocalOnly(c LocalOnlyConfig) (*LocalOnly, error) {
	p, err := prepareLocalOnly(c)
	if err != nil {
		return nil, err
	}
	current := c.CurrentNetwork
	if current == nil {
		current = SystemNetwork
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &LocalOnly{p: p, network: current, factory: newLocalOnlyRuntime, gate: newSessionGate(), ctx: ctx, cancel: cancel, done: make(chan struct{}), ready: make(chan error, 1), closeTimeout: 5 * time.Second}, nil
}

// Start opens actual TCP/UDP DNS ingress through the TUN. ctx bounds startup
// only (maximum 10s); successful runtime lifetime belongs to Close/network
// invalidation, not to a canceled connection attempt. An instance starts once.
// The host must retain ownership until Close confirms cleanup, including after
// any startup error. No replacement can safely reuse the adapter before then.
func (l *LocalOnly) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	l.mu.Lock()
	if l.started || l.closed {
		l.mu.Unlock()
		return errors.New("LOCAL_ONLY_NOT_RESTARTABLE")
	}
	l.started = true
	l.mu.Unlock()
	go l.run()
	select {
	case err := <-l.ready:
		if ctx.Err() != nil {
			l.retire()
			return ctx.Err()
		}
		if err == nil && l.ctx.Err() != nil {
			return errors.New("LOCAL_ONLY_START_CANCELED")
		}
		return err
	case <-ctx.Done():
		l.retire()
		return ctx.Err()
	}
}

func (l *LocalOnly) retire() { l.gate.retire(); l.cancel() }
func (l *LocalOnly) current(ctx context.Context) bool {
	if ctx.Err() != nil || l.ctx.Err() != nil {
		return false
	}
	caller := ctx
	// Adapter creation/removal can briefly delay Windows table reads. A slow
	// unchanged snapshot must not be mistaken for a physical-network change.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	unhook := context.AfterFunc(l.ctx, cancel)
	defer unhook()
	n, err := l.network(ctx)
	// Canceling one application query is not evidence of a network change and
	// must not tear down an otherwise healthy independent local runtime.
	if caller.Err() != nil || l.ctx.Err() != nil {
		return false
	}
	valid := err == nil && ctx.Err() == nil && l.ctx.Err() == nil && n.Generation == l.p.network.Generation
	if !valid {
		cause := errors.New("LOCAL_ONLY_NETWORK_CHANGED")
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			cause = &runtimeError{code: "LOCAL_ONLY_NETWORK_CHECK_TIMEOUT", cause: context.DeadlineExceeded}
		} else if err != nil {
			cause = &runtimeError{code: "LOCAL_ONLY_NETWORK_READ_FAILED", cause: err}
		} else if ctx.Err() != nil {
			cause = &runtimeError{code: "LOCAL_ONLY_NETWORK_CHECK_TIMEOUT", cause: ctx.Err()}
		}
		l.mu.Lock()
		if l.networkErr == nil {
			l.networkErr = cause
		}
		l.mu.Unlock()
		l.retire()
	}
	return valid
}
func (l *LocalOnly) run() {
	var r localOnlyRuntime
	defer func() {
		l.retire()
		var err error
		if r != nil {
			if cause := r.Close(); cause != nil {
				err = &runtimeError{code: "LOCAL_ONLY_CLEANUP_FAILED", cause: cause}
			}
		}
		l.mu.Lock()
		l.running = false
		l.luid = 0
		l.cleanupErr = err
		l.mu.Unlock()
		close(l.done)
	}()
	if !l.current(l.ctx) {
		l.ready <- errors.New("LOCAL_ONLY_NETWORK_CHANGED")
		return
	}
	var err error
	r, err = l.factory(l.ctx, l.p, l.gate, l.current)
	if err == nil && l.ctx.Err() == nil {
		err = r.Start()
	}
	if err == nil && l.ctx.Err() == nil {
		err = r.EnableTunnel()
	}
	if err == nil {
		switch {
		case l.ctx.Err() != nil:
			err = l.ctx.Err()
		case r == nil || r.InterfaceLUID() == 0:
			err = errors.New("LOCAL_ONLY_INTERFACE_UNAVAILABLE")
		case !l.current(l.ctx):
			err = errors.New("LOCAL_ONLY_NETWORK_CHANGED")
		case !l.gate.open():
			err = errGateClosed
		}
	}
	if err != nil {
		l.mu.Lock()
		err = errors.Join(err, l.networkErr)
		l.mu.Unlock()
		l.ready <- &runtimeError{code: "LOCAL_ONLY_START_FAILED", cause: err}
		return
	}
	l.mu.Lock()
	l.luid = r.InterfaceLUID()
	l.running = true
	l.mu.Unlock()
	l.ready <- nil
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			if !l.current(l.ctx) {
				return
			}
		}
	}
}

// InterfaceLUID is nonzero only after successful startup on the captured network.
// WFP activation must be serialized with Close by the service's lifecycle lock.
func (l *LocalOnly) InterfaceLUID() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running || l.closed || l.ctx.Err() != nil {
		return 0
	}
	return l.luid
}
func (l *LocalOnly) TunnelLUID() uint64 { return l.InterfaceLUID() }

// Close synchronously retires ingress and cancels queries, then waits at most
// 5s for the single owned worker's cleanup. Timeouts/failures are NOT successful
// teardown. Repeated Close waits on the same worker; it never starts more workers.
func (l *LocalOnly) Close() error {
	l.mu.Lock()
	l.closed = true
	started := l.started
	l.mu.Unlock()
	l.retire()
	if !started {
		return nil
	}
	timer := time.NewTimer(l.closeTimeout)
	defer timer.Stop()
	select {
	case <-l.done:
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.cleanupErr
	case <-timer.C:
		return errors.New("LOCAL_ONLY_CLEANUP_TIMEOUT")
	}
}
