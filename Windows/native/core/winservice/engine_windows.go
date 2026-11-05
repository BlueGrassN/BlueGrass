//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
)

// These are the real OS boundaries. Service lifecycle and authorization stay
// in this composition and can be exercised without touching WFP or Wintun.
type protectionBackend interface {
	ArmDNSWithPolicy(string, string, string, []winprotect.DNSEndpoint, bool) error
	ArmWithEntryPolicy(string, string, string, netip.AddrPort, winprotect.EntryPolicy, []winprotect.DNSEndpoint, bool) error
	ResumeLogicalSession(string, engine.ResourceKey) (string, error)
	BindLogicalSession(string, engine.ResourceKey, string) error
	SwitchLogicalSession(engine.ResourceKey, engine.ResourceKey, string) error
	BeginRuntime(string, engine.ResourceKey, string) error
	BeginMaintenance(string) (winprotect.SessionCheckpoint, error)
	ConfirmOwnedCleanup(winprotect.SessionCheckpoint) error
	CleanupRequired() bool
	ArmLocalDNS(string, []winprotect.DNSEndpoint, bool) error
	BeginLocalRuntime(string) error
	AllowLocalTunnel(string, uint64) error
	UpdateControlDNSForOwner(string, []winprotect.DNSEndpoint) error
	AllowTunnel(uint64) error
	AllowUDPRelay(netip.AddrPort) error
	RemoveUDPRelay(netip.AddrPort) error
	Block() error
	Release() error
	Active() bool
	RecoveryRequired() bool
}
type ownedTunnel interface {
	Prepare(context.Context, engine.Profile) error
	ProbeExit(context.Context) (engine.ProbeResult, error)
	SetBusinessGate(bool) error
	InterfaceLUID() uint64
	VerifiedIPv4() string
	Counters() (uint64, uint64)
	Close() error
}
type relaySession struct {
	ctx     context.Context
	cancel  context.CancelFunc // service token lifetime, also cancels diagnostics
	allowed bool               // protected by relayMu; set only after WFP accepts the TUN
	direct  bool               // immutable routing mode of this runtime
}

// The service owns this composition. The GUI cannot supply paths, interface
// LUIDs, arbitrary routing config or WFP rules through a connection profile.
type protectedTunnel struct {
	mu                 sync.Mutex
	protection         protectionBackend
	sid                string
	tun                ownedTunnel
	local              ownedLocalTunnel
	localDesired       bool
	localGeneration    uint64
	localWatchCancel   context.CancelFunc
	localWatchInterval time.Duration
	prepared, closed   bool
	cleanupErr         error
	allowLAN           bool
	entryDNS           entrydns.Policy // next Prepare only; guarded by mu
	cachePath          string
	cachePathErr       error
	clearCache         func(string) error
	repairAdapter      func() error
	boundKey           engine.ResourceKey
	boundLogicalID     string
	networkGeneration  uint64
	uiExecutable       string
	network            func(context.Context) (tunengine.NetworkSnapshot, error)
	resolve            func(context.Context, string, entrydns.Policy, []netip.AddrPort) (netip.Addr, error)
	newTun             func(tunengine.Config) (ownedTunnel, error)
	newLocal           func(tunengine.LocalOnlyConfig) (ownedLocalTunnel, error)
	// Callbacks never take mu: Close may wait for those same engine goroutines.
	// relayMu serializes their WFP write with revocation before Close is called.
	relayMu sync.Mutex
	relay   *relaySession
}

func newProtectedTunnel(p protectionBackend, sid, ui string) *protectedTunnel {
	// KnownFolder resolves the fixed service-owned path. winprotect.New creates
	// and pins its protected parent; tunengine validates the ACL when opening it.
	cachePath, pathErr := ownedCachePath()
	return &protectedTunnel{protection: p, sid: sid, uiExecutable: ui, allowLAN: true,
		cachePath: cachePath, cachePathErr: pathErr, clearCache: tunengine.ClearCache, repairAdapter: tunengine.RepairAdapter,
		network: tunengine.SystemNetwork, resolve: entrydns.ResolvePhysical,
		localWatchInterval: time.Second,
		newLocal:           func(c tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) { return tunengine.NewLocalOnly(c) },
		newTun:             func(c tunengine.Config) (ownedTunnel, error) { return tunengine.New(c) }}
}

