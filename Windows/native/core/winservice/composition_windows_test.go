//go:build windows

package winservice

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/nativehost"
	"bluegrass/native/core/serviceipc"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
)

// Only the OS-mutating WFP and TUN boundaries are replaced. The service
// composition, nativehost.Close and client lease handoff execute real code.
type compositionLog struct {
	mu     sync.Mutex
	events []string
}

func (l *compositionLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, s)
}
func (l *compositionLog) count(s string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, event := range l.events {
		if event == s {
			n++
		}
	}
	return n
}
func (l *compositionLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

type protectionFixture struct {
	log                                  *compositionLog
	relayHook                            func()
	bootstrapErr                         error
	controlErr                           error
	localPermitErr                       error
	tunnelPermitErr                      error
	switchErr                            error
	bootstrap, dns                       []winprotect.DNSEndpoint
	checkpoint                           winprotect.SessionCheckpoint
	beginErr, maintenanceErr, confirmErr error
	entryPolicy                          winprotect.EntryPolicy
}

func (p *protectionFixture) ResumeLogicalSession(sid string, key engine.ResourceKey) (string, error) {
	if p.checkpoint.LogicalSessionID == "" {
		return "", nil
	}
	if p.checkpoint.OwnerSID != sid || p.checkpoint.ResourceKey != key {
		return "", winprotect.ErrOwner
	}
	return p.checkpoint.LogicalSessionID, nil
}
func (p *protectionFixture) BindLogicalSession(sid string, key engine.ResourceKey, id string) error {
	if p.checkpoint.LogicalSessionID != "" && (p.checkpoint.OwnerSID != sid || p.checkpoint.ResourceKey != key || p.checkpoint.LogicalSessionID != id) {
		return winprotect.ErrOwner
	}
	p.checkpoint.OwnerSID, p.checkpoint.ResourceKey, p.checkpoint.LogicalSessionID = sid, key, id
	return nil
}
func (p *protectionFixture) BeginRuntime(sid string, key engine.ResourceKey, id string) error {
	p.log.add("journal-runtime")
	if p.beginErr != nil {
		return p.beginErr
	}
	if p.checkpoint.OwnerSID != sid || p.checkpoint.ResourceKey != key || p.checkpoint.LogicalSessionID != id || p.checkpoint.CleanupRequired {
		return winprotect.ErrRecoveryRequired
	}
	p.checkpoint.CleanupRequired = true
	p.checkpoint.Phase = "running"
	p.checkpoint.Revision++
	return nil
}
func (p *protectionFixture) CleanupRequired() bool { return p.checkpoint.CleanupRequired }
func (p *protectionFixture) BeginMaintenance(sid string) (winprotect.SessionCheckpoint, error) {
	p.log.add("journal-maintenance")
	if p.maintenanceErr != nil {
		return winprotect.SessionCheckpoint{}, p.maintenanceErr
	}
	if p.checkpoint.OwnerSID != sid {
		return winprotect.SessionCheckpoint{}, winprotect.ErrOwner
	}
	p.checkpoint.CleanupRequired = true
	p.checkpoint.Phase = "maintenance"
	p.checkpoint.Revision++
	return p.checkpoint, nil
}
func (p *protectionFixture) ConfirmOwnedCleanup(receipt winprotect.SessionCheckpoint) error {
	p.log.add("journal-clean")
	if p.confirmErr != nil {
		return p.confirmErr
	}
	if receipt != p.checkpoint {
		return winprotect.ErrRecoveryRequired
	}
	p.checkpoint.CleanupRequired = false
	p.checkpoint.Phase = "blocked"
	p.checkpoint.Revision++
	return nil
}
func (p *protectionFixture) ArmWithEntryPolicy(sid, account, resource string, entry netip.AddrPort, policy winprotect.EntryPolicy, dns []winprotect.DNSEndpoint, lan bool) error {
	p.entryPolicy = policy
	return p.ArmWithDNSPolicy(sid, account, resource, entry, dns, lan)
}

func (p *protectionFixture) UpdateControlDNSForOwner(string, []winprotect.DNSEndpoint) error {
	p.log.add("control-dns")
	return p.controlErr
}

func (p *protectionFixture) ArmDNSWithPolicy(_, _, _ string, dns []winprotect.DNSEndpoint, _ bool) error {
	p.log.add("arm-dns")
	p.bootstrap = slices.Clone(dns)
	return p.bootstrapErr
}
func (p *protectionFixture) ArmWithDNSPolicy(_, _, _ string, _ netip.AddrPort, dns []winprotect.DNSEndpoint, _ bool) error {
	p.log.add("arm")
	p.dns = slices.Clone(dns)
	return nil
}
func (p *protectionFixture) AllowTunnel(uint64) error {
	p.log.add("allow-tunnel")
	return p.tunnelPermitErr
}
func (p *protectionFixture) AllowUDPRelay(netip.AddrPort) error {
	if p.relayHook != nil {
		p.relayHook()
	}
	p.log.add("relay")
	return nil
}
func (p *protectionFixture) Block() error { p.log.add("block"); return nil }
func (p *protectionFixture) Release() error {
	p.log.add("release")
	if p.checkpoint.CleanupRequired {
		return winprotect.ErrRecoveryRequired
	}
	p.checkpoint = winprotect.SessionCheckpoint{}
	return nil
}
func (*protectionFixture) Active() bool           { return true }
func (*protectionFixture) RecoveryRequired() bool { return false }

type tunnelFixture struct {
	log                  *compositionLog
	closeErr, prepareErr error
	probeErr, gateErr    error
	probeIP, observed    string
	onClose              func()
	onGate               func()
}

func (t *tunnelFixture) Prepare(context.Context, engine.Profile) error {
	t.log.add("prepare")
	return t.prepareErr
}
func (t *tunnelFixture) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	t.log.add("probe")
	if err := ctx.Err(); err != nil {
		return engine.ProbeResult{}, err
	}
	if t.probeErr != nil {
		return engine.ProbeResult{}, t.probeErr
	}
	t.observed = t.probeIP
	return engine.ProbeResult{IPv4: t.observed, Authenticated: true, Reachable: true}, nil
}
func (t *tunnelFixture) SetBusinessGate(bool) error {
	t.log.add("gate")
	if t.onGate != nil {
		t.onGate()
	}
	return t.gateErr
}
func (*tunnelFixture) InterfaceLUID() uint64      { return 42 }
func (t *tunnelFixture) VerifiedIPv4() string     { return t.observed }
func (*tunnelFixture) Counters() (uint64, uint64) { return 0, 0 }
func (t *tunnelFixture) Close() error {
	t.log.add("close-tun")
	if t.onClose != nil {
		t.onClose()
	}
	return t.closeErr
}

