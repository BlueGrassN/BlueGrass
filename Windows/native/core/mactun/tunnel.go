package mactun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"bluegrass/native/core/diagnostics"
	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/tunengine"
)

type ownedTunnel interface {
	engine.Engine
	InterfaceLUID() uint64
	VirtualPrefixes() []netip.Prefix
	VerifiedIPv4() string
	Counters() (uint64, uint64)
	Close() error
}
type session struct {
	ctx          context.Context
	cancel       context.CancelFunc
	split, relay bool
	allowed      bool // under policyMu
	permits      map[Permit]int
}

// Lock order is mu -> policyMu. Engine callbacks only take policyMu; no engine
// Close/Stop/Prepare/gate call is made with policyMu held.
type Tunnel struct {
	mu                           sync.Mutex
	policyMu                     sync.Mutex
	m                            *manager
	tun                          ownedTunnel
	factory                      func(tunengine.Config) (ownedTunnel, error)
	current                      atomic.Pointer[session]
	operation                    atomic.Pointer[operationCancel]
	networkFault                 atomic.Bool
	guard                        Guard
	basePermits                  []Permit
	dnsPermits                   map[Permit]bool
	controls                     map[netip.Addr]bool
	underlay                     Underlay
	networkGeneration            uint64
	allowLAN                     bool
	entryDNS                     entrydns.Policy
	prepared, forwarding, closed bool
	cleanupErr                   error
	cachePath                    string
	clearCache                   func(string) error
}

type operationCancel struct{ cancel context.CancelFunc }

// Nativehost calls Host.NetworkChanged on this latch. Forwarding is already
// closed; the notification updates the controller and GUI to paused.
func (t *Tunnel) NetworkChanged() bool { return t.networkFault.Load() }
func (t *Tunnel) Forwarding() bool     { t.mu.Lock(); defer t.mu.Unlock(); return t.forwarding }

