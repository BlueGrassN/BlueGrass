package entrydns

import (
	"context"
	"errors"
	"github.com/miekg/dns"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestAutomaticFallbackAfterPhysicalDNSFailure(t *testing.T) {
	var calls []string
	ip, err := resolve(context.Background(), "entry.example", Policy{Mode: "auto"}, []netip.AddrPort{netip.MustParseAddrPort("192.168.1.1:53")}, func(ctx context.Context, c Candidate, q *dns.Msg) (*dns.Msg, error) {
		calls = append(calls, c.Address+"/"+c.Transport)
		if c.Address == "192.168.1.1" {
			return nil, errors.New("physical resolver unavailable")
		}
		answer := new(dns.Msg)
		answer.SetReply(q)
		answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("203.0.113.8")}}
		return answer, nil
	})
	if err != nil || ip.String() != "203.0.113.8" || len(calls) != 3 || calls[0] != "192.168.1.1/udp" || calls[1] != "192.168.1.1/tcp" || calls[2] != "1.1.1.1/udp" {
		t.Fatal("auto resolution did not fail over after physical DNS failed", calls, err)
	}
}

func TestCustomNeverUsesSystemCandidatesAndRespectsTransportOrder(t *testing.T) {
	policy := Policy{Mode: "custom", Candidates: []Candidate{{Address: "192.168.1.1", Transport: "udp"}, {Address: "1.1.1.1", Port: 5353, Transport: "tcp"}}}
	var called []Candidate
	ip, err := resolve(context.Background(), "entry.example", policy, []netip.AddrPort{netip.MustParseAddrPort("8.8.8.8:53")}, func(ctx context.Context, c Candidate, q *dns.Msg) (*dns.Msg, error) {
		called = append(called, c)
		if c.Transport == "udp" {
			return nil, errors.New("timed out")
		}
		reply := new(dns.Msg)
		reply.SetReply(q)
		reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("203.0.113.7")}}
		return reply, nil
	})
	if err != nil || ip.String() != "203.0.113.7" || len(called) != 2 || called[0].Endpoint().Port() != 53 || called[1].Endpoint().Port() != 5353 {
		t.Fatal("custom resolution order/ports failed", called, err)
	}
	for _, c := range called {
		if c.Address == "8.8.8.8" {
			t.Fatal("custom policy used unconfigured resolver")
		}
	}
}
func TestRejectUnrelatedAnswerAndNoListFallback(t *testing.T) {
	n := 0
	_, err := resolve(context.Background(), "entry.example", Policy{Mode: "custom", Candidates: []Candidate{{Address: "1.1.1.1", Transport: "udp"}}}, nil, func(ctx context.Context, c Candidate, q *dns.Msg) (*dns.Msg, error) {
		n++
		reply := new(dns.Msg)
		reply.SetReply(q)
		reply.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "unrelated.example.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("203.0.113.8")}}
		return reply, nil
	})
	if err == nil || n != 1 {
		t.Fatal("unrelated DNS response accepted or fallback escaped")
	}
}
func TestLiteralEntryNeedsNoDNSAndCancellationIsBounded(t *testing.T) {
	ip, err := Resolve(context.Background(), "203.0.113.5", Policy{}, nil)
	if err != nil || ip.String() != "203.0.113.5" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	start := time.Now()
	_, err = resolve(ctx, "entry.example", Policy{}, []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53")}, func(context.Context, Candidate, *dns.Msg) (*dns.Msg, error) { called = true; return nil, nil })
	if err == nil || called || time.Since(start) > time.Second {
		t.Fatal("cancelled resolver dialed or stalled")
	}
}
func TestPolicyRejectsImplicitOrUnenforceableServers(t *testing.T) {
	for _, policy := range []Policy{{Mode: "custom"}, {Mode: "bogus"}, {Mode: "auto", Candidates: []Candidate{{Address: "1.1.1.1", Transport: "udp"}}}, {Mode: "custom", Candidates: []Candidate{{Address: "dns.example", Transport: "udp"}}}, {Mode: "custom", Candidates: []Candidate{{Address: "fe80::1", Transport: "udp"}}}, {Mode: "custom", Candidates: []Candidate{{Address: "1.1.1.1", Transport: "https"}}}} {
		if policy.Validate() == nil {
			t.Fatal("unsafe policy accepted")
		}
	}
}