type compositionFixture struct {
	e                *protectedTunnel
	p                *protectionFixture
	tun              *tunnelFixture
	log              *compositionLog
	callbacks        []func(netip.AddrPort) error
	releaseCallbacks []func(netip.AddrPort) error
}

func newCompositionFixture() *compositionFixture {
	f := &compositionFixture{log: &compositionLog{}}
	f.p = &protectionFixture{log: f.log}
	f.tun = &tunnelFixture{log: f.log, probeIP: "1.1.1.1"}
	f.e = newProtectedTunnel(f.p, "S-1-5-21-1-2-3-1001", `C:\Program Files\BlueGrass\BlueGrass.exe`)
	f.e.cachePath, f.e.cachePathErr = `C:\ProgramData\BlueGrass\fakeip.db`, nil
	f.e.repairAdapter = func() error { f.log.add("repair-adapter"); return nil }
	// Every default fixture replaces both mutating runtime constructors.
	f.e.localWatchInterval = 0
	f.e.newLocal = func(c tunengine.LocalOnlyConfig) (ownedLocalTunnel, error) {
		f.log.add("new-local")
		return &localTunnelFixture{log: f.log}, nil
	}
	f.e.clearCache = func(path string) error {
		f.log.add("clear-cache")
		return nil
	}
	f.e.network = func(context.Context) (tunengine.NetworkSnapshot, error) {
		return tunengine.NetworkSnapshot{Generation: 1, EntryDNS: []netip.AddrPort{netip.MustParseAddrPort("9.9.9.9:53")}}, nil
	}
	f.e.resolve = func(context.Context, string, entrydns.Policy, []netip.AddrPort) (netip.Addr, error) {
		f.log.add("resolve")
		return netip.MustParseAddr("8.8.4.4"), nil
	}
	f.e.newTun = func(c tunengine.Config) (ownedTunnel, error) {
		if c.CachePath != f.e.cachePath {
			return nil, errors.New("missing trusted cache path")
		}
		f.log.add("new-tun")
		f.callbacks = append(f.callbacks, c.AllowUDPRelay)
		f.releaseCallbacks = append(f.releaseCallbacks, c.ReleaseUDPRelay)
		return f.tun, nil
	}
	if binder, ok := any(f.e).(interface {
		BindLogicalSession(engine.ResourceKey, string) error
	}); ok {
		if err := binder.BindLogicalSession(compositionProfile().Key, "logical-service-1"); err != nil {
			panic(err)
		}
	}
	return f
}

