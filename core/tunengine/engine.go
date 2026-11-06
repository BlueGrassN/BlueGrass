// Package tunengine uses Xray-core behind a gated native packet stack. Host WFP protection MUST
// be armed before Prepare and retained through Stop, failures and Close.
package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"sync"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
)

var _ engine.Engine = (*Tun)(nil)

var errNetworkChanged = errors.New("NETWORK_CHANGED")

type runtimeEngine interface {
	Start() error
	EnableTunnel() error
	InterfaceLUID() uint64
	Probe(context.Context) (string, error)
	Close() error
}
type runtimeFactory func(context.Context, preparedConfig, *sessionGate) (runtimeEngine, error)

type Tun struct {
	op         sync.Mutex
	mu         sync.Mutex
	config     Config
	factory    runtimeFactory
	runtime    runtimeEngine
	gate       *sessionGate
	session    context.Context
	cancel     context.CancelFunc
	prepared   preparedConfig
	observed   string
	generation uint64
	closed     bool
	cleanupErr error // guarded by op; never hide an unconfirmed teardown
}

// New is inert: it opens no sockets and changes no host state. Network must be
// provided by the trusted service, not deserialized from a GUI request.
func New(config Config) (*Tun, error) {
	if config.Network == nil {
		return nil, errors.New("NETWORK_SNAPSHOT_REQUIRED")
	}
	if config.CachePath != "" && !validCachePath(config.CachePath) {
		return nil, errors.New("INVALID_CACHE_PATH")
	}
	if config.AllowLAN != nil {
		value := *config.AllowLAN
		config.AllowLAN = &value
	}
	return &Tun{config: config, factory: newRuntime, gate: newSessionGate()}, nil
}

func (e *Tun) Address() string { return "" }

// VerifiedIPv4 returns the current session's last observed public IPv4.
// It is not a prerequisite for forwarding.
func (e *Tun) VerifiedIPv4() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.Err() != nil || !e.gate.isOpen() {
		return ""
	}
	return e.observed
}
func (e *Tun) Counters() (uint64, uint64) {
	e.mu.Lock()
	g := e.gate
	e.mu.Unlock()
	return g.upload.Load(), g.download.Load()
}
func (e *Tun) InterfaceLUID() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil || e.session.Err() != nil {
		return 0
	}
	return e.runtime.InterfaceLUID()
}
func (e *Tun) ResolvedEntry() netip.AddrPort {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prepared.entry
}
func (e *Tun) LocalDNSEndpoints() []netip.AddrPort {
	e.mu.Lock()
	defer e.mu.Unlock()
	var result []netip.AddrPort
	if e.prepared.allowLAN {
		for _, d := range e.prepared.network.DNS {
			result = append(result, d.Server)
		}
	}
	return result
}

// VirtualPrefixes is available immediately after Prepare, before a TUN exists.
// The host excludes these pools from LAN/DNS WFP exceptions before opening it.
func (e *Tun) VirtualPrefixes() []netip.Prefix {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return nil
	}
	return []netip.Prefix{e.prepared.tun4.Masked(), e.prepared.tun6.Masked(), netip.MustParsePrefix("198.18.0.0/15")}
}

// Prepare's context is the connection attempt/session lifetime, not an IPC call.
// The host resolves Entry according to its entry-DNS policy and arms WFP for the
// resulting literal endpoint before calling this method.
func (e *Tun) Prepare(ctx context.Context, p engine.Profile) error {
	config, err := p.EffectiveProtocol()
	return e.prepare(ctx, p, config, err)
}

// PrepareProtocol follows the same shared profile/duplicate-auth validation as
// Prepare, including retirement of an old session when validation fails.
func (e *Tun) PrepareProtocol(ctx context.Context, p engine.Profile, protocol protocolconfig.Config) error {
	p.ProtocolConfig = &protocol
	return e.Prepare(ctx, p)
}

