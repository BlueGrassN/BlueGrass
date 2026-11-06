package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/winprotect"
	"bluegrass/native/core/winproxy"
)

type switchProbe struct {
	ctx    context.Context
	answer chan engine.ProbeResult
}
type switchEngineFixture struct {
	*logicalEngineFixture
	tun                 bool
	gateMu              sync.Mutex
	open                bool
	closes, switches    int
	closeErr, switchErr error
	afterSwitch         func()
	probes              chan switchProbe
}

func (e *switchEngineFixture) Prepare(ctx context.Context, p engine.Profile) error {
	if e.tun {
		return e.logicalEngineFixture.Prepare(ctx, p)
	}
	e.prepared <- struct{}{}
	return nil
}
func (e *switchEngineFixture) ProbeExit(ctx context.Context) (engine.ProbeResult, error) {
	call := switchProbe{ctx: ctx, answer: make(chan engine.ProbeResult, 1)}
	e.probes <- call
	// Intentionally return a late old observation after cancellation. The real
	// controller must not publish it for a replacement attempt.
	return <-call.answer, nil
}
func (e *switchEngineFixture) SetBusinessGate(open bool) error {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	if !open {
		e.closes++
		if e.closeErr != nil {
			return e.closeErr
		}
	}
	e.open = open
	return nil
}
func (e *switchEngineFixture) Stop(context.Context) error { return e.SetBusinessGate(false) }
func (e *switchEngineFixture) SwitchLogicalSession(oldKey, newKey engine.ResourceKey, id string) error {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	e.switches++
	if e.open || e.closes == 0 {
		return errors.New("journal switched before gate closure")
	}
	if e.switchErr != nil {
		return e.switchErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.journal.mu.Lock()
	defer e.journal.mu.Unlock()
	j := e.journal
	if !j.active || j.owner != e.owner || j.key != oldKey || oldKey.Environment != newKey.Environment || oldKey.DataSource != newKey.DataSource || oldKey.AccountKey != newKey.AccountKey {
		return winprotect.ErrOwner
	}
	j.key, j.id, e.bound = newKey, id, id
	if e.afterSwitch != nil {
		e.afterSwitch()
	}
	return nil
}
func (e *switchEngineFixture) gate() (bool, int, int) {
	e.gateMu.Lock()
	defer e.gateMu.Unlock()
	return e.open, e.closes, e.switches
}

type switchProxyBackend struct {
	mu    sync.Mutex
	value winproxy.Settings
	sets  int
}

func (b *switchProxyBackend) Query() (winproxy.Settings, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.value, nil
}
func (b *switchProxyBackend) Set(s winproxy.Settings) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.value = s
	b.sets++
	return nil
}
func (*switchProxyBackend) Notify() error { return nil }

type switchProxyStore struct {
	mu             sync.Mutex
	record         *winproxy.Record
	saves, deletes int
}

func (s *switchProxyStore) Load() (*winproxy.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil {
		return nil, nil
	}
	v := *s.record
	return &v, nil
}
func (s *switchProxyStore) Save(r winproxy.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record = &r
	s.saves++
	return nil
}
func (s *switchProxyStore) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record = nil
	s.deletes++
	return nil
}

