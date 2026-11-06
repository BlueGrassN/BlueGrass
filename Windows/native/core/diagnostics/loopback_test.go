package diagnostics

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopbackHTTPConnectActualTunnel(t *testing.T) {
	_, pool, targetDial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ip":"8.8.8.8"}`) })
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	done := make(chan bool, 1)
	go func() {
		conn, err := proxy.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil || req.Method != "CONNECT" || req.Host != "8.8.4.4:443" || req.Header.Get("Proxy-Authorization") != "" {
			done <- false
			return
		}
		target, err := targetDial(context.Background(), "tcp", "")
		if err != nil {
			done <- false
			return
		}
		defer target.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		go io.Copy(target, reader)
		io.Copy(conn, target)
		done <- true
	}()
	req := Request{Binding: binding(), ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}}, Proxy: &LoopbackProxy{Kind: "http_connect", Address: proxy.Addr().String()}, UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}}
	report := (Runner{RootCAs: pool}).Run(context.Background(), req)
	if r := result(t, report, "proxy_exit"); r.Status != Passed {
		t.Fatal(r)
	}
	if r := result(t, report, "proxy_udp"); r.Status != Skipped || r.Code != "udp_channel_unavailable" {
		t.Fatal(r)
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("CONNECT failed")
		}
	case <-time.After(time.Second):
		t.Fatal("tunnel not closed")
	}
}

func TestLoopbackHTTPRejectsAuthAndOversizedHeaders(t *testing.T) {
	for _, reply := range []string{"HTTP/1.1 407 Proxy Authentication Required\r\n\r\n", "HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("x", 9000) + "\r\n\r\n"} {
		proxy, _ := net.Listen("tcp", "127.0.0.1:0")
		go func() {
			conn, err := proxy.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			http.ReadRequest(bufio.NewReader(conn))
			io.WriteString(conn, reply)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := loopbackDial(LoopbackProxy{Kind: "http_connect", Address: proxy.Addr().String()})(ctx, "tcp", "8.8.4.4:443")
		if err == nil {
			conn.Close()
			t.Error("unsafe handshake accepted")
		}
		cancel()
		proxy.Close()
	}
}

// This relay fixture exercises real TCP SOCKS negotiation and encapsulated UDP
// datagrams. It never sends packets to the public logical target.
func socksFixture(t *testing.T, badFragment bool, authRequired bool) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relay, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); relay.Close() })
	hits := new(atomic.Int32)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(conn, greeting); err != nil {
			return
		}
		if string(greeting) != string([]byte{5, 1, 0}) {
			return
		}
		if authRequired {
			conn.Write([]byte{5, 2})
			return
		}
		conn.Write([]byte{5, 0})
		request := make([]byte, 10)
		if _, err := io.ReadFull(conn, request); err != nil || request[1] != 3 {
			return
		}
		_, port, _ := net.SplitHostPort(relay.LocalAddr().String())
		reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
		binary.BigEndian.PutUint16(reply[8:], uint16(portNumber(port)))
		conn.Write(reply)
		io.Copy(io.Discard, conn)
	}()
	go func() {
		buf := make([]byte, 1024)
		n, peer, err := relay.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 10 || string(buf[:10]) != string([]byte{0, 0, 0, 1, 8, 8, 4, 4, 0x27, 0x0f}) {
			return
		}
		hits.Add(1)
		if badFragment {
			buf[2] = 1
		}
		relay.WriteTo(buf[:n], peer)
	}()
	return listener.Addr().String(), hits
}

func TestSOCKSUDPActualRelayAndInvalidFragment(t *testing.T) {
	for _, bad := range []bool{false, true} {
		addr, hits := socksFixture(t, bad, false)
		req := Request{TimeoutMS: 500, Binding: binding(), Proxy: &LoopbackProxy{Kind: "socks5", Address: addr}, UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}}
		report := (Runner{}).Run(context.Background(), req)
		want := Passed
		if bad {
			want = Failed
		}
		if r := result(t, report, "proxy_udp"); r.Status != want || hits.Load() != 1 {
			t.Fatalf("%+v datagrams=%d", r, hits.Load())
		}
	}
}

func TestSOCKSAuthRequiredDoesNotSendCredentials(t *testing.T) {
	addr, hits := socksFixture(t, false, true)
	req := Request{TimeoutMS: 200, Binding: binding(), Proxy: &LoopbackProxy{Kind: "socks5", Address: addr}, UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}}
	if r := result(t, (Runner{}).Run(context.Background(), req), "proxy_udp"); r.Status != Failed || hits.Load() != 0 {
		t.Fatal(r)
	}
}

func TestSOCKSTCPActualTunnel(t *testing.T) {
	_, pool, targetDial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ip":"8.8.8.8"}`) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int32
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 3)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		conn.Write([]byte{5, 0})
		buf = make([]byte, 10)
		if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != string([]byte{5, 1, 0, 1, 8, 8, 4, 4, 1, 187}) {
			return
		}
		requests.Add(1)
		target, err := targetDial(context.Background(), "tcp", "")
		if err != nil {
			return
		}
		defer target.Close()
		conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
		go io.Copy(target, conn)
		io.Copy(conn, target)
	}()
	req := Request{Binding: binding(), ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}}, Proxy: &LoopbackProxy{Kind: "socks5", Address: listener.Addr().String()}}
	if r := result(t, (Runner{RootCAs: pool}).Run(context.Background(), req), "proxy_exit"); r.Status != Passed || requests.Load() != 1 {
		t.Fatal(r)
	}
}

func TestSOCKSRejectsRemoteUDPRelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 3)
		io.ReadFull(conn, buf)
		conn.Write([]byte{5, 0})
		buf = make([]byte, 10)
		io.ReadFull(conn, buf)
		conn.Write([]byte{5, 0, 0, 1, 8, 8, 8, 8, 0, 53})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := loopbackDial(LoopbackProxy{Kind: "socks5", Address: listener.Addr().String()})(ctx, "udp", "8.8.4.4:9999")
	if err == nil {
		conn.Close()
		t.Fatal("remote UDP relay accepted")
	}
}

func TestSOCKSUDPStopsWhenAssociationCloses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	relay, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	drop := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 3)
		io.ReadFull(conn, buf)
		conn.Write([]byte{5, 0})
		buf = make([]byte, 10)
		io.ReadFull(conn, buf)
		_, port, _ := net.SplitHostPort(relay.LocalAddr().String())
		response := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
		binary.BigEndian.PutUint16(response[8:], uint16(portNumber(port)))
		conn.Write(response)
		<-drop
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := loopbackDial(LoopbackProxy{Kind: "socks5", Address: listener.Addr().String()})(ctx, "udp", "8.8.4.4:9999")
	if err != nil {
		close(drop)
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	start := time.Now()
	close(drop)
	_, err = conn.Read(make([]byte, 512))
	if err == nil || time.Since(start) > 700*time.Millisecond {
		t.Fatal("UDP connection outlived its TCP association")
	}
}
