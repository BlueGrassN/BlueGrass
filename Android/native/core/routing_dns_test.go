package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestRoutingDomesticTransportProtectsAndCannotFallback(t *testing.T) {
	s, _, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	r, err := newDNSRouter(s.ctx, s.platform, out, routingpolicy.BypassCN)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.public = selected
	tr, ok := r.domestic.(*dnsTransport)
	if !ok || tr.address != "223.5.5.5:53" || tr.dialer != s.platform {
		t.Fatal("domestic DNS lost protected fixed endpoint")
	}
	if _, err = r.Exchange(context.Background(), new(mdns.Msg).SetQuestion("baidu.com.", mdns.TypeA)); err == nil || protected.calls.Load() != 1 || selected.count() != 0 || out.count() != 0 {
		t.Fatal("domestic DNS failed protection or fell back")
	}
}

func TestRoutingDNSCNAMEKeepsOriginalDomainPolicy(t *testing.T) {
	s, domestic, _, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	domestic.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		a := new(mdns.Msg).SetReply(q)
		for _, text := range []string{"baidu.com. 60 IN CNAME foreign-cdn.invalid.", "unrelated.invalid. 60 IN A 192.168.8.100", "foreign-cdn.invalid. 60 IN A 8.8.8.8"} {
			rr, err := mdns.NewRR(text)
			if err != nil {
				t.Fatal(err)
			}
			a.Answer = append(a.Answer, rr)
		}
		return a, nil
	}
	ip := routingAnswerIP(t, s, "baidu.com")
	route, err := s.resolver.resolve(context.Background(), "baidu.com")
	if err != nil || !route.Direct || route.Address.String() != "8.8.8.8" {
		t.Fatal("CNAME changed original classification", route, err)
	}
	if !fakeRange.Contains(ip) || out.count() != 0 || protected.calls.Load() != 0 {
		t.Fatal("unexpected I/O")
	}
}

func TestRoutingDNSCannotEscapeViaHintsOrAdditionalAddresses(t *testing.T) {
	s, domestic, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	for _, qtype := range []uint16{mdns.TypeAAAA, mdns.TypeHTTPS, mdns.TypeSVCB, mdns.TypeANY} {
		a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("google.com.", qtype))
		if err != nil || len(a.Answer)+len(a.Extra)+len(a.Ns) != 0 || a.AuthenticatedData {
			t.Fatal("hints escaped", a, err)
		}
	}
	if domestic.count()+selected.count() != 0 {
		t.Fatal("unnecessary DNS for suppressed hints")
	}
	selected.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		a := new(mdns.Msg).SetReply(q)
		a.AuthenticatedData = true
		for _, text := range []string{"google.com. 60 IN A 223.5.5.5", "google.com. 60 IN AAAA 2001:4860::8888", "google.com. 60 IN HTTPS 1 . ipv4hint=223.5.5.5", "google.com. 60 IN TXT \"ok\""} {
			rr, e := mdns.NewRR(text)
			if e != nil {
				t.Fatal(e)
			}
			a.Answer = append(a.Answer, rr)
			a.Ns = append(a.Ns, rr)
			a.Extra = append(a.Extra, rr)
		}
		return a, nil
	}
	for _, qtype := range []uint16{mdns.TypeA, mdns.TypeTXT} {
		a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("google.com.", qtype))
		if err != nil {
			t.Fatal(err)
		}
		if a.AuthenticatedData {
			t.Fatal("modified answer claimed DNSSEC")
		}
		if qtype == mdns.TypeA {
			if len(a.Answer) != 1 || len(a.Ns)+len(a.Extra) != 0 {
				t.Fatal("real addresses escaped fake answer")
			}
		} else {
			for _, section := range [][]mdns.RR{a.Answer, a.Ns, a.Extra} {
				for _, rr := range section {
					if rr.Header().Rrtype != mdns.TypeTXT {
						t.Fatal("address hint escaped TXT response")
					}
				}
			}
		}
	}
}

func TestRoutingDNSRejectsInvalidCNAMEAndResponseIdentity(t *testing.T) {
	for _, kind := range []string{"cycle", "ambiguous", "question", "id", "truncated", "ipv6-only"} {
		t.Run(kind, func(t *testing.T) {
			s, domestic, _, _, _, _ := routingSession(t, routingpolicy.BypassCN)
			domestic.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
				a := new(mdns.Msg).SetReply(q)
				texts := []string{"baidu.com. 60 IN A 8.8.8.8"}
				switch kind {
				case "cycle":
					texts = []string{"baidu.com. 60 IN CNAME loop.invalid.", "loop.invalid. 60 IN CNAME baidu.com."}
				case "ambiguous":
					texts = []string{"baidu.com. 60 IN CNAME a.invalid.", "baidu.com. 60 IN CNAME b.invalid.", "a.invalid. 60 IN A 8.8.8.8"}
				case "question":
					a.Question = append([]mdns.Question(nil), a.Question...)
					a.Question[0].Name = "attacker.invalid."
				case "id":
					a.Id++
				case "truncated":
					a.Truncated = true
				case "ipv6-only":
					texts = []string{"baidu.com. 60 IN AAAA 2001:4860::8888"}
				}
				for _, text := range texts {
					rr, e := mdns.NewRR(text)
					if e != nil {
						t.Fatal(e)
					}
					a.Answer = append(a.Answer, rr)
				}
				return a, nil
			}
			if _, err := s.resolver.resolve(context.Background(), "baidu.com"); err == nil {
				t.Fatal("invalid response authorized route")
			}
		})
	}
}

func TestRoutingMissingRulesAndDNSFailClosed(t *testing.T) {
	s, domestic, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	s.resolver.rules = nil
	if _, err := s.resolver.resolve(context.Background(), "baidu.com"); err == nil {
		t.Fatal("missing rules permitted domain route")
	}
	if _, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("baidu.com.", mdns.TypeA)); err == nil {
		t.Fatal("missing rules permitted DNS")
	}
	if domestic.count()+selected.count()+out.count() != 0 || protected.calls.Load() != 0 {
		t.Fatal("rule failure leaked network")
	}
}
