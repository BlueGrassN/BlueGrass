package bridge

import (
	"context"
	"errors"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestDNSPolicyRequiresConnectedLocalResolver(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		addresses, routes, servers []string
		want                       string
	}{
		{"connected", []string{"192.168.8.5/24"}, nil, []string{"192.168.8.1"}, "192.168.8.1"},
		{"skip-public-first", []string{"192.168.8.5/24"}, nil, []string{"1.1.1.1", "192.168.8.1"}, "192.168.8.1"},
		{"public-only", []string{"192.168.8.5/24"}, nil, []string{"1.1.1.1"}, ""},
		{"off-link", []string{"192.168.8.5/24"}, nil, []string{"192.168.9.1"}, ""},
		{"default-is-not-local", []string{"192.168.8.5/24"}, []string{"0.0.0.0/0"}, []string{"10.8.0.1"}, ""},
		{"route-alone-not-connected", []string{"192.168.8.5/24"}, []string{"10.0.0.0/8"}, []string{"10.8.0.1"}, ""},
		{"no-address", nil, []string{"192.168.8.0/24"}, []string{"192.168.8.1"}, ""},
		{"hostname", []string{"192.168.8.5/24"}, nil, []string{"dns.lan"}, ""},
		{"loopback", []string{"127.0.0.2/8"}, nil, []string{"127.0.0.1"}, ""},
		{"vpn", []string{"172.19.0.1/30"}, nil, []string{"172.19.0.2"}, ""},
		{"fake-ip", []string{"198.18.0.1/15"}, nil, []string{"198.18.0.2"}, ""},
		{"unspecified", []string{"0.0.0.0/0"}, nil, []string{"192.168.8.1"}, ""},
		{"broadcast", []string{"192.168.8.5/24"}, nil, []string{"192.168.8.255"}, ""},
		{"ula", []string{"fd12:3456::2/64"}, nil, []string{"fd12:3456::1"}, "fd12:3456::1"},
		{"link-local-zone", []string{"fe80::2/64"}, nil, []string{"fe80::1"}, "fe80::1%wlan0"},
		{"wrong-zone", []string{"fe80::2/64"}, nil, []string{"fe80::1%other"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := networkInfo{Name: "wlan0", Index: 7, Addresses: tt.addresses, Routes: tt.routes, DNSServers: tt.servers}
			if got := dnsConnectedResolver(info); got != tt.want {
				t.Fatalf("resolver=%q want=%q", got, tt.want)
			}
		})
	}
}

// Exercise the production router and assert exact transport call counts.
func TestDNSPolicyScopeAndNoFallback(t *testing.T) {
	for _, connected := range []bool{false, true} {
		info := fixtureNetwork
		info.Domains = []string{"com", "example.com", ".", "*", "LOCAL", "corp.invalid"}
		if !connected {
			info.Addresses = nil
		}
		router, transports := dnsPolicyTestRouter(t, info)
		for _, tt := range []struct {
			name   string
			qtype  uint16
			target string
			rcode  int
		}{
			{"example.com.", mdns.TypeA, "public", -1},
			{"host.corp.invalid.", mdns.TypeA, "public", -1},
			{"nas.lan.example.com.", mdns.TypeA, "public", -1},
			{"evilhome.arpa.", mdns.TypeA, "public", -1},
			{"notlocal.", mdns.TypeA, "", mdns.RcodeServerFailure},
			{"localhost.", mdns.TypeA, "", mdns.RcodeSuccess},
			{"a.LOCALHOST.", mdns.TypeAAAA, "", mdns.RcodeSuccess},
			{"example.com.", mdns.TypeAAAA, "", mdns.RcodeSuccess},
			{"NAS.LAN.", mdns.TypeA, "local-dns", -1},
			{"lan.", mdns.TypeTXT, "local-dns", -1},
			{"home.arpa.", mdns.TypeAAAA, "local-dns", -1},
			{"router.home.arpa.", mdns.TypeSRV, "local-dns", -1},
			{"printer.local.", mdns.TypeA, "mdns", -1},
			{"PRINTER.LOCAL.", mdns.TypeAAAA, "mdns", -1},
		} {
			for _, tr := range transports {
				tr.calls = 0
			}
			wantTarget, wantCode := tt.target, tt.rcode
			if !connected && (wantTarget == "local-dns" || wantTarget == "mdns") {
				wantTarget, wantCode = "", mdns.RcodeServerFailure
			}
			answer, err := router.Exchange(context.Background(), new(mdns.Msg).SetQuestion(tt.name, tt.qtype))
			if wantTarget != "" {
				if err == nil {
					t.Fatalf("%s: failing transport unexpectedly succeeded", tt.name)
				}
			} else if err != nil || answer.Rcode != wantCode || len(answer.Answer) != 0 {
				t.Fatalf("%s connected=%v: response=%v err=%v", tt.name, connected, answer, err)
			}
			for tag, tr := range transports {
				wantCalls := 0
				if tag == wantTarget {
					wantCalls = 1
				}
				if tr.calls != wantCalls {
					t.Fatalf("%s connected=%v: %s called %d times, want %d (fallback/leak)", tt.name, connected, tag, tr.calls, wantCalls)
				}
			}
		}
	}
}

type dnsPolicyFailureTransport struct{ calls int }

func (s *dnsPolicyFailureTransport) Exchange(context.Context, *mdns.Msg) (*mdns.Msg, error) {
	s.calls++
	return nil, errors.New("fixture DNS unavailable")
}
func dnsPolicyTestRouter(t *testing.T, info networkInfo) (*dnsRouter, map[string]*dnsPolicyFailureTransport) {
	t.Helper()
	r, err := newDNSRouter(context.Background(), &platformBridge{callback: &fakePlatform{}, info: info}, &probeOutbound{}, "global")
	if err != nil {
		t.Fatal(err)
	}
	transports := map[string]*dnsPolicyFailureTransport{"public": {}, "local-dns": {}, "mdns": {}}
	r.public = transports["public"]
	if r.local != nil {
		r.local = transports["local-dns"]
	}
	r.multicast = transports["mdns"]
	t.Cleanup(func() { r.Close() })
	return r, transports
}