func switchHost(t *testing.T, mode string) (*Host, *switchEngineFixture, *switchProxyBackend, *switchProxyStore) {
	t.Helper()
	e := &switchEngineFixture{logicalEngineFixture: newLogicalEngine(&logicalJournalFixture{}, "sid-a"), tun: mode == "tun", probes: make(chan switchProbe, 8)}
	h := dnsHost(t, e, mode)
	var backend *switchProxyBackend
	var store *switchProxyStore
	if mode == "system_proxy" {
		backend = &switchProxyBackend{value: winproxy.Settings{Flags: winproxy.ProxyTypeAutoProxyURL, AutoConfigURL: "https://original.example/proxy.pac"}}
		store = &switchProxyStore{}
		manager, err := winproxy.NewWithDependencies(backend, store)
		if err != nil {
			t.Fatal(err)
		}
		h.proxy = manager
		h.proxyEngine = &proxyEngine{Engine: e, manager: manager, address: "127.0.0.1:49152", bypass: proxyBypass(true)}
	}
	return h, e, backend, store
}
func nextSwitchProbe(t *testing.T, e *switchEngineFixture) switchProbe {
	t.Helper()
	select {
	case call := <-e.probes:
		t.Cleanup(func() {
			select {
			case call.answer <- engine.ProbeResult{}:
			default:
			}
		})
		return call
	case <-time.After(time.Second):
		t.Fatal("engine did not start a fresh probe")
		return switchProbe{}
	}
}
func proveSwitch(t *testing.T, h *Host, call switchProbe) {
	t.Helper()
	call.answer <- engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s := h.controller.Snapshot(); s.BusinessGateOpen && s.VerifiedExitIPv4 == "1.1.1.1" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fresh observation was not published")
}
func switchRequest(t *testing.T, h *Host, id, mode, source string) Request {
	t.Helper()
	r := dnsRequest(t, id, mode, nil)
	r.Command = "SwitchResource"
	x := h.context
	r.Context = &x
	var p engine.Profile
	_ = json.Unmarshal(r.Profile, &p)
	p.Key.SourceID = source
	r.Profile, _ = json.Marshal(p)
	return r
}

func TestSwitchResourceRetainsProtectionAndOriginalSnapshotWithFreshPreparation(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		t.Run(mode, func(t *testing.T) {
			h, e, backend, store := switchHost(t, mode)
			if r := h.Handle(dnsRequest(t, "first", mode, nil)); !r.OK {
				t.Fatal(r.Error)
			}
			proveSwitch(t, h, nextSwitchProbe(t, e))
			old := h.context
			var original *winproxy.Record
			if store != nil {
				original, _ = store.Load()
			}
			r := h.Handle(switchRequest(t, h, "switch", mode, "resource-B"))
			if !r.OK {
				t.Fatalf("switch rejected: %s", r.Error)
			}
			call := nextSwitchProbe(t, e)
			open, _, switches := e.gate()
			if !open || h.controller.Snapshot().VerifiedExitIPv4 != "" || h.context.LogicalSessionID == old.LogicalSessionID || h.context.AttemptID == old.AttemptID || h.context.ResourceKey.SourceID != "resource-B" {
				t.Fatal("switch delayed forwarding or reused old identity/observation")
			}
			if e.tun && (switches != 1 || !e.Active()) {
				t.Fatal("journal did not switch under retained protection")
			}
			_, _, _, releases := e.calls()
			if releases != 0 {
				t.Fatal("switch released WFP")
			}
			if store != nil {
				record, _ := store.Load()
				if record == nil || *record != *original || store.deletes != 0 || backend.sets != 1 {
					t.Fatal("switch replaced/restored original proxy snapshot")
				}
			}
			proveSwitch(t, h, call)
			if store != nil {
				record, _ := store.Load()
				if *record != *original || store.saves != 2 || store.deletes != 0 || backend.sets != 1 {
					t.Fatal("new proof rewrote owned system proxy transaction")
				}
			}
		})
	}
}

func TestSwitchResourceRejectsStaleContextAndCrossOwnerBeforeClosing(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		t.Run(mode, func(t *testing.T) {
			h, e, _, _ := switchHost(t, mode)
			if r := h.Handle(dnsRequest(t, "first", mode, nil)); !r.OK {
				t.Fatal(r.Error)
			}
			proveSwitch(t, h, nextSwitchProbe(t, e))
			old := h.context
			_, closes, _ := e.gate()
			for _, variant := range []string{"missing-context", "stale-context", "environment", "data-source", "account", "same-resource", "invalid-profile", "connect-override"} {
				t.Run(variant, func(t *testing.T) {
					r := switchRequest(t, h, variant, mode, "resource-B")
					var p engine.Profile
					_ = json.Unmarshal(r.Profile, &p)
					switch variant {
					case "missing-context":
						r.Context = nil
					case "stale-context":
						r.Context.AttemptID = "old"
					case "environment":
						p.Key.Environment = "other"
					case "data-source":
						p.Key.DataSource = "other"
					case "account":
						p.Key.AccountKey = "other"
					case "same-resource":
						p.Key = h.context.ResourceKey
					case "invalid-profile":
						p.Entry.Port = 0
					case "connect-override":
						r.Command = "Connect"
						r.Context = nil
					}
					r.Profile, _ = json.Marshal(p)
					reply := h.Handle(r)
					open, nowCloses, switches := e.gate()
					if reply.OK || !open || nowCloses != closes || switches != 0 || h.context != old {
						t.Fatalf("invalid switch changed old session: %s", reply.Error)
					}
				})
			}
		})
	}
}