func (e *Tun) prepare(ctx context.Context, p engine.Profile, protocol protocolconfig.Config, configErr error) error {
	e.op.Lock()
	defer e.op.Unlock()
	if err := e.stop(); err != nil {
		return err
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return errors.New("ENGINE_CLOSED")
	}
	if configErr != nil {
		return configErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, err := netip.ParseAddr(p.Entry.Host)
	if err != nil || !gate.PublicIPv4(p.Entry.Host) {
		return errors.New("RESOLVED_ENTRY_IPV4_REQUIRED")
	}
	network, err := e.config.Network(ctx)
	if err != nil {
		return errors.New("NETWORK_SNAPSHOT_FAILED")
	}
	prepared, err := prepareProtocolConfig(p, e.config, network, entry, protocol)
	if err != nil {
		return err
	}
	lifetime, cancel := context.WithCancel(ctx)
	g := newSessionGate()
	r, err := e.factory(lifetime, prepared, g)
	if err != nil {
		cancel()
		return errors.New("ENGINE_PREPARE_FAILED")
	}
	if err = r.Start(); err != nil {
		g.retire()
		cancel()
		e.closeRuntime(r)
		return errors.New("ENGINE_START_FAILED")
	}
	if lifetime.Err() != nil {
		g.retire()
		cancel()
		e.closeRuntime(r)
		return lifetime.Err()
	}
	e.mu.Lock()
	e.runtime, e.session, e.cancel, e.gate = r, lifetime, cancel, g
	e.prepared, e.observed = prepared, ""
	e.generation++
	e.mu.Unlock()
	// Cancellation immediately blocks and tears down an attempt even when the
	// controller has not yet reached Stop. Identity prevents an old callback from
	// stopping a replacement session.
	context.AfterFunc(lifetime, func() { g.retire(); e.stopIfCurrent(g) })
	return nil
}

func (e *Tun) checkNetwork(ctx context.Context, generation uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := e.config.Network(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("NETWORK_SNAPSHOT_FAILED")
	}
	if n.Generation != generation {
		return errNetworkChanged
	}
	return nil
}

func (e *Tun) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	e.mu.Lock()
	r, session, epoch, g := e.runtime, e.session, e.generation, e.gate
	networkGeneration := e.prepared.network.Generation
	e.mu.Unlock()
	if r == nil || session.Err() != nil || g.isRetired() {
		return engine.ProbeResult{}, errors.New("ENGINE_NOT_PREPARED")
	}
	probeCtx, cancel := context.WithTimeout(ctx, exitprobe.TotalTimeout)
	unhook := context.AfterFunc(session, cancel)
	defer cancel()
	defer unhook()
	if err := e.checkNetwork(probeCtx, networkGeneration); err != nil {
		if errors.Is(err, errNetworkChanged) {
			g.retire()
			e.stopIfCurrent(g)
		}
		return engine.ProbeResult{}, err
	}
	ip, err := r.Probe(probeCtx)
	if networkErr := e.checkNetwork(probeCtx, networkGeneration); networkErr != nil {
		if errors.Is(networkErr, errNetworkChanged) {
			g.retire()
			e.stopIfCurrent(g)
		}
		return engine.ProbeResult{}, networkErr
	}
	if err != nil {
		return engine.ProbeResult{}, errors.New("EXIT_PROBE_FAILED")
	}
	if !exitprobe.ValidObservedAddress(ip) {
		return engine.ProbeResult{}, errors.New("INVALID_EXIT_RESPONSE")
	}
	e.mu.Lock()
	if epoch != e.generation || probeCtx.Err() != nil || session.Err() != nil || g.isRetired() {
		e.mu.Unlock()
		return engine.ProbeResult{}, errors.New("STALE_PROBE")
	}
	e.observed = ip
	e.mu.Unlock()
	return engine.ProbeResult{IPv4: ip, Authenticated: true, Reachable: true}, nil
}

func (e *Tun) SetBusinessGate(open bool) error {
	if !open {
		// Invalidate/cancel before waiting for a possibly in-flight Start.
		e.invalidate()
		e.op.Lock()
		defer e.op.Unlock()
		return e.stop()
	}
	e.op.Lock()
	defer e.op.Unlock()
	e.mu.Lock()
	r, ctx, g := e.runtime, e.session, e.gate
	prepared := !e.closed && e.cleanupErr == nil && r != nil && ctx != nil && !g.isRetired()
	networkGeneration := e.prepared.network.Generation
	e.mu.Unlock()
	if !prepared || ctx.Err() != nil {
		return errors.New("ENGINE_NOT_PREPARED")
	}
	if err := e.checkNetwork(ctx, networkGeneration); err != nil {
		_ = e.stop()
		return err
	}
	// The inbound sees a CLOSED gate throughout adapter, stack and route setup.
	if err := r.EnableTunnel(); err != nil {
		_ = e.stop()
		return errors.New("TUN_START_FAILED")
	}
	if ctx.Err() != nil || r.InterfaceLUID() == 0 || e.checkNetwork(ctx, networkGeneration) != nil || !g.open() {
		_ = e.stop()
		return errors.New("TUN_NOT_READY")
	}
	return nil
}

// NetworkChanged must be called by the host on route/interface/DNS-policy changes,
// after WFP Block and before preparing a newly resolved endpoint. It never arms,
// releases or otherwise modifies the host's firewall protection.
func (e *Tun) NetworkChanged() error      { return e.SetBusinessGate(false) }
func (e *Tun) Stop(context.Context) error { return e.SetBusinessGate(false) }
func (e *Tun) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return e.SetBusinessGate(false)
}
func (e *Tun) invalidate() {
	e.mu.Lock()
	e.generation++
	e.observed = ""
	g, cancel := e.gate, e.cancel
	e.mu.Unlock()
	g.retire()
	if cancel != nil {
		cancel()
	}
}
func (e *Tun) stop() error {
	e.invalidate()
	e.mu.Lock()
	r := e.runtime
	e.runtime, e.session, e.cancel = nil, nil, nil
	e.observed, e.prepared = "", preparedConfig{}
	e.mu.Unlock()
	if r != nil {
		e.closeRuntime(r)
	}
	return e.cleanupErr
}
func (e *Tun) closeRuntime(r runtimeEngine) {
	if err := r.Close(); err != nil {
		e.cleanupErr = &runtimeError{code: "ENGINE_STOP_FAILED", cause: err}
	}
}

// Keep the public error stable while preserving native causes for trusted
// diagnostics. The cause may contain system details; never format it for IPC.
type runtimeError struct {
	code  string
	cause error
}

func (e *runtimeError) Error() string { return e.code }
func (e *runtimeError) Unwrap() error { return e.cause }
func (e *Tun) stopIfCurrent(g *sessionGate) {
	e.op.Lock()
	defer e.op.Unlock()
	e.mu.Lock()
	current := e.gate == g
	e.mu.Unlock()
	if current {
		_ = e.stop()
	}
}
