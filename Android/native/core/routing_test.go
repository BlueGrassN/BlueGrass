package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

// Only the network boundaries are fixtures: DNS policy, shared rules, FakeIP,
// protected socket control and the session's actual dial path remain real.
type routingDNSFixture struct {
	mu    sync.Mutex
	calls []string
	ips   []string
	fail  bool
	hook  func(context.Context, *mdns.Msg) (*mdns.Msg, error)
}

func (d *routingDNSFixture) Exchange(ctx context.Context, q *mdns.Msg) (*mdns.Msg, error) {
	d.mu.Lock()
	d.calls = append(d.calls, q.Question[0].Name)
	d.mu.Unlock()
	if d.hook != nil {
		return d.hook(ctx, q)
	}
	if d.fail {
		return nil, errDNS
	}
	a := new(mdns.Msg).SetReply(q)
	for _, ip := range d.ips {
		a.Answer = append(a.Answer, &mdns.A{Hdr: mdns.RR_Header{Name: q.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60}, A: net.ParseIP(ip)})
	}
	return a, nil
}
func (d *routingDNSFixture) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.calls) }

func routingSession(t *testing.T, mode routingpolicy.Mode) (*Session, *routingDNSFixture, *routingDNSFixture, *flowDialFixture, *coreProtectionFixture, *atomic.Bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	valid := new(atomic.Bool)
	valid.Store(true)
	l := newSessionLease(func() bool { return ctx.Err() == nil && valid.Load() })
	callback := &coreProtectionFixture{called: make(chan struct{}, 32)}
	p := &platformBridge{callback: callback, info: fixtureNetwork, lease: l}
	out := &flowDialFixture{}
	r, err := newDNSRouter(ctx, p, out, mode)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	domestic, selected := &routingDNSFixture{ips: []string{"8.8.8.8"}}, &routingDNSFixture{ips: []string{"223.5.5.5"}}
	r.domestic, r.public = domestic, selected
	s := &Session{ctx: ctx, cancel: cancel, lease: l, platform: p, resolver: r, selected: out}
	t.Cleanup(func() { s.Close(); out.Close() })
	return s, domestic, selected, out, callback, valid
}

func routingAnswerIP(t *testing.T, s *Session, host string) netip.Addr {
	t.Helper()
	a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion(mdns.Fqdn(host), mdns.TypeA))
	if err != nil || a == nil || len(a.Answer) == 0 {
		t.Fatalf("DNS %s: %v %v", host, a, err)
	}
	rr, ok := a.Answer[0].(*mdns.A)
	if !ok {
		t.Fatalf("not an A: %v", a)
	}
	ip, ok := netip.AddrFromSlice(rr.A)
	if !ok {
		t.Fatal("invalid address")
	}
	return ip.Unmap()
}

func TestRoutingDNSDomainPriorityAndSharedIPIsolation(t *testing.T) {
	s, domestic, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	domestic.ips = []string{"223.5.5.5"}
	cn := routingAnswerIP(t, s, "baidu.com")
	foreign := routingAnswerIP(t, s, "google.com")
	if cn == foreign || !netip.MustParsePrefix("198.18.0.0/15").Contains(cn) || !netip.MustParsePrefix("198.18.0.0/15").Contains(foreign) {
		t.Fatalf("domain identity lost at shared IP: %s %s", cn, foreign)
	}
	if domestic.count() != 1 || selected.count() != 1 {
		t.Fatalf("wrong DNS: domestic=%d selected=%d", domestic.count(), selected.count())
	}
	for _, network := range []string{"tcp", "udp"} {
		before := protected.calls.Load()
		c, err := s.dial(context.Background(), network, M.SocksaddrFrom(cn, 443))
		if c != nil {
			c.Close()
		}
		if err == nil || protected.calls.Load() != before+1 || out.count() != 0 {
			t.Fatal("CN did not use protected direct; or fell back after protection failure")
		}
		c, err = s.dial(context.Background(), network, M.SocksaddrFrom(foreign, 443))
		if err != nil {
			t.Fatal("foreign domain with CN IP did not proxy", err)
		}
		c.Close()
		out.mu.Lock()
		last := out.calls[len(out.calls)-1]
		out.mu.Unlock()
		if last != network+":223.5.5.5:443" {
			t.Fatalf("proxy did not dial exact resolved IP: %s", last)
		}
		// Reset only the recording fixture, not the policy or FakeIP bindings.
		out.mu.Lock()
		out.calls = nil
		out.mu.Unlock()
	}
}

