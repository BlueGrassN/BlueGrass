package bridge

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

type mdnsPlatformFixture struct {
	valid     atomic.Bool
	protected atomic.Int32
	fail      bool
}

func (p *mdnsPlatformFixture) IsValid() bool { return p.valid.Load() }
func (p *mdnsPlatformFixture) Protect(fd int32) error {
	if fd != 42 {
		return errors.New("wrong descriptor")
	}
	p.protected.Add(1)
	if p.fail {
		return errors.New("FAKE_SECRET")
	}
	return nil
}
func (*mdnsPlatformFixture) Resolve(string) (string, error) {
	panic("mDNS must never call platform DNS")
}
func (*mdnsPlatformFixture) NetworkInfo() (string, error) {
	panic("must use captured physical network")
}

type mdnsRawConnFixture struct{ err error }

func (r mdnsRawConnFixture) Control(f func(uintptr)) error {
	if r.err != nil {
		return r.err
	}
	f(42)
	return nil
}
func (mdnsRawConnFixture) Read(func(uintptr) bool) error  { panic("unexpected raw read") }
func (mdnsRawConnFixture) Write(func(uintptr) bool) error { panic("unexpected raw write") }

type mdnsDatagramFixture struct {
	wire   []byte
	source net.Addr
}
type mdnsPacketFixture struct {
	packets               []mdnsDatagramFixture
	writes                [][]byte
	destinations          []string
	deadline              time.Time
	writeErr, deadlineErr error
	readHook              func()
	block                 bool
	reading               chan struct{}
	closed                chan struct{}
	once                  sync.Once
}

func (p *mdnsPacketFixture) ReadFrom(b []byte) (int, net.Addr, error) {
	if p.readHook != nil {
		p.readHook()
		p.readHook = nil
	}
	if len(p.packets) > 0 {
		packet := p.packets[0]
		p.packets = p.packets[1:]
		return copy(b, packet.wire), packet.source, nil
	}
	if p.block {
		if p.reading != nil {
			close(p.reading)
			p.reading = nil
		}
		<-p.closed
		return 0, nil, net.ErrClosed
	}
	return 0, nil, os.ErrDeadlineExceeded
}
func (p *mdnsPacketFixture) WriteTo(b []byte, a net.Addr) (int, error) {
	p.writes = append(p.writes, append([]byte(nil), b...))
	p.destinations = append(p.destinations, a.String())
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	return len(b), nil
}
func (p *mdnsPacketFixture) Close() error { p.once.Do(func() { close(p.closed) }); return nil }
func (*mdnsPacketFixture) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(192, 168, 8, 5), Port: 49152}
}
func (p *mdnsPacketFixture) SetDeadline(d time.Time) error      { p.deadline = d; return p.deadlineErr }
func (p *mdnsPacketFixture) SetReadDeadline(d time.Time) error  { return p.SetDeadline(d) }
func (p *mdnsPacketFixture) SetWriteDeadline(d time.Time) error { return p.SetDeadline(d) }

func mdnsTestTransport(t *testing.T) (*androidMDNSTransport, *mdnsPlatformFixture, *mdnsPacketFixture) {
	t.Helper()
	p := &mdnsPlatformFixture{}
	p.valid.Store(true)
	transport, err := newAndroidMDNS(context.Background(), &platformBridge{callback: p, info: fixtureNetwork})
	if err != nil {
		t.Fatal(err)
	}
	packet := &mdnsPacketFixture{closed: make(chan struct{})}
	transport.listenPacket = func(ctx context.Context, lc *net.ListenConfig, network, address string) (net.PacketConn, error) {
		if network != "udp4" || address != "192.168.8.5:0" {
			t.Fatalf("wrong socket binding %s %s", network, address)
		}
		if lc.Control == nil {
			t.Fatal("missing protection callback")
		}
		if err := lc.Control(network, address, mdnsRawConnFixture{}); err != nil {
			return nil, err
		}
		if p.protected.Load() != 1 {
			t.Fatal("socket created without protection")
		}
		return packet, nil
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport, p, packet
}

func mdnsAnswerFixture(t *testing.T, mutate func(*mdns.Msg)) mdnsDatagramFixture {
	t.Helper()
	// Legacy-unicast responders echo the question; QU responders may omit it.
	m := new(mdns.Msg).SetReply(new(mdns.Msg).SetQuestion("printer.local.", mdns.TypeA))
	m.Id = 1234
	m.Authoritative = true
	m.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: "printer.local.", Rrtype: mdns.TypeA, Class: mdns.ClassINET | 0x8000, Ttl: 120}, A: net.IPv4(192, 168, 8, 9)}}
	if mutate != nil {
		mutate(m)
	}
	wire, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return mdnsDatagramFixture{wire, &net.UDPAddr{IP: net.IPv4(192, 168, 8, 9), Port: 5353}}
}
func mdnsQueryFixture() *mdns.Msg {
	m := new(mdns.Msg).SetQuestion("PRINTER.local.", mdns.TypeA)
	m.Id = 1234
	return m
}