func TestSwitchResourceLateOldObservationCannotEnterNewResource(t *testing.T) {
	for _, mode := range []string{"tun", "system_proxy"} {
		t.Run(mode, func(t *testing.T) {
			h, e, _, _ := switchHost(t, mode)
			if r := h.Handle(dnsRequest(t, "first", mode, nil)); !r.OK {
				t.Fatal(r.Error)
			}
			old := nextSwitchProbe(t, e)
			request := switchRequest(t, h, "switch-during-probe", mode, "resource-B")
			done := make(chan Response, 1)
			go func() { done <- h.Handle(request) }()
			select {
			case <-old.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("switch did not invalidate old probe")
			}
			old.answer <- engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}
			select {
			case r := <-done:
				if !r.OK {
					t.Fatal(r.Error)
				}
			case <-time.After(time.Second):
				t.Fatal("switch did not complete after old worker returned")
			}
			fresh := nextSwitchProbe(t, e)
			if open, _, _ := e.gate(); !open || h.controller.Snapshot().VerifiedExitIPv4 != "" {
				t.Fatal("new preparation delayed forwarding or inherited an old observation")
			}
			proveSwitch(t, h, fresh)
		})
	}
}

func TestSwitchResourceJournalFailureKeepsOldIntentAndStopsBeforePrepare(t *testing.T) {
	h, e, _, _ := switchHost(t, "tun")
	if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	old := h.context
	e.switchErr = errors.New("private journal failure")
	r := h.Handle(switchRequest(t, h, "failed-switch", "tun", "resource-B"))
	_, _, prepares, releases := e.calls()
	open, _, _ := e.gate()
	if r.OK || r.Error != "RECOVERY_REQUIRED" || open || prepares != 1 || releases != 0 || h.context != old || !e.Active() {
		t.Fatal("journal failure lost old intent or prepared replacement")
	}
}

func TestSwitchResourceCloseFailureNeverChangesJournal(t *testing.T) {
	h, e, _, _ := switchHost(t, "tun")
	if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	old := h.context
	e.closeErr = errors.New("private close failure")
	r := h.Handle(switchRequest(t, h, "failed-close", "tun", "resource-B"))
	_, _, prepares, releases := e.calls()
	_, _, switches := e.gate()
	if r.OK || r.Error != "ENGINE_FAILURE" || switches != 0 || prepares != 1 || releases != 0 || h.context != old || !e.Active() || r.Snapshot["gateKnown"] != false {
		t.Fatal("failed gate closure changed journal or claimed closure")
	}
}

