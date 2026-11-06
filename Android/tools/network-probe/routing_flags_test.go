package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingFlagIsOptIn(t *testing.T) {
	for _, args := range [][]string{
		{"--dns-server", "10.0.2.3:53", "--routing-check"},
		{"--routing-check=false", "--dns-server", "10.0.2.3:53"},
	} {
		if _, valid := parseArgs(args); !valid {
			t.Fatalf("routing option rejected: %v", args)
		}
	}
	body, err := json.Marshal(initialReport())
	if err != nil || strings.Contains(string(body), `"routing"`) {
		t.Fatalf("legacy report gained optional fields: %s, %v", body, err)
	}
}

func TestRoutingRejectsInvalidFlagsWithoutEcho(t *testing.T) {
	for _, extra := range [][]string{
		{"--routing-check=private-input"},
		{"--routing-check", "--insecure"},
		{"--routing-ip", "4.ipw.cn=8.8.4.4"},
		{"--routing-check=false", "--routing-ip", "4.ipw.cn=8.8.4.4"},
		{"--routing-check", "--routing-ip", "4.ipw.cn=198.18.0.1"},
		{"--routing-check", "--routing-ip", "4.ipw.cn=10.1.2.3"},
		{"--routing-check", "--routing-ip", "4.ipw.cn=::ffff:8.8.4.4"},
		{"--routing-check", "--routing-ip", "private-input.example=8.8.4.4"},
		{"--routing-check", "--routing-ip", "4.ipw.cn=8.8.4.4", "--routing-ip", "4.ipw.cn=1.1.1.1"},
	} {
		r, code := run(append([]string{"--dns-server", "10.0.2.3:53"}, extra...))
		body, err := json.Marshal(r)
		if code != 2 || r.Error != "invalid_arguments" || err != nil || strings.Contains(string(body), "private-input") {
			t.Fatalf("invalid flags not sanitized: %s (exit %d)", body, code)
		}
	}
}

func TestRoutingAcceptsOnlyBoundedNamedPhysicalOverrides(t *testing.T) {
	opts, valid := parseArgs([]string{"--routing-check", "--dns-server", "172.19.0.2:53",
		"--routing-ip", "myip.ipip.net=117.149.203.79",
		"--routing-ip", "stun.miwifi.com=111.206.174.2",
		"--routing-ip", "stun.qq.com=1.1.1.1"})
	if !valid || !opts.RoutingCheck || opts.DNSServer != "172.19.0.2:53" || len(opts.RoutingIPs) != 3 || opts.RoutingIPs["myip.ipip.net"].String() != "117.149.203.79" {
		t.Fatalf("valid overrides lost: %+v, %v", opts, valid)
	}
}
