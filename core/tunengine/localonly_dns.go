package tunengine

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	mDNS "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type localOnlyDNS struct {
	p              localOnlyPrepared
	current        func(context.Context) bool
	upstreams      []dnsTransport
	mdns           dnsTransport
	patterns       [][]*regexp.Regexp
	virtualReverse []*regexp.Regexp
	limit          chan struct{}
	mu             sync.Mutex
	closed         bool
	active         sync.WaitGroup
	lifetime       context.Context
	cancel         context.CancelFunc
	closeOnce      sync.Once
	closeErr       error
}

func newLocalOnlyDNS(p localOnlyPrepared, current func(context.Context) bool, upstreams []dnsTransport, mdnsTransport dnsTransport) *localOnlyDNS {
	d := &localOnlyDNS{p: p, current: current, upstreams: upstreams, mdns: mdnsTransport, limit: make(chan struct{}, 32)}
	d.lifetime, d.cancel = context.WithCancel(context.Background())
	for _, resolver := range p.network.DNS {
		var patterns []*regexp.Regexp
		for _, name := range resolver.Domains {
			patterns = append(patterns, regexp.MustCompile(domainPattern(name)))
		}
		for _, route := range p.network.Routes {
			if route.Interface == resolver.Interface && !route.Prefix.Addr().IsLinkLocalUnicast() && !route.Prefix.Addr().IsMulticast() {
				for _, pattern := range reversePatterns(route.Prefix) {
					patterns = append(patterns, regexp.MustCompile(pattern))
				}
			}
		}
		d.patterns = append(d.patterns, patterns)
	}
	for _, pool := range localOnlyVirtualPrefixes() {
		for _, pattern := range reversePatterns(pool) {
			d.virtualReverse = append(d.virtualReverse, regexp.MustCompile(pattern))
		}
	}
	return d
}

func newNativeLocalOnlyDNS(ctx context.Context, p localOnlyPrepared, current func(context.Context) bool) (dnsTransport, error) {
	var upstreams []dnsTransport
	var mdnsTransport dnsTransport
	if p.allowLAN {
		for _, resolver := range p.network.DNS {
			address := resolver.Server.Addr()
			if address.Is6() && address.IsLinkLocalUnicast() {
				address = address.WithZone(resolver.Interface)
			}
			bound := &scopedDirect{interfaceName: resolver.Interface}
			u := newLocalOnlyTCP(ctx, bound, M.SocksaddrFrom(address, 53))
			upstreams = append(upstreams, u)
		}
		if p.network.MDNSInterface != "" {
			mdnsTransport = newMDNSTransport(ctx, p.network.MDNSInterface)
		}
	}
	return newLocalOnlyDNS(p, current, upstreams, mdnsTransport), nil
}

// One query per physical, interface-bound TCP connection. The general pooled
// TCP transport performs extra reuse probes; this deliberately sends only the
// classified question and has no retry, transport fallback or resolver fallback.
type localOnlyTCP struct {
	ctx         context.Context
	cancel      context.CancelFunc
	dial        N.Dialer
	destination M.Socksaddr
}

func newLocalOnlyTCP(ctx context.Context, bound N.Dialer, destination M.Socksaddr) *localOnlyTCP {
	ctx, cancel := context.WithCancel(ctx)
	return &localOnlyTCP{ctx: ctx, cancel: cancel, dial: bound, destination: destination}
}
func (t *localOnlyTCP) Close() error { t.cancel(); return nil }
func (t *localOnlyTCP) Start() error { return nil }
func (t *localOnlyTCP) Reset()       {}
func (t *localOnlyTCP) Exchange(ctx context.Context, m *mDNS.Msg) (*mDNS.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	unhook := context.AfterFunc(t.ctx, cancel)
	defer unhook()
	if t.ctx.Err() != nil || ctx.Err() != nil {
		return nil, errors.New("LOCAL_DNS_CANCELED")
	}
	c, err := t.dial.DialContext(ctx, "tcp", t.destination)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer closeOnCancel()
	client := &mDNS.Client{Net: "tcp", Timeout: 3 * time.Second}
	r, _, err := client.ExchangeWithConnContext(ctx, m, &mDNS.Conn{Conn: c})
	return r, err
}
func (t *localOnlyTCP) ExchangeAsync(ctx context.Context, m *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	callback(t.Exchange(ctx, m))
}