func TestCacheClearOnlyAfterConfirmedCleanupAndBeforeExplicitRelease(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	if err := f.e.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.log.count("clear-cache") != 0 {
		t.Fatal("pause cleared persisted fake addresses")
	}
	f.prepare(t)
	f.e.clearCache = func(string) error { f.log.add("clear-cache"); return errors.New("cache locked") }
	if f.e.Release() == nil || f.log.count("release") != 0 || f.e.tun != nil || !f.e.Active() {
		t.Fatal("cache failure released protection or lost cleanup")
	}
	f.e.clearCache = func(path string) error {
		if path != `C:\ProgramData\BlueGrass\fakeip.db` {
			t.Fatal("cache path came from session input")
		}
		f.log.add("clear-cache")
		return nil
	}
	if err := f.e.Release(); err != nil {
		t.Fatal(err)
	}
	events := f.log.snapshot()
	if !slices.Equal(events[len(events)-3:], []string{"block", "clear-cache", "release"}) {
		t.Fatal("unsafe release order", events)
	}
	f = newCompositionFixture()
	f.prepare(t)
	if err := f.e.Close(); err != nil {
		t.Fatal(err)
	}
	if f.log.count("clear-cache") != 0 {
		t.Fatal("EOF/service shutdown cleared cache")
	}
}
func compositionProfile() engine.Profile {
	return engine.Profile{Key: engine.ResourceKey{Environment: "test", DataSource: "api-v2", Kind: "node", AccountKey: "account", SourceID: "resource"}, Protocol: "socks5", Entry: engine.Entry{Host: "entry.invalid", Port: 8443}}
}
func (f *compositionFixture) prepare(t *testing.T) {
	t.Helper()
	if err := f.e.Prepare(context.Background(), compositionProfile()); err != nil {
		t.Fatal(err)
	}
}