func TestAndroidMDNSProtectedQUWireAndNormalizedAnswer(t *testing.T) {
	for _, omitQuestion := range []bool{false, true} {
		tr, p, conn := mdnsTestTransport(t)
		conn.packets = []mdnsDatagramFixture{mdnsAnswerFixture(t, func(m *mdns.Msg) {
			if omitQuestion {
				m.Question = nil
				m.Id = 0
			}
		})}
		query := mdnsQueryFixture()
		before := time.Now()
		response, err := tr.Exchange(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if p.protected.Load() != 1 || len(conn.writes) != 1 || conn.destinations[0] != "224.0.0.251:5353" {
			t.Fatal("wrong protected multicast path")
		}
		var sent mdns.Msg
		if err = sent.Unpack(conn.writes[0]); err != nil {
			t.Fatal(err)
		}
		if len(sent.Question) != 1 || sent.Question[0].Name != "printer.local." || sent.Question[0].Qclass != mdns.ClassINET|0x8000 || sent.RecursionDesired || sent.Response || len(sent.Extra) != 0 {
			t.Fatalf("not a bounded QU query: %v", &sent)
		}
		if query.Question[0].Qclass != mdns.ClassINET || query.Question[0].Name != "PRINTER.local." {
			t.Fatal("caller request mutated")
		}
		if response.Id != query.Id || !response.Response || len(response.Answer) != 1 || response.Answer[0].Header().Class != mdns.ClassINET || response.Question[0] != query.Question[0] {
			t.Fatalf("bad normalized reply %v", response)
		}
		if conn.deadline.Before(before.Add(time.Second)) || conn.deadline.After(before.Add(2*time.Second)) {
			t.Fatalf("unbounded deadline %v", conn.deadline)
		}
		select {
		case <-conn.closed:
		default:
			t.Fatal("socket leaked")
		}
	}
}

func TestAndroidMDNSRejectsUnrelatedOrInvalidDatagrams(t *testing.T) {
	for _, name := range []string{"port", "off-link", "multicast-source", "broadcast-source", "query", "wrong-id", "wrong-question", "many-questions", "wrong-answer", "class", "rcode", "truncated", "malformed", "oversized"} {
		t.Run(name, func(t *testing.T) {
			tr, _, conn := mdnsTestTransport(t)
			packet := mdnsAnswerFixture(t, func(m *mdns.Msg) {
				switch name {
				case "query":
					m.Response = false
				case "wrong-id":
					m.Id = 999
				case "wrong-question":
					m.Question[0].Name = "attacker.local."
				case "many-questions":
					m.Question = append(m.Question, m.Question[0])
				case "wrong-answer":
					m.Answer[0].Header().Name = "attacker.local."
				case "class":
					m.Answer[0].Header().Class = mdns.ClassCHAOS
				case "rcode":
					m.Rcode = mdns.RcodeServerFailure
				case "truncated":
					m.Truncated = true
				}
			})
			switch name {
			case "port":
				packet.source.(*net.UDPAddr).Port = 53
			case "off-link":
				packet.source.(*net.UDPAddr).IP = net.IPv4(8, 8, 8, 8)
			case "multicast-source":
				packet.source.(*net.UDPAddr).IP = net.IPv4(224, 0, 0, 251)
			case "broadcast-source":
				packet.source.(*net.UDPAddr).IP = net.IPv4(192, 168, 8, 255)
			case "malformed":
				packet.wire = []byte{1, 2, 3}
			case "oversized":
				packet.wire = append(packet.wire, make([]byte, 10000)...)
			}
			conn.packets = []mdnsDatagramFixture{packet, mdnsAnswerFixture(t, nil)}
			answer, err := tr.Exchange(context.Background(), mdnsQueryFixture())
			if err != nil || answer == nil || len(conn.packets) != 0 {
				t.Fatalf("accepted invalid %s packet or lost valid reply: %v", name, err)
			}
		})
	}
}

func TestAndroidMDNSFailsClosedBeforeSocket(t *testing.T) {
	for _, name := range []string{"public", "suffix-trick", "escaped-dot", "empty", "multiple", "bad-class", "invalid-network", "cancelled", "no-ipv4"} {
		t.Run(name, func(t *testing.T) {
			tr, p, _ := mdnsTestTransport(t)
			tr.listenPacket = func(context.Context, *net.ListenConfig, string, string) (net.PacketConn, error) {
				t.Fatal("opened forbidden socket")
				return nil, nil
			}
			query := mdnsQueryFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "public":
				query.Question[0].Name = "example.com."
			case "suffix-trick":
				query.Question[0].Name = "local.example.com."
			case "escaped-dot":
				query.Question[0].Name = `printer\.local.`
			case "empty":
				query.Question = nil
			case "multiple":
				query.Question = append(query.Question, query.Question[0])
			case "bad-class":
				query.Question[0].Qclass = mdns.ClassCHAOS
			case "invalid-network":
				p.valid.Store(false)
			case "cancelled":
				cancel()
			case "no-ipv4":
				tr.platform.info.Addresses = []string{"fd00::2/64"}
			}
			if answer, err := tr.Exchange(ctx, query); err == nil || answer != nil {
				t.Fatal("forbidden query succeeded")
			}
		})
	}
}