func TestSwitchResourceExpiryAfterJournalCommitRemainsRecoverable(t *testing.T) {
	h, e, _, _ := switchHost(t, "tun")
	if r := h.Handle(dnsRequest(t, "first", "tun", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	r := switchRequest(t, h, "expires-during-commit", "tun", "resource-B")
	var p engine.Profile
	_ = json.Unmarshal(r.Profile, &p)
	until := time.Now().UTC().Add(time.Hour)
	p.ValidUntilUTC = &until
	r.Profile, _ = json.Marshal(p)
	e.afterSwitch = func() { _ = h.clock.Calibrate(until.Add(time.Minute)) }
	reply := h.Handle(r)
	if reply.OK || reply.Error != "ELIG-003" || reply.Snapshot["connection"] != "paused" || reply.Snapshot["context"] != nil || reply.Snapshot["protectionIntent"] != true {
		t.Fatal("failed post-commit attempt retained an obsolete context or lost protection")
	}
	committedID := e.journal.id
	// A fresh eligible source profile can reconnect the committed resource;
	// recovery must never require releasing WFP just to reconcile the context.
	r.Command, r.ID, r.Context = "Connect", "renewed-source", nil
	p.ValidUntilUTC = nil
	r.Profile, _ = json.Marshal(p)
	if reply := h.Handle(r); !reply.OK {
		t.Fatalf("committed resource could not recover: %s", reply.Error)
	}
	fresh := nextSwitchProbe(t, e)
	if h.context.LogicalSessionID != committedID {
		t.Fatal("recovery replaced committed logical identity")
	}
	proveSwitch(t, h, fresh)
	_, _, _, releases := e.calls()
	if releases != 0 {
		t.Fatal("post-commit recovery released protection")
	}
}

func TestSwitchResourceLostProxyOwnershipNeverWritesExternalSettings(t *testing.T) {
	h, e, backend, store := switchHost(t, "system_proxy")
	if r := h.Handle(dnsRequest(t, "first", "system_proxy", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	external := winproxy.Settings{Flags: winproxy.ProxyTypeDirect}
	backend.mu.Lock()
	backend.value = external
	backend.mu.Unlock()
	r := h.Handle(switchRequest(t, h, "ownership-lost", "system_proxy", "resource-B"))
	value, _ := backend.Query()
	open, _, _ := e.gate()
	if r.OK || open || value != external || backend.sets != 1 || store.deletes != 0 {
		t.Fatal("lost ownership switch touched external settings or retained forwarding")
	}
}

func TestOwnedProxySameResourceRetryKeepsLogicalIDAndOriginalSnapshot(t *testing.T) {
	h, e, backend, store := switchHost(t, "system_proxy")
	if r := h.Handle(dnsRequest(t, "first", "system_proxy", nil)); !r.OK {
		t.Fatal(r.Error)
	}
	proveSwitch(t, h, nextSwitchProbe(t, e))
	old := h.context
	original, _ := store.Load()
	// A normal Connect cannot replace even the same resource while forwarding.
	if r := h.Handle(dnsRequest(t, "while-open", "system_proxy", nil)); r.OK || r.Error != "DISCONNECT_REQUIRED" {
		t.Fatal("open proxy transaction was reused")
	}
	if r := h.Handle(Request{ID: "pause-for-retry", Command: "Pause"}); !r.OK {
		t.Fatal(r.Error)
	}
	reply := h.Handle(dnsRequest(t, "same-resource-retry", "system_proxy", nil))
	if !reply.OK {
		t.Fatalf("closed owned proxy could not retry: %s", reply.Error)
	}
	fresh := nextSwitchProbe(t, e)
	if h.context.LogicalSessionID != old.LogicalSessionID || h.context.AttemptID == old.AttemptID || h.controller.Snapshot().VerifiedExitIPv4 != "" {
		t.Fatal("same-resource retry changed session identity or reused an observation")
	}
	proveSwitch(t, h, fresh)
	record, _ := store.Load()
	if record == nil || *record != *original || store.deletes != 0 || store.saves != 2 || backend.sets != 1 {
		t.Fatal("retry restored or replaced the owned proxy transaction")
	}
	traffic := responseTraffic(t, h.Handle(Request{ID: "retried-status", Command: "GetStatus"}))
	if !traffic.Available || traffic.Complete {
		t.Fatal("retry claimed complete lifetime counters after engine counters reset")
	}
}

func TestOwnedProxyRetryAndSwitchCannotChangeBypass(t *testing.T) {
	for _, command := range []string{"Connect", "SwitchResource"} {
		t.Run(command, func(t *testing.T) {
			h, e, backend, store := switchHost(t, "system_proxy")
			if r := h.Handle(dnsRequest(t, "first", "system_proxy", nil)); !r.OK {
				t.Fatal(r.Error)
			}
			proveSwitch(t, h, nextSwitchProbe(t, e))
			if r := h.Handle(Request{ID: "pause", Command: "Pause"}); !r.OK {
				t.Fatal(r.Error)
			}
			r := dnsRequest(t, "different-bypass", "system_proxy", nil)
			if command == "SwitchResource" {
				r = switchRequest(t, h, "different-bypass", "system_proxy", "resource-B")
			}
			allow := false
			r.AllowLan = &allow
			reply := h.Handle(r)
			if reply.OK || reply.Error != "DISCONNECT_REQUIRED" || backend.sets != 1 || store.deletes != 0 || store.saves != 2 {
				t.Fatal("active transaction changed bypass")
			}
		})
	}
}
