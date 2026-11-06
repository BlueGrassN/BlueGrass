package tunengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"bluegrass/native/core/protocolconfig"
	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

func TestXrayDispatchCarriesRealTCPWithoutNativeAdapter(t *testing.T) {
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		greeting := make([]byte, 3)
		_, e = io.ReadFull(c, greeting)
		if e != nil {
			done <- e
			return
		}
		if !bytes.Equal(greeting, []byte{5, 1, 0}) {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = c.Write([]byte{5, 0})
		request := make([]byte, 10)
		_, e = io.ReadFull(c, request)
		if e != nil {
			done <- e
			return
		}
		if !bytes.Equal(request, []byte{5, 1, 0, 1, 9, 9, 9, 9, 1, 187}) {
			done <- io.ErrUnexpectedEOF
			return
		}
		_, _ = c.Write(append([]byte{5, 0, 0, 4}, make([]byte, 18)...))
		_, e = io.Copy(c, c)
		done <- e
	}()
	c, e := protocolconfig.Build(protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{}}, netip.MustParseAddrPort("8.8.8.8:1080"))
	if e != nil {
		t.Fatal(e)
	}
	// Only the test server address changes; production Build continues to pin public entries.
	raw := json.RawMessage([]byte(`{"servers":[{"address":"127.0.0.1","port":` + itoaPort(listener.Addr()) + `}]}`))
	c.Settings = &raw
	out, e := newXrayOutbound(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	if e = out.start(""); e != nil {
		t.Fatal(e)
	}
	guard := newGuardedSOCKS(context.Background(), preparedConfig{}, newSessionGate(), "", "", nil)
	guard.selected = out
	conn, e := guard.DialContext(context.Background(), "tcp", M.ParseSocksaddr("9.9.9.9:443"))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte("actual-xray-dispatch")
	if _, e = conn.Write(payload); e != nil {
		t.Fatal(e)
	}
	got := make([]byte, len(payload))
	if _, e = io.ReadFull(conn, got); e != nil || !bytes.Equal(got, payload) {
		t.Fatal(string(got), e)
	}
	_ = conn.Close()
	_ = out.Close()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Xray physical connection survived close")
	}
}
func itoaPort(a net.Addr) string { _, p, _ := net.SplitHostPort(a.String()); return p }
func newTestXraySOCKSPacket(t *testing.T) (net.PacketConn, *relayFixture) {
	t.Helper()
	f := newRelayFixture(t, goodRelayReply())
	g := newSessionGate()
	g.open()
	t.Cleanup(g.retire)
	h := relayOutboundForTest(t, f, g, context.Background(), func(netip.AddrPort) error { return nil })
	c, e := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c, f
}
func TestXraySOCKSRuntimeRejectsOversizedDatagramsBeforeEnqueue(t *testing.T) {
	c, _ := newTestXraySOCKSPacket(t)
	if n, e := c.WriteTo(bytes.Repeat([]byte("x"), 10000), M.ParseSocksaddr("9.9.9.9:443").UDPAddr()); n != 0 || e == nil {
		t.Fatal("Xray codec limit must fail explicitly instead of silently dropping a large datagram", n, e)
	}
}
func TestFakeIPRestoresNameThroughSelectedDNSAndRechecksResolvedRoute(t *testing.T) {
	cfg := testConfig()
	network, _ := cfg.Network(context.Background())
	p, e := prepareConfig(testProfile(), cfg, network, netip.MustParseAddr("8.8.8.8"))
	if e != nil {
		t.Fatal(e)
	}
	policy, e := decodeOptions(p, newSessionGate())
	if e != nil {
		t.Fatal(e)
	}
	fake := newFakeIPTransport(context.Background(), p)
	defer fake.Close()
	_ = fake.Start()
	a, e := fake.store.Create("example.com", false)
	if e != nil {
		t.Fatal(e)
	}
	queried := 0
	upstream := &localOnlyDNSFixture{exchange: func(m *dns.Msg) *dns.Msg {
		queried++
		r := new(dns.Msg).SetReply(m)
		r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: m.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(10, 22, 33, 44)}}
		return r
	}}
	g := newSessionGate()
	g.open()
	r := &nativeRouter{ctx: context.Background(), gate: g, config: policy, fake: fake.store, dns: &policyDNS{config: policy, transports: map[string]dnsTransport{"public": upstream}}}
	_, _, e = r.dialer(context.Background(), inboundContext{Network: "tcp", Destination: M.SocksaddrFrom(a, 443)})
	if e == nil || queried != 1 {
		t.Fatal("resolved private address bypassed route policy", queried, e)
	}
	if _, _, e = r.dialer(context.Background(), inboundContext{Network: "tcp", Destination: M.SocksaddrFrom(a.Next(), 443)}); e == nil || queried != 1 {
		t.Fatal("unknown fake mapping resolved externally")
	}
}

func TestSOCKSPacketRejectsReservedBytesAndChangedDestination(t *testing.T) {
	for _, header := range [][]byte{{1, 0, 0}, {0, 0, 1}} {
		t.Run(fmt.Sprint(header), func(t *testing.T) {
			c, f := newTestXraySOCKSPacket(t)
			if n, e := c.WriteTo([]byte("x"), M.ParseSocksaddr("1.1.1.1:53").UDPAddr()); n != 0 || e == nil {
				t.Fatal("changed target accepted")
			}
			done := make(chan error, 1)
			go func() { _, e := f.udpPeer.Write(append(header, 1, 9, 9, 9, 9, 1, 187, 'x')); done <- e }()
			if _, _, e := c.ReadFrom(make([]byte, 16)); e == nil {
				t.Fatal("malformed SOCKS datagram accepted")
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestSOCKSPacketShortReadDropsTailAndPreservesNextDatagram(t *testing.T) {
	c, f := newTestXraySOCKSPacket(t)
	done := make(chan error, 1)
	go func() {
		for _, payload := range [][]byte{bytes.Repeat([]byte("x"), 100), []byte("next")} {
			wire := append([]byte{0, 0, 0, 1, 9, 9, 9, 9, 1, 187}, payload...)
			if _, e := f.udpPeer.Write(wire); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	if _, _, err := c.ReadFrom(make([]byte, 1)); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal("truncation not reported", err)
	}
	b := make([]byte, 16)
	n, from, err := c.ReadFrom(b)
	if err != nil || string(b[:n]) != "next" || from.String() != "9.9.9.9:443" {
		t.Fatal("datagram tail escaped", n, from, err)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