func TestAndroidMDNSProtectionIOAndTimeoutNeverFallback(t *testing.T) {
	for _, name := range []string{"protect", "raw-control", "listen", "write", "deadline", "timeout", "network-lost"} {
		t.Run(name, func(t *testing.T) {
			tr, p, conn := mdnsTestTransport(t)
			switch name {
			case "protect":
				p.fail = true
			case "raw-control":
				tr.listenPacket = func(_ context.Context, lc *net.ListenConfig, n, a string) (net.PacketConn, error) {
					return nil, lc.Control(n, a, mdnsRawConnFixture{errors.New("FAKE_SECRET")})
				}
			case "listen":
				tr.listenPacket = func(context.Context, *net.ListenConfig, string, string) (net.PacketConn, error) {
					return nil, errors.New("FAKE_SECRET")
				}
			case "write":
				conn.writeErr = errors.New("FAKE_SECRET")
			case "deadline":
				conn.deadlineErr = errors.New("FAKE_SECRET")
			case "network-lost":
				conn.packets = []mdnsDatagramFixture{mdnsAnswerFixture(t, nil)}
				conn.readHook = func() { p.valid.Store(false) }
			}
			response, err := tr.Exchange(context.Background(), mdnsQueryFixture())
			if err == nil || response != nil {
				t.Fatal("failed local query succeeded")
			}
			if err.Error() == "FAKE_SECRET" {
				t.Fatal("leaked platform error")
			}
			if len(conn.writes) > 1 {
				t.Fatal("retried failed DNS")
			}
			for _, dest := range conn.destinations {
				if dest != "224.0.0.251:5353" {
					t.Fatalf("public fallback: %s", dest)
				}
			}
		})
	}
}

func TestAndroidMDNSCancelCloseResetInterruptRead(t *testing.T) {
	for _, action := range []string{"cancel", "close", "reset"} {
		t.Run(action, func(t *testing.T) {
			tr, _, conn := mdnsTestTransport(t)
			conn.block = true
			reading := make(chan struct{})
			conn.reading = reading
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			tr.ExchangeAsync(ctx, mdnsQueryFixture(), func(_ *mdns.Msg, err error) { done <- err })
			select {
			case <-reading:
			case <-time.After(time.Second):
				t.Fatal("exchange did not start")
			}
			switch action {
			case "cancel":
				cancel()
			case "close":
				_ = tr.Close()
			case "reset":
				tr.Reset()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancellation succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("blocked read leaked")
			}
			if action == "close" {
				if _, err := tr.Exchange(context.Background(), mdnsQueryFixture()); err == nil {
					t.Fatal("closed transport reused")
				}
			}
		})
	}
}

var _ syscall.RawConn = mdnsRawConnFixture{}

func TestAndroidMDNSConstructorRequiresCapturedPlatform(t *testing.T) {
	for _, p := range []*platformBridge{nil, {info: fixtureNetwork}} {
		if _, err := newAndroidMDNS(context.Background(), p); err == nil {
			t.Fatal("accepted missing platform")
		}
	}
}

func TestAndroidMDNSDeadlineAndPacketFloodAreBounded(t *testing.T) {
	t.Run("caller-deadline", func(t *testing.T) {
		tr, _, conn := mdnsTestTransport(t)
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		want, _ := ctx.Deadline()
		_, err := tr.Exchange(ctx, mdnsQueryFixture())
		if err == nil || !conn.deadline.Equal(want) {
			t.Fatalf("lost caller deadline: %v %v", conn.deadline, err)
		}
	})
	t.Run("transport-deadline", func(t *testing.T) {
		tr, _, conn := mdnsTestTransport(t)
		conn.block = true
		before := time.Now()
		_, err := tr.Exchange(context.Background(), mdnsQueryFixture())
		elapsed := time.Since(before)
		if err == nil || elapsed < time.Second || elapsed > 2*time.Second {
			t.Fatalf("unbounded timeout: %v %v", elapsed, err)
		}
	})
	t.Run("packet-flood", func(t *testing.T) {
		tr, _, conn := mdnsTestTransport(t)
		for i := 0; i < 100; i++ {
			conn.packets = append(conn.packets, mdnsDatagramFixture{wire: []byte{1}, source: &net.UDPAddr{IP: net.IPv4(192, 168, 8, 9), Port: 5353}})
		}
		_, err := tr.Exchange(context.Background(), mdnsQueryFixture())
		if err == nil || len(conn.packets) != 36 {
			t.Fatalf("read flood was not capped: remaining=%d err=%v", len(conn.packets), err)
		}
	})
}