func TestCompositionRetainsExactTunnelUntilCloseConfirmed(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	failed := errors.New("owned tunnel close failed")
	f.tun.closeErr = failed
	if !errors.Is(f.e.Stop(context.Background()), failed) || f.e.tun != f.tun {
		t.Fatal("lost unresolved owned tunnel")
	}
	if _, err := f.e.ProbeExit(context.Background()); err == nil {
		t.Fatal("unresolved teardown was still probeable")
	}
	if f.e.SetBusinessGate(true) == nil {
		t.Fatal("unresolved teardown reopened business gate")
	}
	if !errors.Is(f.e.Prepare(context.Background(), compositionProfile()), failed) || f.log.count("arm") != 1 {
		t.Fatal("rearmed over unresolved cleanup")
	}
	if !errors.Is(f.e.Release(), failed) || f.log.count("release") != 0 {
		t.Fatal("released protection over unresolved cleanup")
	}
	f.tun.closeErr = nil
	if err := f.e.Release(); err != nil {
		t.Fatal(err)
	}
	if f.e.tun != nil || f.log.count("release") != 1 || f.log.count("new-tun") != 1 {
		t.Fatal("cleanup did not retry only the original owned tunnel")
	}
}

func TestPrepareFailureRetainsFailedCleanup(t *testing.T) {
	f := newCompositionFixture()
	f.tun.prepareErr = errors.New("prepare failed")
	f.tun.closeErr = errors.New("close failed")
	err := f.e.Prepare(context.Background(), compositionProfile())
	if !errors.Is(err, f.tun.prepareErr) || !errors.Is(err, f.tun.closeErr) || f.e.tun != f.tun {
		t.Fatal("prepare discarded unresolved cleanup", err)
	}
	if f.e.Release() == nil || f.log.count("release") != 0 {
		t.Fatal("prepare failure allowed release")
	}
}

func TestServiceHandoffRetainsFailedHostAcrossClients(t *testing.T) {
	var clients clientLifecycle
	first := serviceipc.Identity{SID: "S-1-5-21-1-2-3-1001", PID: 1, Session: 1}
	next := first
	next.PID = 2
	if err := clients.acquire(first); err != nil {
		t.Fatal(err)
	}
	f := newCompositionFixture()
	f.prepare(t)
	host := nativehost.NewTunnel(f.e)
	f.tun.closeErr = errors.New("close failed")
	clients.retire(first, host.Close)
	if err := clients.acquire(next); !errors.Is(err, errCleanupPending) {
		t.Fatal("client handoff discarded failed host cleanup", err)
	}
	if err := clients.acquire(first); !errors.Is(err, errCleanupPending) {
		t.Fatal("failed handoff erased recovery or retained a dead lease", err)
	}
	if f.log.count("release") != 0 || f.log.count("arm") != 1 || f.log.count("close-tun") < 3 {
		t.Fatal("handoff did not retry exact old ownership")
	}
	f.tun.closeErr = nil
	if err := clients.acquire(next); err != nil {
		t.Fatal(err)
	}
	if f.e.tun != nil {
		t.Fatal("new client admitted before teardown confirmation")
	}
	if err := clients.acquire(first); !errors.Is(err, errServiceBusy) {
		t.Fatal("lease exclusivity lost")
	}
	clients.retire(next, nil)
	if err := f.e.Prepare(context.Background(), compositionProfile()); err == nil {
		t.Fatal("closed host could rearm after handoff")
	}
}

func TestBootstrapCompositionPreservesResolutionOrder(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	want := []string{"block", "arm-dns", "resolve", "arm", "journal-runtime", "new-tun", "prepare"}
	if !slices.Equal(f.log.snapshot(), want) {
		t.Fatalf("bootstrap order=%v", f.log.snapshot())
	}
}

func TestRelayCallbackCannotAuthorizeBeforeGateOrAcrossGeneration(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	relay := netip.MustParseAddrPort("8.8.4.4:50000")
	old := f.callbacks[0]
	if old(relay) == nil || f.log.count("relay") != 0 {
		t.Fatal("relay callback bypassed current gate and WFP permission")
	}
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if err := old(relay); err != nil {
		t.Fatal(err)
	}
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if old(relay) == nil || f.log.count("relay") != 1 {
		t.Fatal("old callback authorized new Arm")
	}
	if err := f.callbacks[1](relay); err != nil || f.log.count("relay") != 2 {
		t.Fatal("current callback rejected", err)
	}
	if err := f.e.Close(); err != nil {
		t.Fatal(err)
	}
	if f.callbacks[1](relay) == nil || f.log.count("relay") != 2 {
		t.Fatal("retired client callback reached protection")
	}
}

