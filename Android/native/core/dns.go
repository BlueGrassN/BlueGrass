package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"errors"
	mdns "github.com/miekg/dns"
	"net"
	"net/netip"
	"strings"
	"time"
)

var errDNS = errors.New("DNS_FAILED")

type dnsExchange interface {
	Exchange(context.Context, *mdns.Msg) (*mdns.Msg, error)
}
type dnsRouter struct {
	ctx                      context.Context
	platform                 *platformBridge
	public, local, multicast dnsExchange
	domestic                 dnsExchange
	mode                     routingpolicy.Mode
	rules                    *routingpolicy.Rules
	fake                     fakeNames
}

func newDNSRouter(ctx context.Context, p *platformBridge, out streamDialer, mode routingpolicy.Mode) (*dnsRouter, error) {
	if mode != routingpolicy.Global && mode != routingpolicy.BypassCN {
		return nil, errConfig
	}
	var rules *routingpolicy.Rules
	if mode == routingpolicy.BypassCN {
		var err error
		rules, err = currentRoutingRules()
		if err != nil {
			return nil, errStart
		}
	}
	m, err := newAndroidMDNS(ctx, p)
	if err != nil {
		return nil, err
	}
	r := &dnsRouter{ctx: ctx, platform: p, public: &dnsTransport{dialer: out, network: "tcp", address: "1.1.1.1:53"}, multicast: m}
	r.mode, r.rules = mode, rules
	r.domestic = &dnsTransport{dialer: p, network: "udp", address: net.JoinHostPort(routingpolicy.DomesticDNS, "53")}
	if server := dnsConnectedResolver(p.info); server != "" {
		r.local = &dnsTransport{dialer: p, network: "udp", address: net.JoinHostPort(server, "53")}
	}
	return r, nil
}
func (r *dnsRouter) Close() error {
	r.fake.close()
	if m, ok := r.multicast.(interface{ Close() error }); ok {
		return m.Close()
	}
	return nil
}
func dnsNameUnder(name, suffix string) bool {
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}
func (r *dnsRouter) Exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, error) {
	if m == nil || m.Response || m.Opcode != mdns.OpcodeQuery || len(m.Question) != 1 {
		return nil, errDNS
	}
	q := m.Question[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	if _, ok := mdns.IsDomainName(q.Name); !ok || q.Qclass != mdns.ClassINET || q.Qtype == 0 || q.Qtype == mdns.TypeAXFR || q.Qtype == mdns.TypeIXFR {
		return nil, errDNS
	}
	if ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	reply := func(code int) (*mdns.Msg, error) { return new(mdns.Msg).SetRcode(m, code), nil }
	if dnsNameUnder(name, "localhost") {
		return reply(mdns.RcodeSuccess)
	}
	var target dnsExchange
	public := false
	switch {
	case dnsNameUnder(name, "lan"), dnsNameUnder(name, "home.arpa"):
		target = r.local
	case dnsNameUnder(name, "local"):
		if _, ok := mdnsPhysicalIPv4(r.platform.info); ok {
			target = r.multicast
		}
	default:
		public = true
		if !strings.Contains(name, ".") {
			return reply(mdns.RcodeServerFailure)
		}
		if q.Qtype == mdns.TypeAAAA {
			return reply(mdns.RcodeSuccess)
		}
		target = r.public
		if r.mode == routingpolicy.BypassCN {
			if r.rules == nil {
				return nil, errDNS
			}
			// Hints/ANY can supply literal IPs without the A lookup that retains
			// the original domain. Clients fall back to ordinary A queries.
			if q.Qtype == mdns.TypeHTTPS || q.Qtype == mdns.TypeSVCB || q.Qtype == mdns.TypeANY {
				return reply(mdns.RcodeSuccess)
			}
			if r.rules.Domain(name) == routingpolicy.Direct {
				target = r.domestic
			}
		}
	}
	if target == nil {
		return reply(mdns.RcodeServerFailure)
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	answer, err := target.Exchange(call, m)
	if err != nil || answer == nil || call.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	if public && r.mode == routingpolicy.BypassCN {
		if q.Qtype == mdns.TypeA {
			return r.fakeAnswer(m, answer, name)
		}
		answer = answer.Copy()
		answer.Answer = withoutAddressHints(answer.Answer)
		answer.Ns = withoutAddressHints(answer.Ns)
		answer.Extra = withoutAddressHints(answer.Extra)
		answer.AuthenticatedData = false
	}
	return answer, nil
}
func (r *dnsRouter) LookupPublic(ctx context.Context, host string) ([]netip.Addr, error) {
	// Exit observation explicitly uses this selected-only lookup in both modes.
	return r.lookup(ctx, host, r.public)
}
func (r *dnsRouter) lookup(ctx context.Context, host string, target dnsExchange) ([]netip.Addr, error) {
	if ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	m := new(mdns.Msg).SetQuestion(mdns.Fqdn(host), mdns.TypeA)
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	if target == nil {
		return nil, errDNS
	}
	answer, err := target.Exchange(call, m)
	if err != nil || call.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	addresses, err := answerIPv4(m, answer)
	if err != nil || len(addresses) == 0 {
		return nil, errDNS
	}
	return addresses, nil
}

type dnsTransport struct {
	dialer           streamDialer
	network, address string
}

func (t *dnsTransport) Exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, error) {
	c, err := t.dialer.DialContext(ctx, t.network, t.address)
	if err != nil {
		return nil, errDNS
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if c.SetDeadline(deadline) != nil {
		return nil, errDNS
	}
	var wire net.Conn = c
	if t.network == "udp" {
		wire = &dnsDatagramConn{Conn: c}
	}
	dc := &mdns.Conn{Conn: wire, UDPSize: 65535}
	if dc.WriteMsg(m) != nil {
		return nil, errDNS
	}
	answer, err := dc.ReadMsg()
	if err != nil || !answer.Response || answer.Id != m.Id || answer.Opcode != m.Opcode || len(answer.Question) != 1 || !strings.EqualFold(answer.Question[0].Name, m.Question[0].Name) || answer.Question[0].Qtype != m.Question[0].Qtype || answer.Question[0].Qclass != m.Question[0].Qclass {
		return nil, errDNS
	}
	if answer.Truncated {
		if t.network == "udp" {
			// Retry only the same captured local resolver, still through Protect.
			// A failed local lookup must never switch to the public transport.
			return (&dnsTransport{dialer: t.dialer, network: "tcp", address: t.address}).Exchange(ctx, m)
		}
		return nil, errDNS
	}
	return answer, nil
}

// miekg/dns distinguishes datagrams through net.PacketConn, even when the
// connected UDP socket is wrapped by the session's per-read/per-write lease.
type dnsDatagramConn struct{ net.Conn }

func (c *dnsDatagramConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, e := c.Read(b)
	return n, c.RemoteAddr(), e
}
func (c *dnsDatagramConn) WriteTo(b []byte, _ net.Addr) (int, error) { return c.Write(b) }
