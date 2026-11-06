package mactun

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/tunengine"
	"github.com/miekg/dns"
)

type interruptedRoute struct{ *fakeBackend }

func (b interruptedRoute) AddRoute(ctx context.Context, r Route) error {
	if err := b.fakeBackend.AddRoute(ctx, r); err != nil {
		return err
	}
	return errors.New("crash after route mutation")
}

func TestRecoveryAfterUnacknowledgedRouteMutation(t *testing.T) {
	b := newFakeBackend()
	m, err := newManager(501, interruptedRoute{b})
	if err != nil {
		t.Fatal(err)
	}
	r := Route{Prefix: netip.MustParsePrefix("128.0.0.0/1"), Interface: adapterName}
	if err = m.addRoute(context.Background(), r); err == nil {
		t.Fatal("missing injected crash")
	}
	if b.j == nil || len(b.j.Routes) != 1 || len(b.routes) != 1 {
		t.Fatal("mutation missing durable checkpoint")
	}
	recovered, err := newManager(501, b)
	if err != nil {
		t.Fatal(err)
	}
	if err = recovered.restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.routes) != 0 || len(recovered.j.Routes) != 0 {
		t.Fatal("incomplete restore")
	}
}

func TestRecoveryAcceptsOnlyRecordedPFPendingRules(t *testing.T) {
	b := newFakeBackend()
	m, err := newManager(501, b)
	if err != nil {
		t.Fatal(err)
	}
	g := Guard{OwnerUID: 501, Interface: "en0"}
	if err = m.guard(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	g.Forwarding = true
	g.TunnelIndex = 233
	raw, err := renderGuard(g)
	if err != nil {
		t.Fatal(err)
	}
	m.j.PendingRules = canonicalRules(raw)
	if err = m.save(); err != nil {
		t.Fatal(err)
	}
	b.rules = m.j.PendingRules // crash after kernel commit but before final journal save
	recovered, err := newTunnel(501, b)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Forwarding() || strings.Contains(b.rules, "bluegrass-tunnel") {
		t.Fatal("recovery reopened forwarding")
	}
	b.rules += "pass out all\n"
	if _, err = newTunnel(501, b); !errors.Is(err, ErrExternalWriter) {
		t.Fatal(err)
	}
}

func TestDNSMaskRequiresOwnedOverrideAndOriginalSource(t *testing.T) {
	b := newFakeBackend()
	m, err := newManager(501, b)
	if err != nil {
		t.Fatal(err)
	}
	original := Resolver{Service: "Wi-Fi", Interface: "en0", Effective: []netip.Addr{netip.MustParseAddr("192.168.1.1")}}
	peer := []netip.Addr{netip.MustParseAddr("172.31.255.1")}
	if err = m.setDNS(context.Background(), original, peer); err != nil {
		t.Fatal(err)
	}
	overrides, err := m.dnsOverrides(context.Background())
	if err != nil || !sameAddresses(overrides["en0"], original.Effective) {
		t.Fatal(overrides, err)
	}
	b.fail = "verify-resolver"
	if _, err = m.dnsOverrides(context.Background()); err == nil {
		t.Fatal("masked changed underlying source")
	}
	b.fail = ""
	b.dns["Wi-Fi"] = []netip.Addr{netip.MustParseAddr("8.8.4.4")}
	if _, err = m.dnsOverrides(context.Background()); !errors.Is(err, ErrExternalWriter) {
		t.Fatal(err)
	}
}

func TestReleaseFailuresNeverDiscardRemainingRecovery(t *testing.T) {
	for _, step := range []string{"write-dns", "delete-route", "release-pf", "delete-journal"} {
		t.Run(step, func(t *testing.T) {
			tun, b, _, err := fixture()
			if err != nil {
				t.Fatal(err)
			}
			if err = tun.Prepare(context.Background(), profile()); err != nil {
				t.Fatal(err)
			}
			if err = tun.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			b.fail = step
			if err = tun.Release(); err == nil {
				t.Fatal("release acknowledged failed step")
			}
			if b.j == nil || !tun.Active() {
				t.Fatal("unrecovered journal discarded")
			}
			b.fail = ""
			if err = tun.Release(); err != nil {
				t.Fatalf("failed explicit retry: %v", err)
			}
			if b.j != nil || b.enabled || b.marker {
				t.Fatal("retry did not complete")
			}
		})
	}
}

func TestMalformedJournalDoesNotTouchOS(t *testing.T) {
	for _, mutate := range []func(*Journal){
		func(j *Journal) { j.ID = "../../elsewhere" },
		func(j *Journal) { j.Token = "123 -d" },
		func(j *Journal) {
			j.Routes = []Route{{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Interface: "en0", Gateway: netip.MustParseAddr("192.168.1.1")}}
		},
		func(j *Journal) {
			j.DNS = []DNSChange{{Before: Resolver{Service: "-setwebproxy", Interface: "en0"}, After: []netip.Addr{netip.MustParseAddr("172.31.255.1")}}}
		},
	} {
		b := newFakeBackend()
		m, err := newManager(501, b)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&m.j)
		b.j = cloneJournal(m.j)
		before := len(b.events)
		if _, err = newManager(501, b); err == nil {
			t.Fatal("accepted malformed journal")
		}
		if len(b.events) != before {
			t.Fatal("malformed journal mutated OS")
		}
	}
}

