package bridge

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type probeResolver struct {
	called bool
	fail   bool
}

func (r *probeResolver) LookupPublic(_ context.Context, domain string) ([]netip.Addr, error) {
	r.called = true
	if domain != "api.ipify.org" {
		return nil, errors.New("wrong resolver contract")
	}
	if r.fail {
		return nil, errors.New("FAKE_SECRET")
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("2606:4700::1111"), netip.MustParseAddr("8.8.8.8")}, nil
}

type probeOutbound struct {
	called bool
	dest   string
}

func (d *probeOutbound) DialContext(_ context.Context, network string, dest string) (net.Conn, error) {
	d.called = true
	d.dest = dest
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func TestExitProbeResolvesThroughSelectedDNSAndDialsOnlyPublicIPv4(t *testing.T) {
	resolver := &probeResolver{}
	out := &probeOutbound{}
	dial := probeDialer(resolver, out)
	conn, err := dial(context.Background(), "tcp", "api.ipify.org:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if !resolver.called || !out.called || out.dest != "8.8.8.8:443" {
		t.Fatal("probe path bypassed IPv4 policy")
	}
}
func TestExitProbeNeverFallsBackAfterDNSFailure(t *testing.T) {
	resolver := &probeResolver{fail: true}
	out := &probeOutbound{}
	_, err := probeDialer(resolver, out)(context.Background(), "tcp", "api.ipify.org:443")
	if err == nil || out.called {
		t.Fatal("failed DNS bypassed proxy")
	}
}
func TestExitProbeRejectsArbitraryDestinationBeforeDNS(t *testing.T) {
	resolver := &probeResolver{}
	out := &probeOutbound{}
	_, err := probeDialer(resolver, out)(context.Background(), "tcp", "attacker.example:443")
	if err == nil || resolver.called || out.called {
		t.Fatal("unexpected probe target accepted")
	}
}
