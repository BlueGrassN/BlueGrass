package tunengine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLocalOnlyConfigHasRealDNSIngressAndNoBusinessResolvers(t *testing.T) {
	p, err := prepareLocalOnly(localOnlyFixture())
	if err != nil {
		t.Fatal(err)
	}
	g := newSessionGate()
	o, err := decodeLocalOnlyOptions(p, g, func(context.Context) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if options := nativeOptions(p.tun4, p.tun6); options.Name != InterfaceName || len(options.DNS) == 0 {
		t.Fatal("no Xray TUN DNS ingress")
	}
	for _, server := range o.DNS.Servers {
		if server.Type != "bluegrass-local-only" {
			t.Fatal("unexpected DNS transport", server.Type)
		}
	}
	for _, out := range o.Outbounds {
		if out.Type != "direct" {
			t.Fatal("selected outbound exists", out.Type)
		}
	}
	for _, tc := range []struct {
		ip   string
		port uint16
		want string
	}{
		{"192.168.40.1", 53, "hijack-dns"}, {"8.8.8.8", 53, "hijack-dns"}, {"8.8.8.8", 443, "reject"},
		{"192.168.40.12", 443, "local-lan-0"}, {"198.18.0.10", 443, "reject"},
	} {
		if got := routeAction(t, o, tc.ip, tc.port); got != tc.want {
			t.Fatalf("%s:%d got %s", tc.ip, tc.port, got)
		}
	}
	raw, _ := json.Marshal(localOnlyConfigObject(p))
	for _, bad := range []string{"\"selected\"", "\"fakeip\"", "\"public\"", "1.1.1.1"} {
		if strings.Contains(string(raw), bad) {
			t.Fatal("business config present", bad)
		}
	}
	r, err := newLocalOnlyRuntime(context.Background(), p, g, func(context.Context) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Construct and close only: never starts native TUN, routes or WFP.
	if r.InterfaceLUID() != 0 {
		t.Fatal("constructor opened native interface")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOnlyConfigRejectsUntrustedMappingsAndDetachesSlices(t *testing.T) {
	for _, name := range []string{"", ".", "*", "com", "local", "localhost", "lan..", "a/b.lan"} {
		c := localOnlyFixture()
		c.Network.DNS[0].Domains = []string{name}
		if _, err := prepareLocalOnly(c); err == nil {
			t.Fatal("accepted broad/invalid domain", name)
		}
	}
	c := localOnlyFixture()
	p, err := prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Network.DNS[0].Domains[0] = "example.com"
	if p.network.DNS[0].Domains[0] != "lan" {
		t.Fatal("caller mutated classifier")
	}
	c = localOnlyFixture()
	c.AllowLAN = false
	p, err = prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	o, err := decodeLocalOnlyOptions(p, newSessionGate(), func(context.Context) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if routeAction(t, o, "192.168.40.12", 443) != "reject" {
		t.Fatal("LAN off permits direct")
	}
}
