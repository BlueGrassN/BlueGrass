package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"bluegrass/native/core/routingpolicy"
	"github.com/miekg/dns"
)

type dnsTransport interface {
	Start() error
	Close() error
	Reset()
	Exchange(context.Context, *dns.Msg) (*dns.Msg, error)
}
type fakeIPStore interface {
	Start() error
	Close() error
	Create(string, bool) (netip.Addr, error)
	Lookup(netip.Addr) (string, bool)
	Contains(netip.Addr) bool
	Reset() error
}

func fixedResponse(m *dns.Msg, code int) *dns.Msg { return new(dns.Msg).SetRcode(m, code) }
func isMDNSName(name string) bool {
	name = normalizedName(name)
	if name == "local" || strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".254.169.in-addr.arpa") {
		return true
	}
	for _, suffix := range []string{".8.e.f.ip6.arpa", ".9.e.f.ip6.arpa", ".a.e.f.ip6.arpa", ".b.e.f.ip6.arpa"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

type policyDNS struct {
	config      nativeConfig
	transports  map[string]dnsTransport
	routingMode routingpolicy.Mode
	rules       *routingpolicy.Rules
}

func (d *policyDNS) Exchange(ctx context.Context, m *dns.Msg) (*dns.Msg, error) {
	if m == nil {
		return nil, errGateClosed
	}
	if m.Response || m.Opcode != dns.OpcodeQuery || len(m.Question) != 1 || m.Question[0].Qclass != dns.ClassINET || m.Len() > 4096 || !validLocalOnlyName(normalizedName(m.Question[0].Name)) {
		return fixedResponse(m, dns.RcodeFormatError), nil
	}
	q := m.Question[0]
	r := d.config.dnsRule(q.Name, q.Qtype)
	if r.Action == "predefined" {
		reply := fixedResponse(m, dns.StringToRcode[r.Rcode])
		for _, v := range r.Answer {
			rr, e := dns.NewRR(v)
			if e != nil {
				return nil, e
			}
			rr.Header().Name = q.Name
			reply.Answer = append(reply.Answer, rr)
		}
		return reply, nil
	}
	if r.Server == "public" && d.routingMode == routingpolicy.BypassCN {
		// Address hints can let applications skip the A/FakeIP lookup and lose
		// the original domain before business routing. Return NODATA so they
		// use that path; local DNS and global routing keep their existing policy.
		if q.Qtype == dns.TypeHTTPS || q.Qtype == dns.TypeSVCB || q.Qtype == dns.TypeANY {
			return fixedResponse(m, dns.RcodeSuccess), nil
		}
		if d.rules.Domain(q.Name) == routingpolicy.Direct {
			r.Server = "domestic"
		}
	}
	upstream := d.transports[r.Server]
	if upstream == nil {
		return fixedResponse(m, dns.RcodeServerFailure), nil
	}
	clean := new(dns.Msg)
	clean.Id = m.Id
	clean.RecursionDesired = true
	clean.Question = []dns.Question{q}
	reply, e := upstream.Exchange(ctx, clean)
	if e != nil || reply == nil || ctx.Err() != nil {
		return fixedResponse(m, dns.RcodeServerFailure), nil
	}
	if !reply.Response || len(reply.Question) != 1 || !strings.EqualFold(reply.Question[0].Name, q.Name) || reply.Question[0].Qtype != q.Qtype || reply.Question[0].Qclass != q.Qclass {
		return fixedResponse(m, dns.RcodeServerFailure), nil
	}
	return reply, nil
}
func lookupIPv4(ctx context.Context, transport dnsTransport, host string) ([]netip.Addr, error) {
	m := new(dns.Msg).SetQuestion(dns.Fqdn(host), dns.TypeA)
	r, e := transport.Exchange(ctx, m)
	if e != nil || r == nil || !r.Response || r.Rcode != dns.RcodeSuccess || len(r.Question) != 1 || !strings.EqualFold(r.Question[0].Name, m.Question[0].Name) || r.Question[0].Qtype != dns.TypeA || r.Question[0].Qclass != dns.ClassINET {
		return nil, errors.New("DNS_LOOKUP_FAILED")
	}
	var addresses []netip.Addr
	// Only accept A records reachable from the queried name via a bounded CNAME chain.
	names := map[string]bool{normalizedName(host): true}
	for range 8 {
		changed := false
		for _, rr := range r.Answer {
			if c, ok := rr.(*dns.CNAME); ok && names[normalizedName(c.Hdr.Name)] && !names[normalizedName(c.Target)] {
				names[normalizedName(c.Target)] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok && names[normalizedName(a.Hdr.Name)] {
			v, ok := netip.AddrFromSlice(a.A.To4())
			if ok {
				addresses = append(addresses, v)
			}
		}
	}
	return addresses, nil
}
