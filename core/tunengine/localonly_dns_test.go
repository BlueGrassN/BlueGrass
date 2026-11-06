package tunengine

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

type localOnlyDNSFixture struct {
	dnsTransport
	calls    atomic.Int32
	exchange func(*mdns.Msg) *mdns.Msg
}

func (*localOnlyDNSFixture) Close() error { return nil }
func (*localOnlyDNSFixture) Start() error { return nil }
func (*localOnlyDNSFixture) Reset()       {}

type localOnlyBlockingDNS struct {
	localOnlyDNSFixture
	entered  chan struct{}
	returned chan struct{}
}

func (f *localOnlyBlockingDNS) Exchange(ctx context.Context, _ *mdns.Msg) (*mdns.Msg, error) {
	close(f.entered)
	<-ctx.Done()
	close(f.returned)
	return nil, ctx.Err()
}

func TestLocalOnlyDNSCloseCancelsAndJoinsInflightQuery(t *testing.T) {
	p, _ := prepareLocalOnly(localOnlyFixture())
	f := &localOnlyBlockingDNS{entered: make(chan struct{}), returned: make(chan struct{})}
	d := newLocalOnlyDNS(p, func(context.Context) bool { return true }, []dnsTransport{f}, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.Exchange(context.Background(), localOnlyQuery("nas.lan.", mdns.TypeA))
	}()
	<-f.entered
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.returned:
	default:
		t.Fatal("Close returned before upstream stopped")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inflight exchange leaked")
	}
	r, err := d.Exchange(context.Background(), localOnlyQuery("nas.lan.", mdns.TypeA))
	if err != nil || r.Rcode != mdns.RcodeServerFailure {
		t.Fatal("closed classifier reused", r, err)
	}
}

func (f *localOnlyDNSFixture) Exchange(_ context.Context, m *mdns.Msg) (*mdns.Msg, error) {
	f.calls.Add(1)
	if f.exchange != nil {
		return f.exchange(m), nil
	}
	r := new(mdns.Msg)
	r.SetReply(m)
	return r, nil
}
func localOnlyQuery(name string, typ uint16) *mdns.Msg { return new(mdns.Msg).SetQuestion(name, typ) }

