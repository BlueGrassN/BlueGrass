package tunengine

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

func idleGateConnections(g *sessionGate) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.connections)
}

func TestIngressIdleUDPReclaimsSourceAndCanReopen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newSessionGate()
		r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: &stackEchoDialer{}}
		app := memoryApplication(t, r, g)
		packet, err := app.DialUDP()
		if err != nil {
			t.Fatal(err)
		}
		defer packet.Close()
		to := netip.MustParseAddrPort("9.9.9.9:443")
		for i := 0; i < 2; i++ {
			if got := string(stackPacketExchange(t, packet, to, []byte("probe"))); got != "9.9.9.9:443:probe" {
				t.Fatal(got)
			}
			time.Sleep(3*time.Minute + time.Second)
			synctest.Wait()
			if n := idleGateConnections(g); n != 0 {
				t.Fatalf("idle source retained %d tracked connections", n)
			}
		}
	})
}

type idlePushDialer struct {
	stackEchoDialer
	opened chan *stackEchoPacketConn
}

func (d *idlePushDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	c := &stackEchoPacketConn{replies: make(chan stackEchoPacket, 16), closed: make(chan struct{})}
	d.opened <- c
	return c, nil
}

func TestIngressIdleUDPDownstreamKeepsFlowAndSourceAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newSessionGate()
		d := &idlePushDialer{opened: make(chan *stackEchoPacketConn, 1)}
		r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: d}
		app := memoryApplication(t, r, g)
		packet, err := app.DialUDP()
		if err != nil {
			t.Fatal(err)
		}
		defer packet.Close()
		to := netip.MustParseAddrPort("9.9.9.9:443")
		stackPacketExchange(t, packet, to, []byte("subscribe"))
		out := <-d.opened
		for i := 0; i < 8; i++ {
			time.Sleep(30 * time.Second)
			synctest.Wait()
			select {
			case <-out.closed:
				t.Fatal("active downstream was closed without an upstream packet")
			default:
			}
			payload := []byte(fmt.Sprintf("update-%d", i))
			out.replies <- stackEchoPacket{data: payload, from: to}
			stackIO(t, packet, func() error {
				b := make([]byte, 64)
				n, from, e := packet.ReadFrom(b)
				if e == nil && (from.String() != to.String() || string(b[:n]) != string(payload)) {
					return fmt.Errorf("unexpected downstream %v %q", from, b[:n])
				}
				return e
			})
		}
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		if n := idleGateConnections(g); n != 0 {
			t.Fatalf("finished downstream retained %d connections", n)
		}
	})
}

func TestIngressIdleUDPBoundsTargetsAndReclaimsRejectedSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newSessionGate()
		d := &idlePushDialer{opened: make(chan *stackEchoPacketConn, 128)}
		r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: d}
		app := memoryApplication(t, r, g)
		packet, err := app.DialUDP()
		if err != nil {
			t.Fatal(err)
		}
		defer packet.Close()
		for port := uint16(10000); port < 10065; port++ {
			_, err := packet.WriteTo([]byte("target"), net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("9.9.9.9"), port)))
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		if n := len(d.opened); n != 64 {
			t.Fatalf("per-source target bound: opened %d", n)
		}
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		if n := idleGateConnections(g); n != 0 {
			t.Fatalf("idle targets retained %d connections", n)
		}
		// Even a source whose only target fails policy must eventually disappear.
		_, err = packet.WriteTo([]byte("denied"), net.UDPAddrFromAddrPort(netip.MustParseAddrPort("10.0.0.1:443")))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(3*time.Minute + time.Second)
		synctest.Wait()
		if n := idleGateConnections(g); n != 0 {
			t.Fatalf("rejected source retained %d connections", n)
		}
	})
}

func TestIngressIdleTCPDNSForcesCloseWithXrayDeadlines(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				g := newSessionGate()
				r := &nativeRouter{ctx: context.Background(), gate: g}
				app := memoryApplication(t, r, g)
				c, err := app.DialTCP(context.Background(), netip.MustParseAddrPort("8.8.8.8:53"))
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if partial {
					if _, err := c.Write([]byte{0}); err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				time.Sleep(16 * time.Second)
				synctest.Wait()
				if n := idleGateConnections(g); n != 0 {
					t.Fatalf("DNS deadline left %d connections blocked", n)
				}
				if _, err := c.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("DNS timeout did not send EOF: %v", err)
				}
			})
		})
	}
}

func TestIngressIdleTCPDNSRenewsBetweenQueries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newSessionGate()
		r := &nativeRouter{ctx: context.Background(), gate: g, dns: &exchangeTransport{exchange: func(_ context.Context, q *dns.Msg) (*dns.Msg, error) { return new(dns.Msg).SetReply(q), nil }}}
		app := memoryApplication(t, r, g)
		c, err := app.DialTCP(context.Background(), netip.MustParseAddrPort("8.8.8.8:53"))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		wire := &dns.Conn{Conn: c}
		for i := 0; i < 3; i++ {
			time.Sleep(10 * time.Second)
			stackIO(t, c, func() error {
				q := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
				if e := wire.WriteMsg(q); e != nil {
					return e
				}
				reply, e := wire.ReadMsg()
				if e == nil && (!reply.Response || reply.Id != q.Id) {
					return fmt.Errorf("DNS reply mismatch")
				}
				return e
			})
		}
		time.Sleep(16 * time.Second)
		synctest.Wait()
		if n := idleGateConnections(g); n != 0 {
			t.Fatalf("last DNS query retained %d connections", n)
		}
	})
}

func TestIngressIdleTCPDNSCancelsPendingExchange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newSessionGate()
		started, canceled := make(chan struct{}), make(chan struct{})
		r := &nativeRouter{ctx: context.Background(), gate: g, dns: &exchangeTransport{exchange: func(ctx context.Context, _ *dns.Msg) (*dns.Msg, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}}}
		app := memoryApplication(t, r, g)
		c, err := app.DialTCP(context.Background(), netip.MustParseAddrPort("8.8.8.8:53"))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := (&dns.Conn{Conn: c}).WriteMsg(new(dns.Msg).SetQuestion("example.com.", dns.TypeA)); err != nil {
			t.Fatal(err)
		}
		<-started
		time.Sleep(16 * time.Second)
		synctest.Wait()
		select {
		case <-canceled:
		default:
			t.Fatal("forced DNS close did not cancel pending exchange")
		}
		if n := idleGateConnections(g); n != 0 {
			t.Fatalf("pending DNS retained %d connections", n)
		}
	})
}
