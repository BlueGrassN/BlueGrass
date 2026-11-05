package mactun

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"sync"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/tunengine"
)

type fakeBackend struct {
	mu              sync.Mutex
	j               *Journal
	routes          map[netip.Prefix]Route
	dns             map[string][]netip.Addr
	rules           string
	enabled, marker bool
	failSave        bool
	fail            string
	events          []string
	generation      uint64
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{routes: map[netip.Prefix]Route{}, dns: map[string][]netip.Addr{"Wi-Fi": nil}, generation: 7}
}
func cloneJournal(j Journal) *Journal {
	raw, _ := json.Marshal(j)
	var copy Journal
	_ = json.Unmarshal(raw, &copy)
	return &copy
}
func (b *fakeBackend) record(s string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, s)
	if b.fail == s {
		return errors.New("injected: " + s)
	}
	return nil
}
func (b *fakeBackend) LoadJournal() (*Journal, error) {
	if b.j == nil {
		return nil, nil
	}
	return cloneJournal(*b.j), nil
}
func (b *fakeBackend) SaveJournal(j Journal) error {
	if b.failSave {
		return errors.New("save")
	}
	if e := b.record("save"); e != nil {
		return e
	}
	b.j = cloneJournal(j)
	return nil
}
func (b *fakeBackend) RemoveJournal() error {
	if e := b.record("delete-journal"); e != nil {
		return e
	}
	b.j = nil
	return nil
}
func (b *fakeBackend) Marker(_ uint32, active bool) error {
	if e := b.record("marker"); e != nil {
		return e
	}
	b.marker = active
	return nil
}
func (b *fakeBackend) Close() error                           { return b.record("lease-close") }
func (b *fakeBackend) CheckPF(context.Context, bool) error    { return b.record("check-pf") }
func (b *fakeBackend) ReadPF(context.Context) (string, error) { return b.rules, b.record("read-pf") }
func (b *fakeBackend) CompilePF(_ context.Context, r string) (string, error) {
	return canonicalRules(r), b.record("compile-pf")
}
func (b *fakeBackend) WritePF(_ context.Context, r string) error {
	if e := b.record("write-pf"); e != nil {
		return e
	}
	b.rules = canonicalRules(r)
	return nil
}
func (b *fakeBackend) EnablePF(context.Context) (string, error) {
	if e := b.record("enable-pf"); e != nil {
		return "", e
	}
	b.enabled = true
	return "123456", nil
}
func (b *fakeBackend) PFEnabled(context.Context) error {
	if e := b.record("verify-pf"); e != nil {
		return e
	}
	if !b.enabled {
		return errors.New("disabled")
	}
	return nil
}
func (b *fakeBackend) ReleasePF(context.Context, string) error {
	if e := b.record("release-pf"); e != nil {
		return e
	}
	b.enabled = false
	return nil
}
func (b *fakeBackend) TokenExists(context.Context, string) (bool, error) {
	return b.enabled, b.record("token-exists")
}
func (b *fakeBackend) Network(_ context.Context, _ map[string][]netip.Addr, _ []Route) (tunengine.NetworkSnapshot, error) {
	return tunengine.NetworkSnapshot{Generation: b.generation, EntryDNS: []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:53")}, Routes: []tunengine.LocalRoute{{Prefix: netip.MustParsePrefix("192.168.1.0/24"), Interface: "en0", Connected: true}}}, b.record("network")
}
func (b *fakeBackend) Underlay(context.Context) (Underlay, error) {
	return Underlay{Interface: "en0", Index: 4, Source: netip.MustParseAddr("192.168.1.10"), Gateway: netip.MustParseAddr("192.168.1.1")}, b.record("underlay")
}
func (b *fakeBackend) Resolvers(context.Context) ([]Resolver, error) {
	return []Resolver{{Service: "Wi-Fi", Interface: "en0", Effective: []netip.Addr{netip.MustParseAddr("192.168.1.1")}}}, b.record("resolvers")
}
func (b *fakeBackend) ReadDNS(_ context.Context, s string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), b.dns[s]...), b.record("read-dns")
}
func (b *fakeBackend) VerifyResolver(context.Context, Resolver) error {
	return b.record("verify-resolver")
}
func (b *fakeBackend) WriteDNS(_ context.Context, s string, a []netip.Addr) error {
	if e := b.record("write-dns"); e != nil {
		return e
	}
	b.dns[s] = append([]netip.Addr(nil), a...)
	return nil
}
func (b *fakeBackend) ReadRoute(_ context.Context, p netip.Prefix) (*Route, error) {
	if e := b.record("read-route"); e != nil {
		return nil, e
	}
	r, ok := b.routes[p]
	if !ok {
		return nil, nil
	}
	return &r, nil
}
func (b *fakeBackend) AddRoute(_ context.Context, r Route) error {
	if e := b.record("add-route"); e != nil {
		return e
	}
	b.routes[r.Prefix] = r
	return nil
}
func (b *fakeBackend) DeleteRoute(_ context.Context, r Route) error {
	if e := b.record("delete-route"); e != nil {
		return e
	}
	delete(b.routes, r.Prefix)
	return nil
}
func (b *fakeBackend) VerifyAdapter(context.Context, uint64) error { return b.record("verify-adapter") }
func (b *fakeBackend) Dial(context.Context, string, netip.AddrPort, Underlay) (net.Conn, error) {
	return nil, errors.New("not configured")
}

type fakeTun struct {
	b      *fakeBackend
	config tunengine.Config
	closed bool
	ctx    context.Context
}

func (f *fakeTun) Prepare(ctx context.Context, _ engine.Profile) error {
	f.ctx = ctx
	_, err := f.config.Network(ctx)
	if err != nil {
		return err
	}
	return f.b.record("engine-prepare")
}
func (f *fakeTun) SetBusinessGate(open bool) error {
	if open {
		return f.b.record("adapter-open")
	}
	return f.Close()
}
func (f *fakeTun) Close() error               { f.closed = true; return f.b.record("engine-close") }
func (f *fakeTun) Stop(context.Context) error { return f.Close() }
func (f *fakeTun) ProbeExit(context.Context) (engine.ProbeResult, error) {
	return engine.ProbeResult{}, nil
}
func (f *fakeTun) InterfaceLUID() uint64 { return 233 }
func (f *fakeTun) VirtualPrefixes() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("172.31.255.0/30")}
}
func (f *fakeTun) VerifiedIPv4() string       { return "" }
func (f *fakeTun) Counters() (uint64, uint64) { return 0, 0 }
func profile() engine.Profile {
	return engine.Profile{Key: engine.ResourceKey{Environment: "dev", DataSource: "remote", AccountKey: "account", Kind: "node", SourceID: "one"}, Mode: "tun", Protocol: "socks5", Entry: engine.Entry{Host: "8.8.8.8", Port: 1080}, Auth: engine.Auth{Username: "user", Password: "node-secret"}, RoutingMode: "global"}
}
func fixture() (*Tunnel, *fakeBackend, *fakeTun, error) {
	b := newFakeBackend()
	t, err := newTunnel(501, b)
	if err != nil {
		return nil, nil, nil, err
	}
	f := &fakeTun{b: b}
	t.factory = func(c tunengine.Config) (ownedTunnel, error) { f.config = c; return f, nil }
	err = t.BindLogicalSession(profile().Key, "session_123")
	return t, b, f, err
}
