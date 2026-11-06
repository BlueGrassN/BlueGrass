package tunengine

import (
	"context"
	"github.com/miekg/dns"
	"net"
	"net/netip"
	"sync"
	"time"
)

type persistentFakeIPTransport struct{ store fakeIPStore }

func newFakeIPTransport(ctx context.Context, p preparedConfig) *persistentFakeIPTransport {
	var store fakeIPStore
	if p.cachePath != "" {
		store = newPersistentFakeIPStore(ctx, p, openProtectedCache, time.Now)
	} else {
		store = &volatileFakeIPStore{ctx: ctx, validator: newPersistentFakeIPStore(ctx, p, nil, time.Now), pool: p.fake4, names: map[string]netip.Addr{}, addresses: map[netip.Addr]string{}, next: p.fake4.Addr().Next()}
	}
	return &persistentFakeIPTransport{store: store}
}
func (t *persistentFakeIPTransport) Start() error { return t.store.Start() }
func (t *persistentFakeIPTransport) Close() error { return t.store.Close() }
func (t *persistentFakeIPTransport) Reset()       {}
func (t *persistentFakeIPTransport) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	if ctx.Err() != nil || len(m.Question) != 1 {
		return nil, errFakeIPCache
	}
	q := m.Question[0]
	r := fixedResponse(m, dns.RcodeSuccess)
	if q.Qtype == dns.TypeAAAA {
		return r, nil
	}
	if q.Qtype != dns.TypeA {
		return nil, errFakeIPCache
	}
	a, e := t.store.Create(normalizedName(q.Name), false)
	if e != nil || ctx.Err() != nil {
		return nil, errFakeIPCache
	}
	r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 600}, A: net.IP(a.AsSlice())}}
	return r, nil
}

type volatileFakeIPStore struct {
	mu        sync.Mutex
	ctx       context.Context
	validator *persistentFakeIPStore
	pool      netip.Prefix
	names     map[string]netip.Addr
	addresses map[netip.Addr]string
	next      netip.Addr
	closed    bool
}

func (s *volatileFakeIPStore) Start() error               { return s.ctx.Err() }
func (s *volatileFakeIPStore) Contains(a netip.Addr) bool { return s.pool.Contains(a) }
func (s *volatileFakeIPStore) Create(name string, v6 bool) (netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = normalizedName(name)
	if s.closed || s.ctx.Err() != nil || v6 || !s.validator.validDomain(name) {
		return netip.Addr{}, errFakeIPCache
	}
	if a, ok := s.names[name]; ok {
		return a, nil
	}
	if len(s.names) >= fakeIPMaxEntries || !s.pool.Contains(s.next.Next()) {
		return netip.Addr{}, errFakeIPCache
	}
	a := s.next
	s.next = a.Next()
	s.names[name] = a
	s.addresses[a] = name
	return a, nil
}
func (s *volatileFakeIPStore) Lookup(a netip.Addr) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return "", false
	}
	v, ok := s.addresses[a]
	return v, ok
}
func (s *volatileFakeIPStore) Reset() error { return errFakeIPCache } // Never reassign addresses within a session.
func (s *volatileFakeIPStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
