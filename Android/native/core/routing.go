package bridge

import (
	"bluegrass/native/core/gate"
	"bluegrass/native/core/routingpolicy"
	"context"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mdns "github.com/miekg/dns"
)

var fakeRange = netip.MustParsePrefix("198.18.0.0/15")

// Never reuse an address during this process, including across concurrent or
// reconnected sessions. A stale application DNS cache can only hit its original
// session binding or fail closed. Exhaustion cannot wrap or evict another name.
var processFakeSequence atomic.Uint32

const maxSessionFakeNames = 8192
const maxSessionFakeBindings = 8192
const maxFakeSequence = (1 << 17) - 2

type fakeBinding struct {
	name      string
	candidate netip.Addr
}

type fakeNames struct {
	mu      sync.Mutex
	closed  bool
	byName  map[string]map[netip.Addr]netip.Addr
	byIP    map[netip.Addr]fakeBinding
	answers map[string]routingAnswer
	flights map[string]*routingFlight
}

func (f *fakeNames) reserve(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reserveLocked(name)
}

func (f *fakeNames) reserveLocked(name string) error {
	if f.closed {
		return errDNS
	}
	if _, ok := f.byName[name]; ok {
		return nil
	}
	if len(f.byName) >= maxSessionFakeNames {
		return errDNS
	}
	if f.byName == nil {
		f.byName = make(map[string]map[netip.Addr]netip.Addr)
	}
	f.byName[name] = make(map[netip.Addr]netip.Addr)
	return nil
}

// Allocate the entire answer atomically. Bindings outlive DNS TTLs and are
// never retargeted, including when the same name gains different candidates.
func (f *fakeNames) allocate(name string, candidates []netip.Addr) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || len(candidates) == 0 || len(candidates) > maxRoutingCandidates {
		return nil, errDNS
	}
	newCandidates := make(map[netip.Addr]bool)
	for _, candidate := range candidates {
		if !gate.PublicIPv4(candidate.String()) {
			return nil, errDNS
		}
		if _, ok := f.byName[name][candidate]; !ok {
			newCandidates[candidate] = true
		}
	}
	if len(f.byIP)+len(newCandidates) > maxSessionFakeBindings {
		return nil, errDNS
	}
	if err := f.reserveLocked(name); err != nil {
		return nil, err
	}
	count := uint32(len(newCandidates))
	var n uint32
	for {
		n = processFakeSequence.Load()
		if count > maxFakeSequence-n {
			return nil, errDNS
		}
		if count == 0 || processFakeSequence.CompareAndSwap(n, n+count) {
			break
		}
	}
	if f.byIP == nil {
		f.byIP = make(map[netip.Addr]fakeBinding)
	}
	result := make([]netip.Addr, 0, len(candidates))
	for _, candidate := range candidates {
		ip, ok := f.byName[name][candidate]
		if !ok {
			n++
			ip = netip.AddrFrom4([4]byte{198, 18 + byte(n>>16), byte(n >> 8), byte(n)})
			f.byName[name][candidate] = ip
			f.byIP[ip] = fakeBinding{name: name, candidate: candidate}
		}
		result = append(result, ip)
	}
	return result, nil
}
func (f *fakeNames) lookup(ip netip.Addr) (fakeBinding, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.byIP[ip]
	return name, ok && !f.closed
}
func (f *fakeNames) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for name := range f.flights {
		f.stopFlightLocked(name)
	}
	f.byName = nil
	f.byIP = nil
	f.answers = nil
	f.flights = nil
}

func (r *dnsRouter) resolve(ctx context.Context, host string) (routingpolicy.Route, error) {
	routes, err := r.resolveCandidates(ctx, host)
	if err != nil {
		return routingpolicy.Route{}, err
	}
	return routes[0], nil
}

func (r *dnsRouter) resolveCandidates(ctx context.Context, host string) ([]routingpolicy.Route, error) {
	if ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	var candidates []netip.Addr
	lookup := func(target dnsExchange) routingpolicy.Lookup {
		return func(ctx context.Context, name string) ([]netip.Addr, error) {
			var err error
			if r.mode == routingpolicy.BypassCN {
				candidates, err = r.lookupRouting(ctx, name, target)
			} else {
				candidates, err = r.lookup(ctx, name, target)
			}
			return candidates, err
		}
	}
	route, err := routingpolicy.Resolve(ctx, r.mode, r.rules, host,
		lookup(r.domestic), lookup(r.public))
	if err != nil || ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	if len(candidates) == 0 {
		return []routingpolicy.Route{route}, nil
	}
	routes := make([]routingpolicy.Route, 0, len(candidates))
	for _, ip := range candidates {
		if !gate.PublicIPv4(ip.String()) {
			return nil, errDNS
		}
		routes = append(routes, routingpolicy.Route{Address: ip, Direct: r.mode == routingpolicy.BypassCN && r.rules.ShouldDirect(host, ip)})
	}
	return routes, nil
}