func TestAndroidMDNSDropsUnsolicitedRecordsAndSupportsLocalQueryTypes(t *testing.T) {
	for _, rrText := range []string{
		"printer.local. 120 IN AAAA fd00:1234::9",
		"printer.local. 120 IN TXT \"fixture\"",
		"printer.local. 120 IN SRV 0 0 80 target.local.",
		"printer.local. 120 IN PTR target.local.",
	} {
		tr, _, conn := mdnsTestTransport(t)
		rr, err := mdns.NewRR(rrText)
		if err != nil {
			t.Fatal(err)
		}
		query := mdnsQueryFixture()
		query.Question[0].Qtype = rr.Header().Rrtype
		conn.packets = []mdnsDatagramFixture{mdnsAnswerFixture(t, func(m *mdns.Msg) {
			m.Question[0].Qtype = rr.Header().Rrtype
			poison, _ := mdns.NewRR("example.com. 3600 IN A 8.8.8.8")
			m.Answer = []mdns.RR{rr, poison}
			m.Ns = []mdns.RR{poison}
			m.Extra = []mdns.RR{poison}
		})}
		answer, err := tr.Exchange(context.Background(), query)
		if err != nil || len(answer.Answer) != 1 || answer.Answer[0].Header().Rrtype != query.Question[0].Qtype || len(answer.Ns) != 0 || len(answer.Extra) != 0 {
			t.Fatalf("query type or record scope failure: %v %v", answer, err)
		}
	}
}

func TestAndroidMDNSValidityCheckedInsideControlAndBeforeSend(t *testing.T) {
	for _, stage := range []string{"before-control", "after-control"} {
		tr, p, conn := mdnsTestTransport(t)
		tr.listenPacket = func(_ context.Context, lc *net.ListenConfig, n, a string) (net.PacketConn, error) {
			if stage == "before-control" {
				p.valid.Store(false)
			}
			if err := lc.Control(n, a, mdnsRawConnFixture{}); err != nil {
				return nil, err
			}
			p.valid.Store(false)
			return conn, nil
		}
		if _, err := tr.Exchange(context.Background(), mdnsQueryFixture()); err == nil || len(conn.writes) != 0 {
			t.Fatal("invalid network emitted request")
		}
		want := int32(1)
		if stage == "before-control" {
			want = 0
		}
		if p.protected.Load() != want {
			t.Fatal("protection ran after network invalidation")
		}
	}
}

func TestAndroidMDNSLimitsConcurrentSocketsAndResetAllowsFreshExchange(t *testing.T) {
	tr, p, _ := mdnsTestTransport(t)
	opened := make(chan *mdnsPacketFixture, 17)
	tr.listenPacket = func(_ context.Context, lc *net.ListenConfig, network, address string) (net.PacketConn, error) {
		if err := lc.Control(network, address, mdnsRawConnFixture{}); err != nil {
			return nil, err
		}
		conn := &mdnsPacketFixture{block: true, closed: make(chan struct{}), reading: make(chan struct{})}
		opened <- conn
		return conn, nil
	}
	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		tr.ExchangeAsync(context.Background(), mdnsQueryFixture(), func(_ *mdns.Msg, err error) { done <- err })
		select {
		case <-opened:
		case <-time.After(time.Second):
			t.Fatal("exchange failed to open socket")
		}
	}
	if _, err := tr.Exchange(context.Background(), mdnsQueryFixture()); err == nil || p.protected.Load() != 16 {
		t.Fatal("exceeded simultaneous socket limit")
	}
	tr.Reset()
	for i := 0; i < 16; i++ {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("reset exchange succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("reset did not release active exchange")
		}
	}
	conn := &mdnsPacketFixture{closed: make(chan struct{}), packets: []mdnsDatagramFixture{mdnsAnswerFixture(t, nil)}}
	tr.listenPacket = func(_ context.Context, lc *net.ListenConfig, network, address string) (net.PacketConn, error) {
		if err := lc.Control(network, address, mdnsRawConnFixture{}); err != nil {
			return nil, err
		}
		return conn, nil
	}
	if answer, err := tr.Exchange(context.Background(), mdnsQueryFixture()); err != nil || answer == nil || p.protected.Load() != 17 {
		t.Fatalf("reset did not allow a freshly protected socket: %v", err)
	}
}
