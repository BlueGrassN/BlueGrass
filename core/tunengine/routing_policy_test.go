package tunengine

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"bluegrass/native/core/routingpolicy"
	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestSplitDNSMetadataUsesDomesticResolverWithoutAddressHints(t *testing.T) {
	for _, mode := range []string{"global", "bypass_cn"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig()
			cfg.AllowDirect = func(string, netip.AddrPort) error { return nil }
			cfg.ReleaseDirect = cfg.AllowDirect
			profile := testProfile()
			profile.RoutingMode = mode
			network, _ := cfg.Network(context.Background())
			prepared, err := prepareConfig(profile, cfg, network, netip.MustParseAddr("8.8.8.8"))
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := newRuntime(context.Background(), prepared, newSessionGate())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			d := runtime.(*xrayRuntime).dns
			queried := ""
			for _, tag := range []string{"domestic", "public"} {
				d.transports[tag] = &localOnlyDNSFixture{exchange: func(q *dns.Msg) *dns.Msg { queried = tag; return new(dns.Msg).SetReply(q) }}
			}
			for _, tc := range []struct {
				host  string
				kind  uint16
				split string
			}{
				{"www.baidu.com", dns.TypeTXT, "domestic"}, {"www.google.com", dns.TypeTXT, "public"},
				{"unlisted.routing-test.invalid", dns.TypeMX, "public"},
				{"www.baidu.com", dns.TypeHTTPS, ""}, {"www.google.com", dns.TypeHTTPS, ""},
				{"www.baidu.com", dns.TypeSVCB, ""}, {"www.baidu.com", dns.TypeANY, ""},
			} {
				queried = ""
				r, err := d.Exchange(context.Background(), new(dns.Msg).SetQuestion(dns.Fqdn(tc.host), tc.kind))
				want := tc.split
				if mode == "global" {
					want = "public"
				}
				if err != nil || r.Rcode != dns.RcodeSuccess || queried != want {
					t.Fatalf("mode=%s type=%d routed=%q want=%q err=%v", mode, tc.kind, queried, want, err)
				}
			}
		})
	}
}

func TestFakeIPSplitRestoresDomainAndSelectsExactTransport(t *testing.T) {
	rules, err := routingpolicy.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, answer string
		direct       bool
	}{
		{"www.baidu.com", "8.8.8.8", true},
		{"www.google.com", "223.5.5.5", false},
		{"unlisted.routing-test.invalid", "223.5.5.5", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			network, _ := cfg.Network(context.Background())
			p, err := prepareConfig(testProfile(), cfg, network, netip.MustParseAddr("8.8.8.8"))
			if err != nil {
				t.Fatal(err)
			}
			g := newSessionGate()
			g.open()
			policy, err := decodeOptions(p, g)
			if err != nil {
				t.Fatal(err)
			}
			fake := newFakeIPTransport(context.Background(), p)
			defer fake.Close()
			if err := fake.Start(); err != nil {
				t.Fatal(err)
			}
			ip, err := fake.store.Create(tc.name, false)
			if err != nil {
				t.Fatal(err)
			}
			queried := ""
			transport := func(tag string) dnsTransport {
				return &localOnlyDNSFixture{exchange: func(q *dns.Msg) *dns.Msg {
					queried = tag
					r := new(dns.Msg).SetReply(q)
					r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP(tc.answer)}}
					return r
				}}
			}
			selected, physical := &stackEchoDialer{}, &directFixture{}
			r := &nativeRouter{ctx: context.Background(), gate: g, config: policy, fake: fake.store, routingMode: routingpolicy.BypassCN, rules: rules, selected: selected, directs: map[string]N.Dialer{"domestic": physical}, dns: &policyDNS{config: policy, transports: map[string]dnsTransport{"domestic": transport("domestic"), "public": transport("public")}}}
			d, m, err := r.dialer(context.Background(), inboundContext{Network: "tcp", Destination: M.SocksaddrFrom(ip, 443)})
			if err != nil || m.Destination.Fqdn != "" || m.Destination.Addr.String() != tc.answer {
				t.Fatal("fake name not resolved exactly", err)
			}
			if (d == physical) != tc.direct {
				t.Fatal("wrong business path")
			}
			wantDNS := "public"
			if rules.Domain(tc.name) == routingpolicy.Direct {
				wantDNS = "domestic"
			}
			if queried != wantDNS {
				t.Fatal("wrong DNS path")
			}
		})
	}
}
