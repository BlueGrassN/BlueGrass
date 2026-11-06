package mactun

import (
	"net/netip"
	"strings"
	"testing"
)

func TestPFHookSkipAndTokenParsing(t *testing.T) {
	if !supportedRootNAT("nat-anchor \"com.apple/*\" all\nrdr-anchor \"com.apple/*\" all\n") || supportedRootNAT("nat on en0 from any to any -> (en0)\n") {
		t.Fatal("unverified NAT bypass admitted")
	}
	if !supportedRootRules("anchor \"com.apple/*\" all\n") {
		t.Fatal("stock hook")
	}
	for _, s := range []string{"", "pass all\nanchor \"com.apple/*\" all\n", "anchor \"com.apple\" all\n", "anchor \"com.apple/*\" out all\n"} {
		if supportedRootRules(s) {
			t.Fatal(s)
		}
	}
	if safePFInterfaces("en0 (skip)\n") || safePFInterfaces("en0\n\tFlags: skip\n") {
		t.Fatal("physical skip admitted")
	}
	if !safePFInterfaces("lo0 (skip)\nen0\n") {
		t.Fatal("loopback skip rejected")
	}
	token, err := parsePFToken("No ALTQ support in kernel\nPF enabled\nToken : 12345\n")
	if err != nil || token != "12345" {
		t.Fatalf("%s %v", token, err)
	}
	for _, raw := range []string{"", "Token : abc", "Token : 123\nToken : 456", "Token : 123; -d"} {
		if _, err = parsePFToken(raw); err == nil {
			t.Fatal(raw)
		}
	}
}
func TestResolverParsersPreserveAutomaticAndDisabledServices(t *testing.T) {
	a, err := parseDNSOutput("There aren't any DNS Servers set on Wi-Fi.\n")
	if err != nil || len(a) != 0 {
		t.Fatal(a, err)
	}
	a, err = parseDNSOutput("192.168.1.1\n2001:4860:4860::8888\n")
	if err != nil || len(a) != 2 {
		t.Fatal(a, err)
	}
	if _, err = parseDNSOutput("Error: no such service"); err == nil {
		t.Fatal("error interpreted as automatic")
	}
	list, err := parseServiceOrder("An asterisk (*) denotes disabled.\n(1) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en0)\n(*) Disabled Ethernet\n(Hardware Port: Ethernet, Device: en1)\n(3) VPN\n(Hardware Port: VPN, Device: utun5)\n")
	if err != nil || len(list) != 1 || list[0].Service != "Wi-Fi" {
		t.Fatal(list, err)
	}
}
func TestPublicIPv6NeverGetsBroadLANPermit(t *testing.T) {
	g := Guard{OwnerUID: 501, Interface: "en0"}
	rules, err := renderGuard(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "block drop out quick all") || strings.Contains(rules, "pass out quick on en0 inet6") {
		t.Fatal(rules)
	}
	for _, endpoint := range []string{"[2606:4700:4700::1111]:443", "127.0.0.1:443", "198.18.0.1:443", "0.0.0.0:53"} {
		if validEndpoint(netip.MustParseAddrPort(endpoint)) {
			t.Fatal(endpoint)
		}
	}
}

func TestMDNSIsAnExplicitLANScope(t *testing.T) {
	g := Guard{OwnerUID: 501, Interface: "en0", MDNSInterface: "en0"}
	rules, err := renderGuard(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "to 224.0.0.251 port 5353") || !strings.Contains(rules, "to ff02::fb port 5353") {
		t.Fatal("missing scoped multicast")
	}
	g.MDNSInterface = "en1"
	if _, err = renderGuard(g); err == nil {
		t.Fatal("admitted cross-link discovery")
	}
	g.MDNSInterface = ""
	rules, err = renderGuard(g)
	if err != nil || strings.Contains(rules, "bluegrass-mdns") {
		t.Fatal("disabled LAN retained discovery")
	}
}