func (e *protectedTunnel) Prepare(ctx context.Context, p engine.Profile) (resultErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if e.boundLogicalID == "" || p.Key != e.boundKey {
		return errors.New("LOGICAL_SESSION_REQUIRED")
	}
	e.stopLocalWatcher()
	if err := e.block(); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, e.pause())
		}
	}()
	protocol, err := p.EffectiveProtocol()
	if err != nil {
		return err
	}
	routingMode, err := routingpolicy.Normalize(p.RoutingMode)
	if err != nil {
		return err
	}
	requirements, err := protocolconfig.Requirements(protocol)
	if err != nil {
		return err
	}
	entryPolicy := winprotect.EntryPolicy{TCP: requirements.EntryTCP, UDP: requirements.EntryUDP, AuthenticatedSOCKSRelay: requirements.UDPTransport == "authenticated-socks-relay"}
	if e.cachePathErr != nil || e.cachePath == "" {
		return errors.New("FAKEIP_CACHE_PATH_FAILED")
	}
	if p.Entry.Port < 1 || p.Entry.Port > 65535 {
		return errors.New("INVALID_ENTRY")
	}
	network, err := e.network(ctx)
	if err != nil {
		return errors.New("NETWORK_SNAPSHOT_FAILED")
	}
	auto := usableDNS(network.EntryDNS)
	dns, err := entryPermissions(p.Entry.Host, e.entryDNS, auto)
	if err != nil {
		return err
	}
	// Durable blocking precedes resolution. DNS is sent from this service to
	// exact numeric endpoints, never delegated to the Windows DNS client.
	if err = e.protection.ArmDNSWithPolicy(e.sid, p.Key.AccountKey, p.Key.SourceID, dns, e.allowLAN); err != nil {
		return errors.New("PROTECTION_ARM_FAILED")
	}
	ip, err := e.resolve(ctx, p.Entry.Host, e.entryDNS, auto)
	if err != nil || !gate.PublicIPv4(ip.String()) {
		return errors.New("ENTRY_DNS_FAILED")
	}
	// Local-name resolvers are a separate trusted policy, not entry fallbacks.
	// They join permissions only after entry resolution and only with LAN on.
	if e.allowLAN {
		for _, d := range network.DNS {
			for _, endpoint := range usableDNS([]netip.AddrPort{d.Server}) {
				dns = append(dns, winprotect.DNSEndpoint{Endpoint: endpoint, Transport: "tcp"},
					winprotect.DNSEndpoint{Endpoint: endpoint, Transport: "udp"})
			}
		}
	}
	if routingMode == routingpolicy.BypassCN {
		dns = append(dns, winprotect.DNSEndpoint{Endpoint: netip.MustParseAddrPort(routingpolicy.DomesticDNS + ":53"), Transport: "tcp"})
	}
	dns = uniqueDNSPermissions(dns)
	if err = e.protection.ArmWithEntryPolicy(e.sid, p.Key.AccountKey, p.Key.SourceID, netip.AddrPortFrom(ip, uint16(p.Entry.Port)), entryPolicy, dns, e.allowLAN); err != nil {
		return errors.New("PROTECTION_ARM_FAILED")
	}
	if err := e.protection.BeginRuntime(e.sid, p.Key, e.boundLogicalID); err != nil {
		return errors.New("SESSION_JOURNAL_FAILED")
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, e.block())
	}
	allow := e.allowLAN
	sessionCtx, cancelSession := context.WithCancel(ctx)
	session := &relaySession{ctx: sessionCtx, cancel: cancelSession, direct: routingMode == routingpolicy.BypassCN}
	e.relayMu.Lock()
	e.relay = session
	e.relayMu.Unlock()
	var relay, releaseRelay func(netip.AddrPort) error
	if entryPolicy.AuthenticatedSOCKSRelay {
		relay = func(endpoint netip.AddrPort) error { return e.allowUDPRelay(session, endpoint) }
		releaseRelay = func(endpoint netip.AddrPort) error { return e.releaseUDPRelay(session, endpoint) }
	}
	var allowDirect, releaseDirect func(string, netip.AddrPort) error
	if session.direct {
		if _, ok := e.protection.(directProtection); !ok {
			return errors.New("DIRECT_PROTECTION_REQUIRED")
		}
		allowDirect = func(network string, endpoint netip.AddrPort) error { return e.allowDirect(session, network, endpoint) }
		releaseDirect = func(network string, endpoint netip.AddrPort) error {
			return e.releaseDirect(session, network, endpoint)
		}
	}
	tun, err := e.newTun(tunengine.Config{AllowLAN: &allow, UIExecutable: e.uiExecutable, Network: e.network, CachePath: e.cachePath, AllowUDPRelay: relay, ReleaseUDPRelay: releaseRelay, AllowDirect: allowDirect, ReleaseDirect: releaseDirect})
	e.tun = tun
	if err != nil {
		return errors.Join(err, e.block())
	}
	e.networkGeneration = network.Generation
	p.Entry.Host = ip.String()
	if err = tun.Prepare(ctx, p); err != nil {
		return errors.Join(err, e.block())
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, e.block())
	}
	e.prepared = true
	return nil
}