func (d *localOnlyDNS) Start() error {
	for _, u := range d.upstreams {
		if err := u.Start(); err != nil {
			return err
		}
	}
	if d.mdns != nil {
		return d.mdns.Start()
	}
	return nil
}
func (d *localOnlyDNS) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.cancel()
		d.mu.Unlock()
		for _, u := range d.upstreams {
			if err := u.Close(); err != nil {
				d.closeErr = err
			}
		}
		if d.mdns != nil {
			if err := d.mdns.Close(); err != nil {
				d.closeErr = err
			}
		}
		// Add is serialized with closed above. The owning runtime bounds waiting
		// for noncooperative OS cleanup; it never reports this as confirmed early.
		d.active.Wait()
	})
	return d.closeErr
}
func (d *localOnlyDNS) Reset() {
	for _, u := range d.upstreams {
		u.Reset()
	}
	if d.mdns != nil {
		d.mdns.Reset()
	}
}
func (d *localOnlyDNS) ExchangeAsync(ctx context.Context, m *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	callback(d.Exchange(ctx, m))
}

func (d *localOnlyDNS) Exchange(ctx context.Context, m *mDNS.Msg) (*mDNS.Msg, error) {
	if m == nil {
		return nil, errGateClosed
	}
	reply := func(code int) (*mDNS.Msg, error) { return fixedResponse(m, code), nil }
	if m.Response || m.Opcode != mDNS.OpcodeQuery || len(m.Question) != 1 || m.Question[0].Qclass != mDNS.ClassINET || m.Len() > 4096 {
		return reply(mDNS.RcodeFormatError)
	}
	q := m.Question[0]
	name := normalizedName(q.Name)
	if !validLocalOnlyName(name) {
		return reply(mDNS.RcodeFormatError)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return reply(mDNS.RcodeServerFailure)
	}
	d.active.Add(1)
	d.mu.Unlock()
	defer d.active.Done()
	unhook := context.AfterFunc(d.lifetime, cancel)
	defer unhook()
	select {
	case d.limit <- struct{}{}:
		defer func() { <-d.limit }()
	default:
		return reply(mDNS.RcodeServerFailure)
	}
	if ctx.Err() != nil || !d.current(ctx) {
		return reply(mDNS.RcodeServerFailure)
	}
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		r := fixedResponse(m, mDNS.RcodeSuccess)
		if q.Qtype == mDNS.TypeA {
			r.Answer = []mDNS.RR{&mDNS.A{Hdr: mDNS.RR_Header{Name: q.Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60}, A: net.IPv4(127, 0, 0, 1)}}
		}
		if q.Qtype == mDNS.TypeAAAA {
			r.Answer = []mDNS.RR{&mDNS.AAAA{Hdr: mDNS.RR_Header{Name: q.Name, Rrtype: mDNS.TypeAAAA, Class: mDNS.ClassINET, Ttl: 60}, AAAA: net.IPv6loopback}}
		}
		return r, nil
	}
	for _, pattern := range d.virtualReverse {
		if pattern.MatchString(name) {
			return reply(mDNS.RcodeRefused)
		}
	}
	var upstream dnsTransport
	local := localName(name) || isMDNSName(q.Name)
	if isMDNSName(q.Name) {
		if d.p.allowLAN {
			upstream = d.mdns
		}
	} else {
		for i, patterns := range d.patterns {
			for _, pattern := range patterns {
				if pattern.MatchString(name) {
					local = true
					if d.p.allowLAN && i < len(d.upstreams) {
						upstream = d.upstreams[i]
					}
					break
				}
			}
			if upstream != nil {
				break
			}
		}
	}
	if upstream == nil {
		if local {
			return reply(mDNS.RcodeServerFailure)
		}
		return reply(mDNS.RcodeRefused)
	}
	// Fresh, single-question request: no client-supplied additional records,
	// EDNS client subnet, update payload, or other tunneled query data.
	query := new(mDNS.Msg)
	query.Id = m.Id
	query.RecursionDesired = true
	query.Question = []mDNS.Question{q}
	r, err := upstream.Exchange(ctx, query)
	if err != nil || r == nil || ctx.Err() != nil || !d.current(ctx) {
		return reply(mDNS.RcodeServerFailure)
	}
	if !r.Response || len(r.Question) != 1 || !strings.EqualFold(r.Question[0].Name, q.Name) || r.Question[0].Qtype != q.Qtype || r.Question[0].Qclass != q.Qclass || r.Len() > 65535 {
		return reply(mDNS.RcodeServerFailure)
	}
	// This transport never follows CNAME/SRV targets. A client's subsequent
	// lookup re-enters classification and gets no inherited local permission.
	r = r.Copy()
	r.Id = m.Id
	return r, nil
}