type dnsBackend struct {
	*fakeBackend
	answer        netip.Addr
	dialed        []netip.AddrPort
	maskSensitive bool
	lastOwned     []Route
}

func (b *dnsBackend) Network(ctx context.Context, original map[string][]netip.Addr, owned []Route) (tunengine.NetworkSnapshot, error) {
	n, err := b.fakeBackend.Network(ctx, original, owned)
	b.lastOwned = append([]Route(nil), owned...)
	if b.maskSensitive {
		for _, r := range b.routes {
			if r.Interface == adapterName {
				continue
			}
			masked := false
			for _, p := range owned {
				if r == p {
					masked = true
				}
			}
			if !masked {
				n.Generation++
			}
		}
	}
	return n, err
}

func (b *dnsBackend) Dial(_ context.Context, network string, e netip.AddrPort, u Underlay) (net.Conn, error) {
	b.dialed = append(b.dialed, e)
	if network != "tcp" || u.Interface != "en0" || u.Index != 4 {
		return nil, errors.New("binding missing")
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		conn := &dns.Conn{Conn: server}
		q, err := conn.ReadMsg()
		if err != nil {
			return
		}
		a := new(dns.Msg)
		a.SetReply(q)
		a.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IP(b.answer.AsSlice())}}
		_ = conn.WriteMsg(a)
	}()
	return client, nil
}
func TestControlResolverPinsIPv4AndKeepsRelayPermits(t *testing.T) {
	tun, b, f, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	dnsb := &dnsBackend{fakeBackend: b, answer: netip.MustParseAddr("9.9.9.9"), maskSensitive: true}
	tun.m.b = dnsb
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	relay := netip.MustParseAddrPort("8.8.4.4:55555")
	if err = f.config.AllowUDPRelay(relay); err != nil {
		t.Fatal(err)
	}
	ips, err := tun.ResolveControlHost(context.Background(), "client-api.sunip.vip")
	if err != nil || len(ips) != 1 || ips[0] != dnsb.answer {
		t.Fatal(ips, err)
	}
	if b.routes[netip.PrefixFrom(dnsb.answer, 32)].Interface != "en0" || !strings.Contains(b.rules, "port 443 user 501") || !strings.Contains(b.rules, "55555") {
		t.Fatal("lost scoped control or existing relay")
	}
	if len(dnsb.lastOwned) != 1 || dnsb.lastOwned[0].Prefix != netip.PrefixFrom(dnsb.answer, 32) {
		t.Fatal("API pin not masked before generation")
	}
	tun.policyMu.Lock()
	pin := b.routes[netip.PrefixFrom(dnsb.answer, 32)]
	tampered := pin
	tampered.Metric = 9
	b.routes[pin.Prefix] = tampered
	_, maskErr := tun.snapshotLocked(context.Background())
	b.routes[pin.Prefix] = pin
	tun.policyMu.Unlock()
	if !errors.Is(maskErr, ErrExternalWriter) {
		t.Fatal("tampered route masked", maskErr)
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = tun.ResolveControlHost(context.Background(), "client-api.sunip.vip"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.rules, "1080") || strings.Contains(b.rules, "55555") {
		t.Fatal("control DNS reopened paused node permits")
	}
}

func TestWatcherClosesForwardingBeforeHostNotification(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.fail = "network"
	b.mu.Unlock()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && (!tun.NetworkChanged() || tun.Forwarding()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !tun.NetworkChanged() || tun.Forwarding() {
		t.Fatal("network failure did not pause")
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.rules, "bluegrass-tunnel") {
		t.Fatal("PF still permits utun")
	}
}

func TestKernelStateLossRecoversOnlyBlocking(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	tun.current.Load().cancel()
	tun.current.Store(nil)
	b.rules = ""
	b.enabled = false
	recovered, err := newTunnel(501, b)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Active() || recovered.Forwarding() || !b.enabled || strings.Contains(b.rules, "bluegrass-tunnel") {
		t.Fatal("lost intent or resumed forwarding")
	}
}
func TestBoundResolverRejectsPrivateAnswerAndNeverUsesOSDNS(t *testing.T) {
	b := &dnsBackend{fakeBackend: newFakeBackend(), answer: netip.MustParseAddr("192.168.1.20")}
	tun, err := newTunnel(501, b)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := b.Underlay(context.Background())
	p := entrydns.Policy{Mode: "custom", Candidates: []entrydns.Candidate{{Address: "1.1.1.1", Transport: "tcp"}}}
	if _, err = tun.resolve(context.Background(), "example.com", p, nil, u); err == nil {
		t.Fatal("accepted private DNS reply")
	}
	if len(b.dialed) != 1 || b.dialed[0].String() != "1.1.1.1:53" {
		t.Fatal("unconfigured fallback", b.dialed)
	}
}
