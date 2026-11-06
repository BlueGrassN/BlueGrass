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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestXrayStackPacketsThroughDNSFakeIPAndAuthenticatedRelay(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var serving sync.WaitGroup
	serverErrors := make(chan error, 16)
	serving.Add(1)
	go func() {
		defer serving.Done()
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				serverErrors <- serveXrayDNSOrEcho(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		serving.Wait()
		close(serverErrors)
		for e := range serverErrors {
			if e != nil {
				t.Error(e)
			}
		}
	})
	config := testConfig()
	network, _ := config.Network(context.Background())
	p, err := prepareConfig(testProfile(), config, network, netip.MustParseAddr("8.8.8.8"))
	if err != nil {
		t.Fatal(err)
	}
	settings := json.RawMessage(`{"address":"127.0.0.1","port":` + itoaPort(listener.Addr()) + `}`)
	p.selected.Settings = &settings
	g := newSessionGate()
	runtime, err := newRuntime(context.Background(), p, g)
	if err != nil {
		t.Fatal(err)
	}
	r := runtime.(*xrayRuntime)
	t.Cleanup(func() { _ = r.Close() })
	// Start only the protocol and DNS components. Runtime.Start's physical NIC
	// discovery and Runtime.EnableTunnel's real adapter opener are not invoked.
	if err = r.selected.start(""); err != nil {
		t.Fatal(err)
	}
	if err = r.fake.Start(); err != nil {
		t.Fatal(err)
	}
	f := newRelayFixture(t, goodRelayReply())
	authorized := make(chan netip.AddrPort, 1)
	r.router.socks = relayOutboundForTest(t, f, g, context.Background(), func(a netip.AddrPort) error { authorized <- a; return nil })
	app := memoryApplication(t, r.router, g)
	packet, err := app.DialUDP()
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	// Reuse this exact source port for DNS and then authenticated application UDP.
	reply := stackDNSExchange(t, packet, netip.MustParseAddrPort("8.8.8.8:53"))
	if len(reply.Answer) != 1 {
		t.Fatal("Xray stack DNS ingress failed", reply)
	}
	a, ok := reply.Answer[0].(*dns.A)
	if !ok {
		t.Fatal("expected fake IPv4 answer")
	}
	fake, _ := netip.AddrFromSlice(a.A.To4())
	if !p.fake4.Contains(fake) {
		t.Fatal("DNS bypassed FakeIP")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := app.DialTCP(ctx, netip.AddrPortFrom(fake, 443))
	if err != nil {
		t.Fatal("Xray stack TCP did not reach selected outbound", err)
	}
	defer stream.Close()
	payload := []byte("xray-stack-fakeip-echo")
	stackIO(t, stream, func() error {
		if _, e := stream.Write(payload); e != nil {
			return e
		}
		got := make([]byte, len(payload))
		if _, e := io.ReadFull(stream, got); e != nil {
			return e
		}
		if !bytes.Equal(got, payload) {
			return errors.New("Xray TCP roundtrip changed payload")
		}
		return nil
	})
	_ = stream.Close()

	if _, err = packet.WriteTo(payload, net.UDPAddrFromAddrPort(netip.AddrPortFrom(fake, 443))); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-authorized:
		if a != goodRelayReply().bind.AddrPort() {
			t.Fatal("wrong authenticated relay")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UDP association not authorized")
	}
	// The peer becomes available only after the authorization callback returns.
	relayDone := make(chan error, 1)
	go func() {
		wire := make([]byte, len(payload)+10)
		var peer net.Conn
		select {
		case peer = <-f.udpReady:
		case <-ctx.Done():
			relayDone <- ctx.Err()
			return
		}
		_, e := io.ReadFull(peer, wire)
		if e == nil && (!bytes.Equal(wire[:10], []byte{0, 0, 0, 1, 9, 9, 9, 9, 1, 187}) || !bytes.Equal(wire[10:], payload)) {
			e = errors.New("relay packet changed")
		}
		if e == nil {
			_, e = peer.Write(wire)
		}
		relayDone <- e
	}()
	stackIO(t, packet, func() error {
		got := make([]byte, len(payload)+1)
		n, from, e := packet.ReadFrom(got)
		if e != nil {
			return e
		}
		if !bytes.Equal(got[:n], payload) || from.String() != netip.AddrPortFrom(fake, 443).String() {
			return fmt.Errorf("authenticated UDP reply: from %v, payload %q", from, got[:n])
		}
		return nil
	})
	if e := <-relayDone; e != nil {
		t.Fatal(e)
	}
	g.retire()
	assertAssociationClosed(t, f)
	if g.upload.Load() == 0 || g.download.Load() == 0 {
		t.Fatal("packet accounting missing")
	}
}

func TestXrayStackUDPSourcePortPreservesPerPacketDestination(t *testing.T) {
	for _, dnsFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("dns-first=%t", dnsFirst), func(t *testing.T) {
			g := newSessionGate()
			var dnsQueries atomic.Int32
			r := &nativeRouter{
				ctx: context.Background(), gate: g, config: policyOptions(t, false),
				selected: &stackEchoDialer{},
				dns: &exchangeTransport{exchange: func(_ context.Context, q *dns.Msg) (*dns.Msg, error) {
					dnsQueries.Add(1)
					answer := new(dns.Msg).SetReply(q)
					answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(9, 9, 9, 9)}}
					return answer, nil
				}},
			}
			app := memoryApplication(t, r, g)
			packet, err := app.DialUDP()
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			source := packet.LocalAddr().String()
			destinations := []string{"8.8.8.8:53", "9.9.9.9:443", "9.9.9.9:8443", "8.8.4.4:443", "1.1.1.1:53", "9.9.9.9:443"}
			if !dnsFirst {
				destinations[0], destinations[1] = destinations[1], destinations[0]
			}
			for i, address := range destinations {
				destination := netip.MustParseAddrPort(address)
				if destination.Port() == 53 {
					reply := stackDNSExchange(t, packet, destination)
					if len(reply.Answer) != 1 {
						t.Fatal("DNS lost after changing packet destination", reply)
					}
				} else {
					payload := []byte(fmt.Sprintf("packet-%d", i))
					got := stackPacketExchange(t, packet, destination, payload)
					// The outbound puts the address it actually received in its reply.
					// Checking both that and ReadFrom's source catches either direction
					// being pinned to the first destination of this full-cone flow.
					want := address + ":" + string(payload)
					if string(got) != want {
						t.Fatalf("packet %d went to the wrong outbound destination: got %q, want %q", i, got, want)
					}
				}
				if packet.LocalAddr().String() != source {
					t.Fatal("application source port changed")
				}
			}
			if dnsQueries.Load() != 2 {
				t.Fatalf("DNS packets bypassed interception: got %d exchanges", dnsQueries.Load())
			}
		})
	}
}

