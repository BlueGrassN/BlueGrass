// Package entrydns resolves only proxy entry names through a bounded explicit
// policy. It never handles application DNS or consults the system resolver.
package entrydns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type Candidate struct {
	Address   string `json:"address"`
	Port      uint16 `json:"port"`
	Transport string `json:"transport"`
}
type Policy struct {
	Mode       string      `json:"mode"`
	Candidates []Candidate `json:"candidates"`
}

func (p Policy) Validate() error {
	if p.Mode != "" && p.Mode != "auto" && p.Mode != "custom" {
		return errors.New("INVALID_ENTRY_DNS")
	}
	if p.Mode != "custom" {
		if len(p.Candidates) != 0 {
			return errors.New("INVALID_ENTRY_DNS")
		}
		return nil
	}
	if len(p.Candidates) == 0 || len(p.Candidates) > 8 {
		return errors.New("INVALID_ENTRY_DNS")
	}
	for _, c := range p.Candidates {
		ip, err := netip.ParseAddr(c.Address)
		if err != nil || ip.Zone() != "" || ip.Is4In6() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.Is6() && ip.IsLinkLocalUnicast() || c.Transport != "udp" && c.Transport != "tcp" {
			return errors.New("INVALID_ENTRY_DNS")
		}
	}
	return nil
}

func (c Candidate) Endpoint() netip.AddrPort {
	port := c.Port
	if port == 0 {
		port = 53
	}
	return netip.AddrPortFrom(netip.MustParseAddr(c.Address), port)
}

// Effective preserves custom ordering and never adds automatic candidates to it.
// Automatic mode tries physical-network DNS, then the two fixed public backups
// shown by the application. Custom mode never reaches these backups.
func (p Policy) Effective(auto []netip.AddrPort) ([]Candidate, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Mode == "custom" {
		return append([]Candidate(nil), p.Candidates...), nil
	}
	var result []Candidate
	seen := map[netip.AddrPort]bool{}
	ordered := append([]netip.AddrPort(nil), auto...)
	if len(ordered) > 8 {
		ordered = ordered[:8]
	}
	ordered = append(ordered, netip.MustParseAddrPort("1.1.1.1:53"), netip.MustParseAddrPort("223.5.5.5:53"))
	for _, endpoint := range ordered {
		if !endpoint.IsValid() || endpoint.Port() == 0 || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		c := Candidate{Address: endpoint.Addr().String(), Port: endpoint.Port(), Transport: "udp"}
		if (Policy{Mode: "custom", Candidates: []Candidate{c}}).Validate() != nil {
			continue
		}
		result = append(result, c)
		c.Transport = "tcp"
		result = append(result, c)
	}
	if len(result) == 0 {
		return nil, errors.New("ENTRY_DNS_UNAVAILABLE")
	}
	return result, nil
}

type exchangeFunc func(context.Context, Candidate, *dns.Msg) (*dns.Msg, error)

func Resolve(ctx context.Context, host string, policy Policy, auto []netip.AddrPort) (netip.Addr, error) {
	return resolveWithDial(ctx, host, policy, auto, (&net.Dialer{}).DialContext)
}

// All DNS I/O uses the supplied connected socket. A binding failure never
// falls back to Client.ExchangeContext (which would open an unbound socket).
func resolveWithDial(ctx context.Context, host string, policy Policy, auto []netip.AddrPort, dial func(context.Context, string, string) (net.Conn, error)) (netip.Addr, error) {
	return resolve(ctx, host, policy, auto, func(ctx context.Context, c Candidate, q *dns.Msg) (*dns.Msg, error) {
		conn, err := dial(ctx, c.Transport, c.Endpoint().String())
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, err
		}
		if conn == nil {
			return nil, errors.New("ENTRY_DNS_SOCKET_MISSING")
		}
		defer conn.Close()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		answer, _, err := (&dns.Client{Net: c.Transport, Timeout: 5 * time.Second}).ExchangeWithConnContext(ctx, q, &dns.Conn{Conn: conn})
		return answer, err
	})
}

func resolve(ctx context.Context, host string, policy Policy, auto []netip.AddrPort, exchange exchangeFunc) (netip.Addr, error) {
	if err := policy.Validate(); err != nil {
		return netip.Addr{}, err
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, nil
	}
	if len(host) > 253 || strings.ContainsAny(host, "\x00\r\n /\\") {
		return netip.Addr{}, errors.New("INVALID_ENTRY_HOST")
	}
	name := dns.Fqdn(strings.ToLower(host))
	if _, ok := dns.IsDomainName(name); !ok || name == "." {
		return netip.Addr{}, errors.New("INVALID_ENTRY_HOST")
	}
	candidates, err := policy.Effective(auto)
	if err != nil {
		return netip.Addr{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for i, candidate := range candidates {
		if ctx.Err() != nil {
			break
		}
		deadline, _ := ctx.Deadline()
		budget := time.Until(deadline) / time.Duration(len(candidates)-i)
		attempt, done := context.WithTimeout(ctx, budget)
		query := new(dns.Msg)
		query.SetQuestion(name, dns.TypeA)
		answer, err := exchange(attempt, candidate, query)
		expired := attempt.Err() != nil
		done()
		if ctx.Err() != nil {
			break
		}
		if err != nil || expired || answer == nil || !answer.Response || answer.Id != query.Id || len(answer.Question) != 1 || answer.Question[0] != query.Question[0] || answer.Truncated {
			continue
		}
		if answer.Rcode == dns.RcodeNameError {
			return netip.Addr{}, errors.New("ENTRY_DNS_NXDOMAIN")
		}
		if answer.Rcode != dns.RcodeSuccess {
			continue
		}
		// Accept only the queried name and a bounded CNAME chain from this response.
		names := map[string]bool{name: true}
		for hop := 0; hop < 8; hop++ {
			changed := false
			for _, rr := range answer.Answer {
				if cname, ok := rr.(*dns.CNAME); ok && names[strings.ToLower(cname.Hdr.Name)] && !names[strings.ToLower(cname.Target)] {
					names[strings.ToLower(cname.Target)] = true
					changed = true
				}
			}
			if !changed {
				break
			}
		}
		for _, rr := range answer.Answer {
			if a, ok := rr.(*dns.A); ok && a.Hdr.Class == dns.ClassINET && names[strings.ToLower(a.Hdr.Name)] {
				ip, valid := netip.AddrFromSlice(a.A)
				if valid && ip.Unmap().Is4() {
					return ip.Unmap(), nil
				}
			}
		}
	}
	return netip.Addr{}, errors.New("ENTRY_DNS_FAILED")
}