func TestRoutingNamesAndLiteralIPs(t *testing.T) {
	for _, mode := range []routingpolicy.Mode{routingpolicy.Global, routingpolicy.BypassCN} {
		for _, network := range []string{"tcp", "udp"} {
			for _, tt := range []struct {
				host, ip       string
				direct, denied bool
			}{
				{"baidu.com", "8.8.8.8", true, false}, {"google.com", "223.5.5.5", false, false},
				{"unlisted-routing-fixture.invalid", "223.5.5.5", true, false}, {"unlisted-routing-fixture.invalid", "8.8.8.8", false, false},
				{"223.5.5.5", "", true, false}, {"8.8.8.8", "", false, false},
				{"192.168.8.100", "", true, false}, {"10.99.0.1", "", false, true},
				{"2001:4860:4860::8888", "", false, true}, {"198.19.255.254", "", false, true},
			} {
				t.Run(string(mode)+"/"+network+"/"+tt.host+"/"+tt.ip, func(t *testing.T) {
					s, domestic, selected, out, protected, _ := routingSession(t, mode)
					domestic.ips, selected.ips = []string{tt.ip}, []string{tt.ip}
					c, err := s.dial(context.Background(), network, M.ParseSocksaddr(net.JoinHostPort(tt.host, "443")))
					if c != nil {
						c.Close()
					}
					direct := tt.direct && (mode == routingpolicy.BypassCN || tt.host == "192.168.8.100")
					if tt.denied {
						if err == nil || out.count() != 0 || protected.calls.Load() != 0 {
							t.Fatal("blocked target escaped")
						}
						return
					}
					if direct {
						if err == nil || protected.calls.Load() != 1 || out.count() != 0 {
							t.Fatal("missing protected direct or fallback occurred")
						}
					} else if err != nil || out.count() != 1 || protected.calls.Load() != 0 {
						t.Fatal("missing proxy", err)
					}
				})
			}
		}
	}
}

func TestRoutingDNSGlobalUnchangedAndProbeAlwaysSelected(t *testing.T) {
	s, domestic, selected, _, _, _ := routingSession(t, routingpolicy.Global)
	if ip := routingAnswerIP(t, s, "baidu.com"); ip.String() != "223.5.5.5" || domestic.count() != 0 || selected.count() != 1 {
		t.Fatal("global DNS changed")
	}
	s2, domestic2, selected2, out2, protected2, _ := routingSession(t, routingpolicy.BypassCN)
	// Even a CN-classified probe lookup is explicitly selected, without FakeIP.
	ips, err := s2.resolver.LookupPublic(context.Background(), "baidu.com")
	if err != nil || len(ips) != 1 || ips[0].String() != "223.5.5.5" || domestic2.count() != 0 || selected2.count() != 1 {
		t.Fatal("probe lookup followed bypass mode")
	}
	c, err := probeDialer(s2.resolver, out2)(context.Background(), "tcp", "api.ipify.org:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if out2.count() != 1 || protected2.calls.Load() != 0 {
		t.Fatal("probe left selected outbound")
	}
}

func TestRoutingCanceledCallerCannotDial(t *testing.T) {
	s, domestic, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, host := range []string{"8.8.8.8", "223.5.5.5", "baidu.com"} {
		c, err := s.dial(ctx, "tcp", M.ParseSocksaddr(net.JoinHostPort(host, "443")))
		if c != nil {
			c.Close()
		}
		if err == nil {
			t.Fatal("canceled caller dialed")
		}
	}
	if domestic.count()+selected.count()+out.count() != 0 || protected.calls.Load() != 0 {
		t.Fatal("cancellation touched network")
	}
}

func TestRoutingDNSRejectsUnrelatedAndUnsafeAnswers(t *testing.T) {
	for _, rr := range []string{
		"attacker.example. 60 IN A 8.8.8.8", "baidu.com. 60 IN A 192.168.8.100",
		"baidu.com. 60 IN A 198.18.0.9", "baidu.com. 60 IN A 127.0.0.1",
	} {
		t.Run(rr, func(t *testing.T) {
			s, domestic, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
			bad := func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
				a := new(mdns.Msg).SetReply(q)
				r, e := mdns.NewRR(rr)
				if e != nil {
					t.Fatal(e)
				}
				a.Answer = []mdns.RR{r}
				return a, nil
			}
			domestic.hook, selected.hook = bad, bad
			c, err := s.dial(context.Background(), "tcp", M.ParseSocksaddr("baidu.com:443"))
			if c != nil {
				c.Close()
			}
			if err == nil || out.count() != 0 || protected.calls.Load() != 0 {
				t.Fatal("DNS authorized an unsafe/unrelated target")
			}
			a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("baidu.com.", mdns.TypeA))
			if err == nil && len(a.Answer) > 0 {
				t.Fatal("unsafe/unrelated DNS answer escaped")
			}
		})
	}
}

func TestRoutingDNSNoFallbackAndLateRevocation(t *testing.T) {
	for _, phase := range []string{"failure", "caller", "session", "lease"} {
		t.Run(phase, func(t *testing.T) {
			s, domestic, selected, out, protected, valid := routingSession(t, routingpolicy.BypassCN)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			domestic.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
				switch phase {
				case "failure":
					return nil, errDNS
				case "caller":
					cancel()
				case "session":
					s.cancel()
				case "lease":
					valid.Store(false)
				}
				a := new(mdns.Msg).SetReply(q)
				rr, _ := mdns.NewRR("baidu.com. 60 IN A 8.8.8.8")
				a.Answer = []mdns.RR{rr}
				return a, nil
			}
			c, err := s.dial(ctx, "tcp", M.ParseSocksaddr("baidu.com:443"))
			if c != nil {
				c.Close()
			}
			if err == nil || domestic.count() != 1 || selected.count() != 0 || out.count() != 0 || protected.calls.Load() != 0 {
				t.Fatal("failed/revoked domestic DNS escaped or fell back")
			}
		})
	}
}