func serveXrayDNSOrEcho(c net.Conn) error {
	var greeting [3]byte
	if _, e := io.ReadFull(c, greeting[:]); e != nil {
		return e
	}
	if greeting != [3]byte{5, 1, 0} {
		return errors.New("unexpected Xray SOCKS greeting")
	}
	if _, e := c.Write([]byte{5, 0}); e != nil {
		return e
	}
	var request [10]byte
	if _, e := io.ReadFull(c, request[:]); e != nil {
		return e
	}
	if request[0] != 5 || request[1] != 1 || request[3] != 1 {
		return errors.New("unexpected Xray SOCKS target")
	}
	if _, e := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); e != nil {
		return e
	}
	if bytes.Equal(request[4:], []byte{1, 1, 1, 1, 0, 53}) {
		wire := &dns.Conn{Conn: c}
		m, e := wire.ReadMsg()
		if e != nil {
			return e
		}
		if len(m.Question) != 1 || m.Question[0].Name != "example.com." {
			return errors.New("unexpected public DNS question")
		}
		r := new(dns.Msg).SetReply(m)
		r.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.IPv4(9, 9, 9, 9)}}
		return wire.WriteMsg(r)
	}
	if !bytes.Equal(request[4:], []byte{9, 9, 9, 9, 1, 187}) {
		return errors.New("FakeIP leaked to selected outbound")
	}
	_, e := io.Copy(c, c)
	return e
}
