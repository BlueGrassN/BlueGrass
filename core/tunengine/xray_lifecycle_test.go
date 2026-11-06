package tunengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"bluegrass/native/core/protocolconfig"
	M "github.com/sagernet/sing/common/metadata"
)

// A real loopback socket holds the proxy handshake open. No external address
// is dialed and no native adapter or host route is touched.
func stalledXray(t *testing.T) *xrayOutbound {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, c)
	}()
	selected, err := protocolconfig.Build(protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{}}, netip.MustParseAddrPort("8.8.8.8:1080"))
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"servers":[{"address":"127.0.0.1","port":` + itoaPort(l.Addr()) + `}]}`)
	selected.Settings = &raw
	out, err := newXrayOutbound(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = out.Close()
		_ = l.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Xray physical handshake survived close")
		}
	})
	if err = out.start(""); err != nil {
		t.Fatal(err)
	}
	return out
}

type physicalGuardProbe func(context.Context, string, string) (net.Conn, error)

func (p physicalGuardProbe) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return p(ctx, network, address)
}

// This exercises the real Xray/protocolconfig context boundary. A platform
// missing the system-dialer guard adapter otherwise falls through to ordinary
// sockets: SOCKS authorization is never reached and handshakes outlive Close.
// The only fallback endpoint here is a local stalled fixture, never the public
// logical destination, so a missing adapter cannot cause external test traffic.
func TestXrayPhysicalGuardIsUsedForTCPAndUDP(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			out := stalledXray(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type attempt struct{ network, address string }
			called := make(chan attempt, 8)
			guard := physicalGuardProbe(func(_ context.Context, network, address string) (net.Conn, error) {
				called <- attempt{network, address}
				return nil, errors.New("physical guard probe refused socket")
			})
			c, err := out.DialContext(protocolconfig.WithPhysicalDialer(ctx, guard), network, M.ParseSocksaddr("9.9.9.9:443"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			select {
			case attempt := <-called:
				host, _, err := net.SplitHostPort(attempt.address)
				if err != nil || attempt.network != "tcp" || host != "127.0.0.1" {
					t.Fatal("guard received the wrong physical proxy endpoint", attempt, err)
				}
			case <-time.After(time.Second):
				t.Fatal("platform system dialer never consumed the per-flow physical guard")
			}
		})
	}
}

func TestXrayAdapterDeadlinesAndCallerCancellation(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			out := stalledXray(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, err := out.DialContext(ctx, network, M.ParseSocksaddr("9.9.9.9:443"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			read := func() <-chan error {
				done := make(chan error, 1)
				go func() { _, e := c.Read(make([]byte, 1)); done <- e }()
				return done
			}
			select {
			case err = <-read():
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("read deadline: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Xray read ignored deadline")
			}
			if err = c.SetReadDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			done := read()
			select {
			case e := <-done:
				t.Fatalf("cleared deadline still active: %v", e)
			case <-time.After(20 * time.Millisecond):
			}
			cancel()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("canceled read succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("caller cancellation left read blocked")
			}
		})
	}
}

func TestXrayAdapterRejectsOversizeUDPWithoutSplitting(t *testing.T) {
	out := stalledXray(t)
	dest := M.ParseSocksaddr("9.9.9.9:443")
	c, err := out.ListenPacket(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n, err := c.WriteTo(bytes.Repeat([]byte("x"), 10000), dest.UDPAddr()); n != 0 || err == nil {
		t.Fatalf("oversize datagram reported success: %d %v", n, err)
	}
}

func TestRouterCancellationClosesIdleDNSIngress(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			g := newSessionGate()
			g.open()
			defer g.retire()
			r := &nativeRouter{ctx: context.Background(), gate: g}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			m := inboundContext{Network: network, Destination: M.ParseSocksaddr("8.8.8.8:53")}
			if network == "tcp" {
				client, server := net.Pipe()
				defer client.Close()
				defer server.Close()
				go func() { done <- r.RouteConnection(ctx, server, m) }()
			} else {
				p := &localOnlyPacket{input: make(chan []byte), output: make(chan []byte), closed: make(chan struct{})}
				defer p.Close()
				go func() { done <- r.RoutePacketConnection(ctx, p, m) }()
			}
			// Let the real ingress enter its blocking read before cancellation.
			select {
			case e := <-done:
				t.Fatalf("ingress stopped before cancellation: %v", e)
			case <-time.After(20 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("canceled DNS ingress stayed blocked")
			}
		})
	}
}