func newTunnel(uid uint32, b Backend) (*Tunnel, error) {
	m, err := newManager(uid, b)
	if err != nil {
		return nil, err
	}
	t := &Tunnel{m: m, allowLAN: true, controls: map[netip.Addr]bool{}, dnsPermits: map[Permit]bool{}, factory: func(c tunengine.Config) (ownedTunnel, error) { return tunengine.New(c) }}
	if !m.j.Active {
		rules, e := b.ReadPF(context.Background())
		if e != nil {
			return nil, e
		}
		if rules != "" {
			return nil, ErrRecovery
		}
	}
	if m.j.Active {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		u, underlayErr := b.Underlay(ctx) // absence still permits a pure blocking recovery guard
		if underlayErr != nil {
			u = Underlay{}
		}
		t.underlay = u
		t.guard = Guard{OwnerUID: uid, Interface: u.Interface}
		// Recovered forwarding/relay/direct permissions are NEVER resumed.
		if err = m.guard(ctx, t.guard); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func resourceHash(key engine.ResourceKey) string {
	raw, _ := json.Marshal(key)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func validKey(k engine.ResourceKey) bool {
	return k.Environment != "" && k.DataSource != "" && k.AccountKey != "" && k.Kind != "" && k.SourceID != "" && len(k.Environment)+len(k.DataSource)+len(k.AccountKey)+len(k.Kind)+len(k.SourceID) <= 2048
}
func (t *Tunnel) ResumeLogicalSession(k engine.ResourceKey) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	if t.closed {
		return "", errors.New("ENGINE_CLOSED")
	}
	if !validKey(k) {
		return "", ErrOwner
	}
	if t.m.j.Session == "" {
		if t.m.j.Active {
			return "", ErrRecovery
		}
		return "", nil
	}
	if t.m.j.ResourceHash != resourceHash(k) {
		return "", ErrOwner
	}
	return t.m.j.Session, nil
}
func (t *Tunnel) BindLogicalSession(k engine.ResourceKey, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if !validKey(k) || !opaqueID.MatchString(id) {
		return ErrOwner
	}
	if t.m.j.Session != "" && (t.m.j.ResourceHash != resourceHash(k) || t.m.j.Session != id) {
		return ErrOwner
	}
	old := t.m.j
	t.m.j.ResourceHash = resourceHash(k)
	t.m.j.Session = id
	if err := t.m.save(); err != nil {
		t.m.j = old
		return err
	}
	return nil
}
func (t *Tunnel) SwitchLogicalSession(old, next engine.ResourceKey, id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	t.policyMu.Lock()
	valid := validKey(next) && next != old && old.Environment == next.Environment && old.DataSource == next.DataSource && old.AccountKey == next.AccountKey && t.m.j.ResourceHash == resourceHash(old) && opaqueID.MatchString(id)
	t.policyMu.Unlock()
	if !valid {
		return ErrOwner
	}
	t.interrupt()
	if err := t.pauseLocked(); err != nil {
		return err
	}
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	previous := t.m.j
	t.m.j.ResourceHash = resourceHash(next)
	t.m.j.Session = id
	if err := t.m.save(); err != nil {
		t.m.j = previous
		return err
	}
	return nil
}
func (t *Tunnel) SetEntryDNS(p entrydns.Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, c := range p.Candidates {
		if !validEndpoint(c.Endpoint()) {
			return errors.New("INVALID_ENTRY_DNS")
		}
	}
	p.Candidates = append([]entrydns.Candidate(nil), p.Candidates...)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	t.entryDNS = p
	return nil
}
func (t *Tunnel) SetAllowLAN(v bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	if t.tun != nil {
		return errors.New("DISCONNECT_REQUIRED")
	}
	t.allowLAN = v
	return nil
}

func (t *Tunnel) Prepare(ctx context.Context, p engine.Profile) (result error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	t.policyMu.Lock()
	bound := t.m.j.Session != "" && t.m.j.ResourceHash == resourceHash(p.Key)
	t.policyMu.Unlock()
	if !bound {
		return errors.New("LOGICAL_SESSION_REQUIRED")
	}
	if err := t.pauseLocked(); err != nil {
		return err
	}
	protocol, err := p.EffectiveProtocol()
	if err != nil {
		return err
	}
	req, err := protocolconfig.Requirements(protocol)
	if err != nil {
		return err
	}
	mode, err := routingpolicy.Normalize(p.RoutingMode)
	if err != nil {
		return err
	}
	if p.Entry.Port < 1 || p.Entry.Port > 65535 || p.Mode != "tun" {
		return errors.New("INVALID_ENTRY")
	}
	lifetime, cancel := context.WithCancel(ctx)
	s := &session{ctx: lifetime, cancel: cancel, split: mode == routingpolicy.BypassCN, relay: req.UDPTransport == "authenticated-socks-relay", permits: map[Permit]int{}}
	t.current.Store(s)
	timer := time.AfterFunc(60*time.Second, cancel)
	defer timer.Stop()
	defer func() {
		if result != nil {
			cancel()
			result = errors.Join(result, t.pauseLocked())
		}
	}()
	op, done := context.WithTimeout(lifetime, 60*time.Second)
	defer done()
	t.policyMu.Lock()
	err = t.m.restore(op)
	if err == nil {
		t.underlay, err = t.m.b.Underlay(op)
	}
	var network tunengine.NetworkSnapshot
	if err == nil {
		network, err = t.m.b.Network(op, nil, nil)
	}
	if err == nil {
		t.guard = Guard{OwnerUID: t.m.j.OwnerUID, Interface: t.underlay.Interface}
		if t.allowLAN {
			if interfacePattern.MatchString(network.MDNSInterface) {
				t.guard.MDNSInterface = network.MDNSInterface
			}
			for _, route := range network.Routes {
				if interfacePattern.MatchString(route.Interface) {
					t.guard.LAN = append(t.guard.LAN, route)
				}
			}
		}
		t.basePermits = nil
		t.dnsPermits = map[Permit]bool{}
		t.controls = map[netip.Addr]bool{}
		err = t.m.guard(op, t.guard)
	}
	if err == nil {
		err = t.authorizeDNSLocked(op, t.entryDNS, network.EntryDNS)
	}
	t.policyMu.Unlock()
	if err != nil {
		return err
	}
	ip, err := t.resolve(op, p.Entry.Host, t.entryDNS, network.EntryDNS, t.underlay)
	if err != nil || !gate.PublicIPv4(ip.String()) {
		return errors.New("ENTRY_DNS_FAILED")
	}
	endpoint := netip.AddrPortFrom(ip, uint16(p.Entry.Port))
	t.policyMu.Lock()
	// Node sockets are bound by Xray; only GUI API peers require /32 routes.
	if err == nil {
		if req.EntryTCP {
			t.basePermits = appendPermit(t.basePermits, Permit{Endpoint: endpoint, Protocol: "tcp"})
		}
		if req.EntryUDP {
			t.basePermits = appendPermit(t.basePermits, Permit{Endpoint: endpoint, Protocol: "udp"})
		}
		if s.split {
			t.basePermits = appendPermit(t.basePermits, Permit{Endpoint: netip.MustParseAddrPort(routingpolicy.DomesticDNS + ":53"), Protocol: "tcp"})
		}
		t.guard.Permits = append([]Permit(nil), t.basePermits...)
		err = t.m.guard(op, t.guard)
	}
	if err == nil {
		err = t.refreshGenerationLocked(op, true)
	}
	t.policyMu.Unlock()
	if err != nil {
		return err
	}
	allow := t.allowLAN
	config := tunengine.Config{AllowLAN: &allow, Network: t.network, CachePath: t.cachePath}
	if s.relay {
		config.AllowUDPRelay = func(e netip.AddrPort) error { return t.permit(s, "udp", e, true, false) }
		config.ReleaseUDPRelay = func(e netip.AddrPort) error { return t.permit(s, "udp", e, false, false) }
	}
	if s.split {
		config.AllowDirect = func(n string, e netip.AddrPort) error { return t.permit(s, n, e, true, true) }
		config.ReleaseDirect = func(n string, e netip.AddrPort) error { return t.permit(s, n, e, false, true) }
	}
	t.tun, err = t.factory(config)
	if err != nil {
		return err
	}
	if t.tun == nil {
		return errors.New("ENGINE_PREPARE_FAILED")
	}
	p.Entry.Host = ip.String()
	if err = t.tun.Prepare(lifetime, p); err != nil {
		return err
	}
	if err = op.Err(); err != nil {
		return err
	}
	t.prepared = true
	t.networkFault.Store(false)
	return nil
}

func (t *Tunnel) SetBusinessGate(open bool) error {
	if !open {
		t.interrupt()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !open {
		return t.pauseLocked()
	}
	if t.closed || !t.prepared || t.tun == nil {
		return errors.New("ENGINE_NOT_PREPARED")
	}
	if t.forwarding {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		t.policyMu.Lock()
		err := t.verifyLocked(ctx)
		t.policyMu.Unlock()
		if err != nil {
			return errors.Join(err, t.pauseLocked())
		}
		return nil
	}
	s := t.current.Load()
	if s == nil || s.ctx.Err() != nil {
		return errors.New("STALE_TUNNEL_SESSION")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	err := t.tun.SetBusinessGate(true)
	if err == nil {
		t.policyMu.Lock()
		err = t.m.b.VerifyAdapter(ctx, t.tun.InterfaceLUID())
		var resolvers []Resolver
		if err == nil {
			resolvers, err = t.m.b.Resolvers(ctx)
			if err == nil && len(resolvers) == 0 {
				err = errors.New("PHYSICAL_DNS_UNAVAILABLE")
			}
		}
		if err == nil {
			err = t.checkNetworkLocked(ctx)
		}
		// PF remains blocking utun traffic throughout route and resolver takeover.
		if err == nil {
			for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
				if err = t.m.addRoute(ctx, Route{Prefix: netip.MustParsePrefix(prefix), Interface: adapterName}); err != nil {
					break
				}
			}
		}
		if err == nil {
			prefixes := t.tun.VirtualPrefixes()
			if len(prefixes) == 0 || !prefixes[0].Addr().Is4() {
				err = errors.New("TUN_DNS_PEER_UNAVAILABLE")
			} else {
				dns := []netip.Addr{prefixes[0].Addr().Next()}
				for _, r := range resolvers {
					if err = t.m.setDNS(ctx, r, dns); err != nil {
						break
					}
				}
			}
		}
		if err == nil {
			err = t.refreshGenerationLocked(ctx, false)
		}
		if err == nil {
			t.guard.Forwarding = true
			t.guard.TunnelIndex = t.tun.InterfaceLUID()
			err = t.m.guard(ctx, t.guard)
		}
		if err == nil {
			err = t.verifyLocked(ctx)
		}
		if err == nil {
			s.allowed = true
			t.forwarding = true
		}
		t.policyMu.Unlock()
	}
	if err != nil {
		return errors.Join(err, t.pauseLocked())
	}
	go t.watch(s)
	return nil
}
func (t *Tunnel) interrupt() {
	if op := t.operation.Load(); op != nil {
		op.cancel()
	}
	if s := t.current.Load(); s != nil {
		s.cancel()
	}
}
func (t *Tunnel) pauseLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return t.pauseWith(ctx)
}
func (t *Tunnel) pauseWith(ctx context.Context) error {
	t.policyMu.Lock()
	s := t.current.Swap(nil)
	if s != nil {
		s.allowed = false
		s.cancel()
	}
	t.forwarding = false
	t.prepared = false
	var err error
	if t.m.j.Active {
		t.guard.Forwarding = false
		t.guard.TunnelIndex = 0
		// Keep only owner HTTPS control permits and root physical DNS on pause.
		t.guard.Permits = nil
		for _, p := range t.basePermits {
			if p.UID == t.m.j.OwnerUID || t.dnsPermits[p] {
				t.guard.Permits = appendPermit(t.guard.Permits, p)
			}
		}
		t.basePermits = append([]Permit(nil), t.guard.Permits...)
		err = t.m.blockFast(ctx, t.guard)
	}
	t.policyMu.Unlock()
	if t.tun != nil {
		closeErr := t.tun.Close()
		err = errors.Join(err, closeErr)
		if closeErr == nil {
			t.tun = nil
		}
	}
	t.cleanupErr = err
	return err
}
func (t *Tunnel) Stop(ctx context.Context) error {
	t.interrupt()
	t.mu.Lock()
	defer t.mu.Unlock()
	err := t.pauseLocked()
	return errors.Join(err, ctx.Err())
}
func (t *Tunnel) Close() error {
	t.interrupt()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return t.cleanupErr
	}
	t.closed = true
	err := t.pauseLocked()
	err = errors.Join(err, t.m.b.Close())
	t.cleanupErr = err
	return err
}
func (t *Tunnel) Release() error {
	t.interrupt()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("ENGINE_CLOSED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := t.pauseWith(ctx); err != nil {
		return err
	}
	if t.cachePath != "" {
		if t.clearCache == nil {
			t.cleanupErr = errors.New("FAKEIP_CACHE_CLEAR_FAILED")
			return t.cleanupErr
		}
		if err := t.clearCache(t.cachePath); err != nil {
			t.cleanupErr = errors.New("FAKEIP_CACHE_CLEAR_FAILED")
			return t.cleanupErr
		}
		if err := ctx.Err(); err != nil {
			t.cleanupErr = err
			return err
		}
	}
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	if err := t.m.release(ctx); err != nil {
		t.cleanupErr = err
		return err
	}
	t.cleanupErr = nil
	t.guard = Guard{}
	t.controls = map[netip.Addr]bool{}
	t.basePermits = nil
	return nil
}
func (t *Tunnel) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.policyMu.Lock()
	defer t.policyMu.Unlock()
	return t.m.j.Active || t.cleanupErr != nil || len(t.m.j.Routes) > 0 || len(t.m.j.DNS) > 0
}
func (t *Tunnel) ProtectionState(forwarding bool) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cleanupErr != nil {
		return "unknown"
	}
	if forwarding && t.forwarding {
		return "pf_forwarding"
	}
	return "pf_blocking"
}
func (t *Tunnel) Address() string { return "" }
func (t *Tunnel) VerifiedIPv4() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tun == nil || !t.forwarding {
		return ""
	}
	return t.tun.VerifiedIPv4()
}
func (t *Tunnel) Counters() (uint64, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tun == nil {
		return 0, 0
	}
	return t.tun.Counters()
}
func (t *Tunnel) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	t.mu.Lock()
	v := t.tun
	open := t.forwarding
	t.mu.Unlock()
	if v == nil || !open {
		return engine.ProbeResult{}, errors.New("ENGINE_NOT_PREPARED")
	}
	return v.ProbeExit(ctx)
}
func (t *Tunnel) Diagnose(ctx context.Context, binding diagnostics.Binding) (diagnostics.Report, error) {
	t.mu.Lock()
	v := t.tun
	open := t.forwarding
	t.mu.Unlock()
	d, ok := v.(interface {
		Diagnose(context.Context, diagnostics.Binding) (diagnostics.Report, error)
	})
	if !ok || !open {
		return diagnostics.Report{}, errors.New("DIAGNOSTIC_UNAVAILABLE")
	}
	return d.Diagnose(ctx, binding)
}
