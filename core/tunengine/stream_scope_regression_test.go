package tunengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"bluegrass/native/core/protocolconfig"
	M "github.com/sagernet/sing/common/metadata"
)

// Separate real pipes preserve TCP's independent read/write lifetimes without
// OS sockets. Closing upload yields EOF while download stays usable.
type regressionDuplex struct {
	net.Conn
	writer net.Conn
	once   sync.Once
}

func TestIngressHalfCloseCancellationInterruptsDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, ingress := regressionDuplexPair()
		out, server := regressionDuplexPair()
		defer client.Close()
		defer ingress.Close()
		defer out.Close()
		defer server.Close()
		g := newSessionGate()
		g.open()
		defer g.retire()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: &regressionStreamDialer{conn: out}}
		done := make(chan error, 1)
		go func() {
			done <- r.RouteConnection(ctx, ingress, inboundContext{Network: "tcp", Destination: M.ParseSocksaddr("9.9.9.9:8443")})
		}()
		_ = client.CloseWrite()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("cancellation waited for the half-close drain timeout")
		}
	})
}

func TestIngressHalfCloseThroughSharedXray(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- func() error {
			c, err := listener.Accept()
			if err != nil {
				return err
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			greeting := make([]byte, 3)
			if _, err = io.ReadFull(c, greeting); err != nil {
				return err
			}
			if !bytes.Equal(greeting, []byte{5, 1, 0}) {
				return errors.New("unexpected SOCKS greeting")
			}
			if _, err = c.Write([]byte{5, 0}); err != nil {
				return err
			}
			request := make([]byte, 10)
			if _, err = io.ReadFull(c, request); err != nil {
				return err
			}
			if !bytes.Equal(request, []byte{5, 1, 0, 1, 9, 9, 9, 9, 32, 251}) {
				return errors.New("unexpected SOCKS destination")
			}
			if _, err = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
				return err
			}
			payload := make([]byte, 7)
			if _, err = io.ReadFull(c, payload); err != nil {
				return err
			}
			if string(payload) != "request" {
				return errors.New("request changed")
			}
			// Xray's SOCKS outbound does not propagate a physical FIN; a delayed
			// reply to a framed request must still survive application upload EOF.
			time.Sleep(30 * time.Millisecond)
			_, err = c.Write([]byte("delayed-response"))
			return err
		}()
	}()
	selected, err := protocolconfig.Build(protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{}}, netip.MustParseAddrPort("8.8.8.8:1080"))
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	raw := json.RawMessage(`{"address":"127.0.0.1","port":` + port + `}`)
	selected.Settings = &raw
	xray, err := newXrayOutbound(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	defer xray.Close()
	if err = xray.start(""); err != nil {
		t.Fatal(err)
	}
	client, ingress := regressionDuplexPair()
	defer client.Close()
	defer ingress.Close()
	g := newSessionGate()
	g.open()
	defer g.retire()
	r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: xray}
	done := make(chan error, 1)
	go func() {
		done <- r.RouteConnection(context.Background(), ingress, inboundContext{Network: "tcp", Destination: M.ParseSocksaddr("9.9.9.9:8443")})
	}()
	_ = client.Conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_ = client.writer.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	_ = client.CloseWrite()
	got, err := io.ReadAll(client)
	if err != nil || string(got) != "delayed-response" {
		t.Fatalf("Xray half-close response: %q %v", got, err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS fixture did not exit")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Xray ingress did not exit")
	}
}

func regressionDuplexPair() (*regressionDuplex, *regressionDuplex) {
	aRead, bWrite := net.Pipe()
	bRead, aWrite := net.Pipe()
	return &regressionDuplex{Conn: aRead, writer: aWrite}, &regressionDuplex{Conn: bRead, writer: bWrite}
}
func (c *regressionDuplex) Write(b []byte) (int, error) { return c.writer.Write(b) }
func (c *regressionDuplex) CloseWrite() error           { return c.writer.Close() }
func (c *regressionDuplex) Close() error {
	c.once.Do(func() { _ = c.Conn.Close(); _ = c.writer.Close() })
	return nil
}

type regressionStreamDialer struct {
	stackEchoDialer
	conn net.Conn
}

func (d *regressionStreamDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

func TestIngressHalfCloseDrainsAndBoundsDownstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, ingress := regressionDuplexPair()
		out, server := regressionDuplexPair()
		defer client.Close()
		defer ingress.Close()
		defer out.Close()
		defer server.Close()
		g := newSessionGate()
		g.open()
		defer g.retire()
		r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), selected: &regressionStreamDialer{conn: out}}
		done := make(chan error, 1)
		go func() {
			done <- r.RouteConnection(context.Background(), ingress, inboundContext{Network: "tcp", Destination: M.ParseSocksaddr("9.9.9.9:8443")})
		}()
		request := make(chan string, 1)
		go func() { b, _ := io.ReadAll(server); request <- string(b) }()
		if _, err := client.Write([]byte("request")); err != nil {
			t.Fatal(err)
		}
		if err := client.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		if got := <-request; got != "request" {
			t.Fatalf("upload changed: %q", got)
		}
		// Replies continue beyond the 30s drain window if they keep arriving.
		for i := 0; i < 3; i++ {
			time.Sleep(20 * time.Second)
			written := make(chan error, 1)
			go func() { _, err := server.Write([]byte("response")); written <- err }()
			b := make([]byte, 8)
			if _, err := io.ReadFull(client, b); err != nil {
				t.Fatalf("half-close discarded delayed response: %v", err)
			}
			if string(b) != "response" {
				t.Fatalf("response changed: %q", b)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(31 * time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("half-closed stream survived downstream idle limit")
		}
	})
}

func TestScopedPacketKeepsLogicalPeerSeparateFromPhysicalZone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		physical, peer := net.Pipe()
		defer physical.Close()
		defer peer.Close()
		logical := net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[fe80::2]:1234"))
		bound := (&scopedDirect{interfaceName: "Ethernet"}).destination(M.SocksaddrFromNet(logical)).UDPAddr()
		c := &connectedPacket{Conn: physical, remote: bound}
		read := make(chan string, 1)
		go func() { b := make([]byte, 4); n, _ := peer.Read(b); read <- string(b[:n]) }()
		if n, err := c.WriteTo([]byte("ping"), logical); err != nil || n != 4 {
			t.Fatalf("logical destination rejected by physical zone: %d %v", n, err)
		}
		if got := <-read; got != "ping" {
			t.Fatal(got)
		}
		written := make(chan error, 1)
		go func() { _, err := peer.Write([]byte("pong")); written <- err }()
		b := make([]byte, 4)
		if n, from, err := c.ReadFrom(b); err != nil || n != 4 || from.String() != "[fe80::2]:1234" {
			t.Fatalf("physical scope leaked into logical reply: %d %v %v", n, from, err)
		}
		if err := <-written; err != nil {
			t.Fatal(err)
		}
		for _, addr := range []net.Addr{nil, net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[fe80::3]:1234")), net.UDPAddrFromAddrPort(netip.MustParseAddrPort("[fe80::2]:1235")), &net.UDPAddr{IP: net.ParseIP("fe80::2"), Port: 1234, Zone: "Other"}} {
			if n, err := c.WriteTo([]byte("deny"), addr); err == nil || n != 0 {
				t.Fatalf("changed logical peer accepted: %v", addr)
			}
		}
		_ = peer.Close()
		if _, _, err := c.ReadFrom(b); !errors.Is(err, io.EOF) {
			t.Fatalf("closed physical connection: %v", err)
		}
	})
}