func TestCallbackCancellationAndCloseCannotDeadlock(t *testing.T) {
	f := newCompositionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.e.Prepare(ctx, compositionProfile()); err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	cb := f.callbacks[0]
	relay := netip.MustParseAddrPort("8.8.4.4:50000")
	cancel()
	if cb(relay) == nil || f.log.count("relay") != 0 {
		t.Fatal("canceled session authorized relay")
	}
	callbackResult := make(chan error, 1)
	f.tun.onClose = func() { callbackResult <- cb(relay) }
	closed := make(chan error, 1)
	go func() { closed <- f.e.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked with retired callback")
	}
	if <-callbackResult == nil {
		t.Fatal("Close callback reopened relay")
	}
}

func TestInflightRelayCompletesBeforeBlockAndNewArm(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	if err := f.e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	f.p.relayHook = func() { close(entered); <-resume }
	callbackDone := make(chan error, 1)
	go func() { callbackDone <- f.callbacks[0](netip.MustParseAddrPort("8.8.4.4:50000")) }()
	<-entered
	prepareDone := make(chan error, 1)
	go func() { prepareDone <- f.e.Prepare(context.Background(), compositionProfile()) }()
	close(resume)
	if err := <-callbackDone; err != nil {
		t.Fatal(err)
	}
	if err := <-prepareDone; err != nil {
		t.Fatal(err)
	}
	events := f.log.snapshot()
	i := slices.Index(events, "relay")
	if i < 0 || !slices.Equal(events[i+1:], []string{"journal-maintenance", "block", "close-tun", "journal-clean", "arm-dns", "resolve", "arm", "journal-runtime", "new-tun", "prepare"}) {
		t.Fatalf("relay crossed policy generation: %v", events)
	}
}

func TestCallbackDuringGateEnableDeniesWithoutDeadlock(t *testing.T) {
	f := newCompositionFixture()
	f.prepare(t)
	f.tun.onGate = func() {
		if f.callbacks[0](netip.MustParseAddrPort("8.8.4.4:50000")) == nil {
			t.Error("gate enable authorized UDP before AllowTunnel")
		}
	}
	done := make(chan error, 1)
	go func() { done <- f.e.SetBusinessGate(true) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate enable deadlocked with engine relay callback")
	}
	if f.log.count("relay") != 0 {
		t.Fatal("premature relay permit")
	}
}

func TestStickyCleanupCannotBeClearedByRepeatedReleaseOrClientHandoff(t *testing.T) {
	var clients clientLifecycle
	id := serviceipc.Identity{SID: "S-1-5-21-1-2-3-1001", PID: 1, Session: 1}
	if err := clients.acquire(id); err != nil {
		t.Fatal(err)
	}
	f := newCompositionFixture()
	f.prepare(t)
	f.tun.closeErr = errors.New("ENGINE_STOP_FAILED") // models Tun's sticky unconfirmed teardown
	for range 3 {
		if f.e.Release() == nil || f.e.tun != f.tun || !f.e.Active() {
			t.Fatal("release erased unresolved ownership")
		}
	}
	clients.retire(id, nativehost.NewTunnel(f.e).Close)
	for range 3 {
		id.PID++
		if !errors.Is(clients.acquire(id), errCleanupPending) {
			t.Fatal("handoff admitted client without cleanup proof")
		}
		if !errors.Is(clients.retryCleanup(), errCleanupPending) {
			t.Fatal("maintenance falsely confirmed cleanup")
		}
	}
	if clients.pending == nil || f.log.count("release") != 0 || f.log.count("new-tun") != 1 {
		t.Fatal("sticky cleanup escaped service retention")
	}
}
