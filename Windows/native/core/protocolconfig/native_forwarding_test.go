package protocolconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/freedom"
)

// Only tests bypass Build's public IPv4 guard to reach loopback fixtures.
func nativeFixtureOutbound(t *testing.T, ctx context.Context, protocol string, settings any) *Outbound {
	t.Helper()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	j := json.RawMessage(raw)
	out, err := StartOutbound(ctx, &conf.OutboundDetourConfig{Tag: "selected", Protocol: protocol, Settings: &j})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	return out
}

func nativeEchoTCP(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	conns := make(map[net.Conn]bool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			conns[c] = true
			mu.Unlock()
			wg.Add(1)
			go func() { defer wg.Done(); defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String()
}

func nativeEchoUDP(t *testing.T) string {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 65535)
		for {
			n, a, e := c.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = c.WriteTo(b[:n], a)
		}
	}()
	t.Cleanup(func() { _ = c.Close(); <-done })
	return c.LocalAddr().String()
}

func nativeXrayServer(t *testing.T, protocol string, settings any) string {
	t.Helper()
	// Windows has independent TCP/UDP exclusions, and either ephemeral allocator
	// may repeatedly return a port unavailable to the other protocol. Probe both
	// transports at random non-ephemeral candidates without changing exclusions.
	port := 0
	for attempt := 0; attempt < 64; attempt++ {
		candidate := net.JoinHostPort("127.0.0.1", strconv.Itoa(20000+rand.IntN(29000)))
		packet, err := net.ListenPacket("udp4", candidate)
		if err != nil {
			continue
		}
		listener, err := net.Listen("tcp4", packet.LocalAddr().String())
		if err != nil {
			_ = packet.Close()
			continue
		}
		port = listener.Addr().(*net.TCPAddr).Port
		_ = packet.Close()
		_ = listener.Close()
		break
	}
	if port == 0 {
		t.Fatal("no loopback TCP+UDP fixture port available")
	}
	raw, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "none", "access": "none", "error": "none"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": protocol, "settings": settings}},
		// Xray's server default blocks loopback targets for encrypted inbounds.
		// This test-only rule permits only the local echo fixtures.
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	})
	var c conf.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	config, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := core.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Start(); err != nil {
		_ = instance.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func TestNativeRealTCPAndUDPForwarding(t *testing.T) {
	for _, protocol := range []string{"socks", "2022-blake3-chacha20-poly1305"} {
		t.Run(protocol, func(t *testing.T) {
			tcpTarget, udpTarget := nativeEchoTCP(t), nativeEchoUDP(t)
			var entry string
			var settings map[string]any
			outboundProtocol := protocol
			if protocol == "socks" {
				entry = nativeXrayServer(t, "socks", map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "fixture", "pass": "fixture-password"}}, "udp": true, "ip": "127.0.0.1"})
				settings = map[string]any{"user": "fixture", "pass": "fixture-password"}
			} else {
				password := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
				entry = nativeXrayServer(t, "shadowsocks", map[string]any{"method": protocol, "password": password, "network": "tcp,udp"})
				outboundProtocol = "shadowsocks"
				settings = map[string]any{"method": protocol, "password": password}
			}
			host, portString, _ := net.SplitHostPort(entry)
			port, _ := strconv.Atoi(portString)
			settings["address"] = host
			settings["port"] = port
			out := nativeFixtureOutbound(t, context.Background(), outboundProtocol, settings)
			conn, err := out.DialContext(context.Background(), "tcp", tcpTarget)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			want := bytes.Repeat([]byte("real-xray-forwarding-"), 4000)
			writeDone := make(chan error, 1)
			go func() { _, e := conn.Write(want); writeDone <- e }()
			got := make([]byte, len(want))
			if _, err = io.ReadFull(conn, got); err != nil {
				t.Fatal(err)
			}
			if err = <-writeDone; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("TCP payload changed")
			}
			packet, err := out.ListenPacket(context.Background(), udpTarget)
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			_ = packet.SetDeadline(time.Now().Add(5 * time.Second))
			addr, _ := net.ResolveUDPAddr("udp", udpTarget)
			for _, size := range []int{1, 300, 1500, 7000} {
				want := bytes.Repeat([]byte{byte(size % 251)}, size)
				if n, err := packet.WriteTo(want, addr); err != nil || n != size {
					t.Fatalf("UDP write: %d %v", n, err)
				}
				got := make([]byte, 8192)
				n, from, err := packet.ReadFrom(got)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got[:n], want) || from.String() != addr.String() {
					t.Fatalf("UDP datagram/address changed: n=%d from=%v", n, from)
				}
			}
		})
	}
}

func TestNativeDeadlinesResetAndCancellation(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			target := nativeEchoTCP(t)
			if network == "udp" {
				target = nativeEchoUDP(t)
			}
			session, stop := context.WithCancel(context.Background())
			defer stop()
			out := nativeFixtureOutbound(t, session, "freedom", map[string]any{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := out.DialContext(ctx, network, target)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(35 * time.Millisecond))
			_, err = conn.Read(make([]byte, 16))
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("read deadline: %v", err)
			}
			_ = conn.SetReadDeadline(time.Time{})
			_ = conn.SetWriteDeadline(time.Now().Add(-time.Second))
			if _, err = conn.Write([]byte("no-send")); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("write deadline: %v", err)
			}
			_ = conn.SetWriteDeadline(time.Time{})
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err = conn.Write([]byte("reset")); err != nil {
				t.Fatal(err)
			}
			b := make([]byte, 5)
			if _, err = io.ReadFull(conn, b); err != nil || string(b) != "reset" {
				t.Fatalf("deadline reset failed: %q %v", b, err)
			}
			_ = conn.SetReadDeadline(time.Time{})
			done := make(chan struct{})
			go func() { defer close(done); _, _ = conn.Read(make([]byte, 1)) }()
			cancel()
			nativeAwait(t, done, "canceled read")
			stop()
			if err = out.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativePacketDeadlineChangesWakeExistingRead(t *testing.T) {
	out := nativeFixtureOutbound(t, context.Background(), "freedom", map[string]any{})
	packet, err := out.ListenPacket(context.Background(), nativeEchoUDP(t))
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	done := make(chan struct{})
	var readErr error
	go func() { defer close(done); _, _, readErr = packet.ReadFrom(make([]byte, 1)) }()
	_ = packet.SetReadDeadline(time.Now().Add(35 * time.Millisecond))
	nativeAwait(t, done, "deadline update")
	if !errors.Is(readErr, os.ErrDeadlineExceeded) {
		t.Fatalf("updated deadline: %v", readErr)
	}
}

func TestNativeSessionRetirementClosesAllFlows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := nativeFixtureOutbound(t, ctx, "freedom", map[string]any{})
	var conns []net.Conn
	for _, network := range []string{"tcp", "udp"} {
		target := nativeEchoTCP(t)
		if network == "udp" {
			target = nativeEchoUDP(t)
		}
		for i := 0; i < 4; i++ {
			c, e := out.DialContext(context.Background(), network, target)
			if e != nil {
				t.Fatal(e)
			}
			conns = append(conns, c)
		}
	}
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Read(make([]byte, 1)) }()
	}
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = out.Close(); wg.Wait() }()
	nativeAwait(t, done, "session retirement")
	if c, e := out.ListenPacket(context.Background(), "127.0.0.1:53"); e == nil || c != nil {
		t.Fatal("retired packet flow accepted")
	}
}