// ProbeExit observes the selected runtime after connection. Its result and
// detector errors are advisory; neither authorizes nor revokes forwarding.
func (e *protectedTunnel) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tun == nil || !e.prepared || e.closed {
		return engine.ProbeResult{}, errors.New("ENGINE_NOT_PREPARED")
	}
	return e.tun.ProbeExit(ctx)
}
func (e *protectedTunnel) SetBusinessGate(open bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !open {
		if e.closed && e.tun == nil && e.local == nil && e.cleanupErr == nil {
			return nil
		}
		return e.pause()
	}
	if e.tun == nil || !e.prepared || e.closed {
		return errors.New("ENGINE_NOT_PREPARED")
	}
	if err := e.tun.SetBusinessGate(true); err != nil {
		return errors.Join(err, e.block())
	}
	e.relayMu.Lock()
	session := e.relay
	var err error
	if session == nil || session.ctx.Err() != nil {
		err = errors.New("STALE_TUNNEL_SESSION")
	} else {
		err = e.protection.AllowTunnel(e.tun.InterfaceLUID())
		if err == nil {
			err = session.ctx.Err()
		}
		if err == nil {
			session.allowed = true
		}
	}
	e.relayMu.Unlock()
	if err != nil {
		return errors.Join(err, e.block())
	}
	return nil
}

func (e *protectedTunnel) allowUDPRelay(session *relaySession, endpoint netip.AddrPort) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if e.relay != session || !session.allowed || session.ctx.Err() != nil {
		return errors.New("STALE_TUNNEL_SESSION")
	}
	return e.protection.AllowUDPRelay(endpoint)
}

func (e *protectedTunnel) releaseUDPRelay(session *relaySession, endpoint netip.AddrPort) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if session == nil || e.relay != session || !session.allowed {
		// Block already owns cleanup for retired sessions. Never let their
		// delayed socket closes decrement a new generation's references.
		return nil
	}
	// Cancellation denies new allows but must not prevent current cleanup.
	if err := e.protection.RemoveUDPRelay(endpoint); err != nil {
		// Close callbacks cannot take mu or close their own engine goroutine.
		// Revoke authorization and WFP permits directly under relayMu instead.
		session.cancel()
		return errors.Join(err, e.protection.Block())
	}
	return nil
}