func TestLocalOnlyPublicQueriesNeverReachAnyUpstream(t *testing.T) {
	p, err := prepareLocalOnly(localOnlyFixture())
	if err != nil {
		t.Fatal(err)
	}
	f := &localOnlyDNSFixture{}
	d := newLocalOnlyDNS(p, func(context.Context) bool { return true }, []dnsTransport{f}, f)
	for _, name := range []string{"example.com.", "nas.lan.example.com.", "evilhome.arpa.", "8.8.8.8.in-addr.arpa.", "1.0.18.198.in-addr.arpa."} {
		for _, typ := range []uint16{mdns.TypeA, mdns.TypeAAAA, mdns.TypeTXT, mdns.TypeSRV, mdns.TypeHTTPS, mdns.TypePTR} {
			r, err := d.Exchange(context.Background(), localOnlyQuery(name, typ))
			if err != nil || r.Rcode == mdns.RcodeSuccess {
				t.Fatalf("public query succeeded: %s %d: %v", name, typ, err)
			}
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("public query escaped classification")
	}
}

func TestLocalOnlyLANOffAndUnavailableResolverFailClosed(t *testing.T) {
	c := localOnlyFixture()
	c.AllowLAN = false
	p, err := prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	f := &localOnlyDNSFixture{}
	d := newLocalOnlyDNS(p, func(context.Context) bool { return true }, []dnsTransport{f}, f)
	for _, name := range []string{"nas.lan.", "router.home.arpa.", "printer.local."} {
		r, err := d.Exchange(context.Background(), localOnlyQuery(name, mdns.TypeA))
		if err != nil || r.Rcode != mdns.RcodeServerFailure {
			t.Fatal(name, r, err)
		}
	}
	r, err := d.Exchange(context.Background(), localOnlyQuery("LOCALHOST.", mdns.TypeA))
	if err != nil || len(r.Answer) != 1 || r.Answer[0].(*mdns.A).A.String() != "127.0.0.1" {
		t.Fatal("localhost lost", r, err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("LAN disabled query reached upstream")
	}
	c = localOnlyFixture()
	c.Network.DNS = nil
	p, err = prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	d = newLocalOnlyDNS(p, func(context.Context) bool { return true }, nil, nil)
	r, err = d.Exchange(context.Background(), localOnlyQuery("nas.lan.", mdns.TypeTXT))
	if err != nil || r.Rcode != mdns.RcodeServerFailure {
		t.Fatal("missing resolver fell back", r, err)
	}
}

func TestLocalOnlyStaleResponseAndMalformedRequestDoNotEscape(t *testing.T) {
	p, _ := prepareLocalOnly(localOnlyFixture())
	current := true
	f := &localOnlyDNSFixture{exchange: func(m *mdns.Msg) *mdns.Msg { current = false; r := new(mdns.Msg); r.SetReply(m); return r }}
	d := newLocalOnlyDNS(p, func(context.Context) bool { return current }, []dnsTransport{f}, nil)
	r, _ := d.Exchange(context.Background(), localOnlyQuery("NAS.LAN.", mdns.TypeTXT))
	if r.Rcode != mdns.RcodeServerFailure {
		t.Fatal("stale result accepted")
	}
	current = true
	m := localOnlyQuery("nas.lan.", mdns.TypeA)
	m.Question = append(m.Question, mdns.Question{Name: "example.com.", Qtype: mdns.TypeA, Qclass: mdns.ClassINET})
	r, _ = d.Exchange(context.Background(), m)
	if r.Rcode != mdns.RcodeFormatError || f.calls.Load() != 1 {
		t.Fatal("multi-question reached upstream")
	}
}

func TestLocalOnlyMDNSAndAliasFollowupRemainSeparatelyClassified(t *testing.T) {
	c := localOnlyFixture()
	c.Network.MDNSInterface = "Ethernet"
	p, err := prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	local := &localOnlyDNSFixture{exchange: func(m *mdns.Msg) *mdns.Msg {
		r := new(mdns.Msg)
		r.SetReply(m)
		r.Answer = []mdns.RR{&mdns.CNAME{Hdr: mdns.RR_Header{Name: m.Question[0].Name, Rrtype: mdns.TypeCNAME, Class: mdns.ClassINET}, Target: "outside.example.com."}}
		return r
	}}
	multicast := &localOnlyDNSFixture{}
	d := newLocalOnlyDNS(p, func(context.Context) bool { return true }, []dnsTransport{local}, multicast)
	for _, name := range []string{"printer.local.", "1.0.254.169.in-addr.arpa."} {
		if _, err := d.Exchange(context.Background(), localOnlyQuery(name, mdns.TypePTR)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := d.Exchange(context.Background(), localOnlyQuery("nas.lan.", mdns.TypeA))
	if err != nil || len(r.Answer) != 1 {
		t.Fatal("alias response lost", r, err)
	}
	r, err = d.Exchange(context.Background(), localOnlyQuery("outside.example.com.", mdns.TypeA))
	if err != nil || r.Rcode != mdns.RcodeRefused || local.calls.Load() != 1 || multicast.calls.Load() != 2 {
		t.Fatal("alias/mDNS crossed classifier", r, err)
	}
}

// Implements the pinned sing dialer using a test-only exact loopback fixture.
type localOnlyTestDialer struct{ address string }

func (d localOnlyTestDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.address)
}
func (d localOnlyTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	panic("TCP only")
}

func TestLocalOnlyRealTCPResolverPreservesNXDOMAINAndRecordTypes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	server := &mdns.Server{Listener: listener, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, m *mdns.Msg) {
		count.Add(1)
		r := new(mdns.Msg)
		r.SetRcode(m, mdns.RcodeNameError)
		if m.Question[0].Name == "printer.lan." && m.Question[0].Qtype == mdns.TypeA {
			r.SetReply(m)
			r.Rcode = mdns.RcodeSuccess
			r.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: "printer.lan.", Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 42}, A: net.IPv4(192, 168, 40, 12)}}
		}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = server.ActivateAndServe() }()
	defer server.Shutdown()
	tcp := newLocalOnlyTCP(context.Background(), localOnlyTestDialer{listener.Addr().String()}, M.ParseSocksaddr(listener.Addr().String()))
	defer tcp.Close()
	p, _ := prepareLocalOnly(localOnlyFixture())
	d := newLocalOnlyDNS(p, func(context.Context) bool { return true }, []dnsTransport{tcp}, nil)
	for _, typ := range []uint16{mdns.TypeA, mdns.TypeAAAA, mdns.TypeSRV, mdns.TypeTXT} {
		r, err := d.Exchange(context.Background(), localOnlyQuery("NAS.LAN.", typ))
		if err != nil || r.Rcode != mdns.RcodeNameError {
			t.Fatal("NXDOMAIN lost", r, err)
		}
	}
	r, err := d.Exchange(context.Background(), localOnlyQuery("printer.lan.", mdns.TypeA))
	if err != nil || r.Rcode != mdns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*mdns.A).A.String() != "192.168.40.12" || r.Answer[0].Header().Ttl != 42 {
		t.Fatal("real local answer/TTL changed", r, err)
	}
	_, _ = d.Exchange(context.Background(), localOnlyQuery("example.com.", mdns.TypeA))
	if count.Load() != 5 {
		t.Fatal("public request reached loopback upstream", count.Load())
	}
}