const maxRoutingTTL uint32 = 300
const maxRoutingCandidates = 256

// Positive answers retain the complete validated candidate set, or fail closed
// if it exceeds the memory bound. Zero TTL, NXDOMAIN and NODATA are not cached.
type routingAnswer struct {
	addresses []netip.Addr
	ttl       uint32
	expires   time.Time
	rcode     int
	recursive bool
}

// Only A records owned by the original question or its validated CNAME chain
// may authorize a connection. Ignore additional/unrelated records; never let a
// public lookup enter LAN routing, IPv6, or the synthetic address space.
func answerIPv4(q, answer *mdns.Msg) ([]netip.Addr, error) {
	a, err := validatedRoutingAnswer(q, answer)
	if err != nil || a.rcode != mdns.RcodeSuccess {
		return nil, errDNS
	}
	return a.addresses, nil
}

func validatedRoutingAnswer(q, answer *mdns.Msg) (routingAnswer, error) {
	a := routingAnswer{ttl: maxRoutingTTL}
	if answer == nil || !answer.Response || answer.Truncated || answer.Id != q.Id || answer.Opcode != q.Opcode || len(answer.Question) != 1 || !strings.EqualFold(answer.Question[0].Name, q.Question[0].Name) || answer.Question[0].Qtype != q.Question[0].Qtype || answer.Question[0].Qclass != mdns.ClassINET {
		return a, errDNS
	}
	a.rcode, a.recursive = answer.Rcode, answer.RecursionAvailable
	if answer.Rcode != mdns.RcodeSuccess {
		return a, nil
	}
	name := strings.ToLower(q.Question[0].Name)
	seen := make(map[string]bool)
	for depth := 0; depth < 16; depth++ {
		if seen[name] {
			return a, errDNS
		}
		seen[name] = true
		var next string
		var ips []netip.Addr
		unique := make(map[netip.Addr]bool)
		for _, rr := range answer.Answer {
			if rr.Header().Class != mdns.ClassINET || !strings.EqualFold(rr.Header().Name, name) {
				continue
			}
			switch rr := rr.(type) {
			case *mdns.CNAME:
				a.ttl = min(a.ttl, rr.Hdr.Ttl)
				target := strings.ToLower(rr.Target)
				if next != "" && next != target {
					return a, errDNS
				}
				next = target
			case *mdns.A:
				a.ttl = min(a.ttl, rr.Hdr.Ttl)
				ip, ok := netip.AddrFromSlice(rr.A)
				if !ok || !gate.PublicIPv4(ip.Unmap().String()) {
					return a, errDNS
				}
				ip = ip.Unmap()
				if !unique[ip] {
					unique[ip] = true
					ips = append(ips, ip)
				}
				if len(ips) > maxRoutingCandidates {
					return a, errDNS
				}
			}
		}
		if next == "" {
			a.addresses = ips
			a.expires = time.Now().Add(time.Duration(a.ttl) * time.Second)
			return a, nil
		}
		if len(ips) != 0 {
			return a, errDNS
		}
		name = next
	}
	return a, errDNS
}

func (r *dnsRouter) fakeAnswer(m, answer *mdns.Msg, name string) (*mdns.Msg, error) {
	a, err := validatedRoutingAnswer(m, answer)
	if err != nil {
		return nil, err
	}
	// Synthetic answers cannot claim DNSSEC validation. Drop address hints and
	// real A/AAAA records in every section: they would discard domain identity.
	reply := new(mdns.Msg).SetRcode(m, answer.Rcode)
	reply.RecursionAvailable = answer.RecursionAvailable
	if len(a.addresses) == 0 {
		// A newer negative answer invalidates a previously seeded positive one.
		_ = r.fake.seed(name, a)
		return reply, nil
	}
	ips, err := r.fake.allocate(name, a.addresses)
	if err != nil {
		return nil, err
	}
	if err = r.fake.seed(name, a); err != nil {
		return nil, err
	}
	for _, ip := range ips {
		reply.Answer = append(reply.Answer, &mdns.A{Hdr: mdns.RR_Header{Name: m.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: a.ttl}, A: ip.AsSlice()})
	}
	return reply, nil
}

func withoutAddressHints(records []mdns.RR) []mdns.RR {
	result := make([]mdns.RR, 0, len(records))
	for _, rr := range records {
		switch rr.Header().Rrtype {
		case mdns.TypeA, mdns.TypeAAAA, mdns.TypeSVCB, mdns.TypeHTTPS:
			continue
		}
		result = append(result, rr)
	}
	return result
}