func (e *protectedTunnel) block() error {
	// Revoke the interface permit before stopping its engine. Persistent base
	// rules remain even if Stop/Close fails or the service is killed next.
	e.prepared = false
	e.relayMu.Lock()
	if e.relay != nil && e.relay.cancel != nil {
		e.relay.cancel()
	}
	e.relay = nil
	e.relayMu.Unlock()
	needsCleanup := e.tun != nil || e.local != nil || e.protection.CleanupRequired()
	hadRuntime := e.tun != nil || e.local != nil
	var receipt winprotect.SessionCheckpoint
	var journalErr error
	if needsCleanup {
		receipt, journalErr = e.protection.BeginMaintenance(e.sid)
	}
	err := errors.Join(journalErr, e.protection.Block())
	if e.tun != nil {
		closeErr := e.tun.Close()
		err = errors.Join(err, closeErr)
		if closeErr == nil {
			e.tun = nil
		}
	}
	if e.local != nil {
		closeErr := e.local.Close()
		err = errors.Join(err, closeErr)
		if closeErr == nil {
			e.local = nil
		}
	}
	if !hadRuntime && needsCleanup && journalErr == nil {
		// On process restart there is no runtime object to retry. Only the
		// exact product adapter repair may establish cleanup; absence is checked
		// by that implementation, never inferred from this nil Go reference.
		err = errors.Join(err, e.repairAdapter())
	}
	if needsCleanup && err == nil {
		err = e.protection.ConfirmOwnedCleanup(receipt)
	}
	e.cleanupErr = err
	return err
}
func (e *protectedTunnel) Stop(context.Context) error { return e.SetBusinessGate(false) }
func (e *protectedTunnel) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed && e.tun == nil && e.local == nil && e.cleanupErr == nil {
		return nil
	}
	e.closed = true
	e.stopLocalWatcher()
	return e.block()
}
func (e *protectedTunnel) Address() string { return "" }

// VerifiedIPv4 is a legacy observation accessor, never a readiness condition.
func (e *protectedTunnel) VerifiedIPv4() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tun == nil || !e.prepared {
		return ""
	}
	return e.tun.VerifiedIPv4()
}
func (e *protectedTunnel) Counters() (uint64, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tun == nil {
		return 0, 0
	}
	return e.tun.Counters()
}
func (e *protectedTunnel) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cleanupErr != nil || e.protection.CleanupRequired() || e.protection.Active() || e.protection.RecoveryRequired()
}
func (e *protectedTunnel) Release() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	e.stopLocalWatcher()
	if err := e.block(); err != nil {
		return err
	}
	if e.cachePathErr != nil || e.cachePath == "" {
		return errors.New("FAKEIP_CACHE_PATH_FAILED")
	}
	if err := e.clearCache(e.cachePath); err != nil {
		e.cleanupErr = errors.New("FAKEIP_CACHE_CLEAR_FAILED")
		return e.cleanupErr
	}
	if err := e.protection.Release(); err != nil {
		return err
	}
	e.boundKey, e.boundLogicalID = engine.ResourceKey{}, ""
	return nil
}
func (e *protectedTunnel) SetAllowLAN(allow bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if e.tun != nil {
		return errors.New("DISCONNECT_REQUIRED")
	}
	if e.localDesired {
		if err := e.block(); err != nil {
			return err
		}
		e.allowLAN = allow
		return e.startLocal()
	}
	e.allowLAN = allow
	return nil
}

// SetEntryDNS copies the policy for the next Prepare. It never alters the
// current session or resolves names, and accepts no unrepresentable endpoint.
func (e *protectedTunnel) SetEntryDNS(policy entrydns.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	for _, c := range policy.Candidates {
		if len(usableDNS([]netip.AddrPort{c.Endpoint()})) != 1 {
			return errors.New("INVALID_ENTRY_DNS")
		}
	}
	policy.Candidates = append([]entrydns.Candidate(nil), policy.Candidates...)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("ENGINE_CLOSED")
	}
	e.entryDNS = policy
	return nil
}

func (e *protectedTunnel) networkChanged() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tun == nil || !e.prepared {
		return false
	}
	n, err := e.network(context.Background())
	return err != nil || n.Generation != e.networkGeneration
}
