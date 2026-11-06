package tunengine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type probeDNSFixture struct {
	hosts     []string
	addresses []netip.Addr
	err       error
}

func (f *probeDNSFixture) Lookup(_ context.Context, host string) ([]netip.Addr, error) {
	f.hosts = append(f.hosts, host)
	return f.addresses, f.err
}

type probeOutboundFixture struct {
	N.Dialer
	dial func(context.Context, string, M.Socksaddr) (net.Conn, error)
}

func (f probeOutboundFixture) DialContext(ctx context.Context, network string, address M.Socksaddr) (net.Conn, error) {
	return f.dial(ctx, network, address)
}

func TestProbeAllowlistBeforeProxyDNSAndOnlySelectedPublicIPv4(t *testing.T) {
	resolver := &probeDNSFixture{addresses: []netip.Addr{
		netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.168.1.1"), netip.MustParseAddr("198.18.0.2"),
		netip.MustParseAddr("2606:4700::1111"), netip.MustParseAddr("::ffff:8.8.8.8"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("9.9.9.9"),
	}}
	var targets []string
	out := probeOutboundFixture{dial: func(_ context.Context, network string, address M.Socksaddr) (net.Conn, error) {
		targets = append(targets, network+" "+address.String())
		if address.String() == "8.8.8.8:443" {
			return nil, errors.New("fixture first IPv4 unreachable")
		}
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		return client, nil
	}}
	dial := probeDialer(resolver.Lookup, out)
	for _, target := range []string{"evil.example:443", "api.ipify.org:80", "api.ipify.org.evil:443", "8.8.8.8:443"} {
		if _, err := dial(context.Background(), "tcp", target); err == nil {
			t.Fatal("untrusted target reached probe dial")
		}
	}
	if _, err := dial(context.Background(), "udp", "api.ipify.org:443"); err == nil || len(resolver.hosts) != 0 || len(targets) != 0 {
		t.Fatal("invalid target reached DNS or outbound")
	}
	for _, host := range []string{"api.ipify.org", "icanhazip.com", "checkip.amazonaws.com"} {
		targets = nil
		conn, err := dial(context.Background(), "tcp", host+":443")
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
		if strings.Join(targets, ",") != "tcp 8.8.8.8:443,tcp 9.9.9.9:443" {
			t.Fatal("DNS response escaped selected public IPv4 path", targets)
		}
		if resolver.hosts[len(resolver.hosts)-1] != host {
			t.Fatal("wrong probe DNS name")
		}
	}
	targets = nil
	resolver.err = errors.New("fixture DNS failure")
	if _, err := dial(context.Background(), "tcp", "api.ipify.org:443"); err == nil || len(targets) != 0 {
		t.Fatal("DNS failure fell back to direct/remote-domain dialing")
	}
	resolver.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dial(ctx, "tcp", "api.ipify.org:443"); err == nil || len(targets) != 0 {
		t.Fatal("canceled probe reached outbound")
	}
}

func TestPinnedRuntimeUsesSharedEndpointsThroughPublicDNS(t *testing.T) {
	c := testConfig()
	n, _ := c.Network(context.Background())
	p, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := newRuntime(context.Background(), p, newSessionGate())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	sing := r.(*xrayRuntime)
	resolver := &probeDNSFixture{err: errors.New("fixture offline DNS")}
	sing.probeLookup = resolver.Lookup // the Box is constructed, never started
	if address, err := r.Probe(context.Background()); err == nil || address != "" {
		t.Fatal("failed lookup became exit evidence")
	}
	if strings.Join(resolver.hosts, ",") != "api.ipify.org,icanhazip.com,checkip.amazonaws.com" {
		t.Fatal("runtime did not use shared fallback", resolver.hosts)
	}
	if r.InterfaceLUID() != 0 {
		t.Fatal("probe created native interface")
	}
}
