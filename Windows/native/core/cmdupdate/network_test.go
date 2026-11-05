package cmdupdate

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestControlledDialOnlyUsesWhitelistedHost443AndValidatedNumericAddresses(t *testing.T) {
	resolved, dialed := 0, 0
	resolve := func(_ context.Context, host string) ([]netip.Addr, error) {
		resolved++
		if host != "updates.example.test" {
			t.Fatal("unexpected resolver host")
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		dialed++
		if network != "tcp4" || address != "93.184.216.34:443" {
			t.Fatal("not numeric pinned HTTPS", network, address)
		}
		return nil, errors.New("fixture connection refusal")
	}
	controlled := controlDialer([]string{"updates.example.test"}, resolve, dial)
	for _, address := range []string{"evil.test:443", "updates.example.test:8443", "93.184.216.34:443"} {
		if _, err := controlled(context.Background(), "tcp", address); err == nil {
			t.Fatal("unapproved connection")
		}
	}
	if resolved != 0 || dialed != 0 {
		t.Fatal("unapproved request reached DNS/network")
	}
	_, _ = controlled(context.Background(), "tcp", "updates.example.test:443")
	if resolved != 1 || dialed != 1 {
		t.Fatal("approved path not used")
	}
}
func TestControlDNSRejectsPrivateReservedAndMissingServiceWithoutFallback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "198.18.0.1", "192.0.2.1", "224.0.0.1", "255.255.255.255", "::1"} {
		if publicIPv4(netip.MustParseAddr(ip)) {
			t.Fatal("nonpublic DNS result accepted", ip)
		}
	}
	dialed := false
	f := controlDialer([]string{"updates.example.test"}, nil, func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, nil })
	if _, err := f(context.Background(), "tcp", "updates.example.test:443"); err == nil || dialed {
		t.Fatal("service DNS fallback occurred")
	}
	if len(TrustedHosts()) != 0 {
		t.Fatal("unconfigured build exposed release hosts")
	}
}
