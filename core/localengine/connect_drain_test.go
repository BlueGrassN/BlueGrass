package localengine

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/tunengine"
)

func drainTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	return client, server
}

type prefetchedHijacker struct {
	*httptest.ResponseRecorder
	conn   net.Conn
	buffer *bufio.ReadWriter
}

func (w *prefetchedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.buffer, nil
}

func connectFixture(t *testing.T, prefetch string) (*Local, *net.TCPConn, *net.TCPConn, *bufio.Reader, <-chan struct{}) {
	t.Helper()
	client, ingress := drainTCPPair(t)
	out, server := drainTCPPair(t)
	e := reviewOpen(t, func(context.Context, string, string) (net.Conn, error) { return out, nil })
	reader := bufio.NewReader(io.MultiReader(strings.NewReader(prefetch), ingress))
	if _, err := reader.Peek(len(prefetch)); err != nil {
		t.Fatal(err)
	}
	w := &prefetchedHijacker{httptest.NewRecorder(), ingress, bufio.NewReadWriter(reader, bufio.NewWriter(ingress))}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.connect(w, &http.Request{Method: http.MethodConnect, Host: "example.com:443"})
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("CONNECT handler did not stop after socket cleanup")
		}
	})
	reply := bufio.NewReader(client)
	response, err := http.ReadResponse(reply, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT handshake: %v, %v", response, err)
	}
	return e, client, server, reply, done
}

func TestCONNECTPrefetchedPayloadSurvivesHalfClose(t *testing.T) {
	_, client, server, reply, done := connectFixture(t, "prefetched-")
	if _, err := client.Write([]byte("socket")); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(server)
	if err != nil || string(payload) != "prefetched-socket" {
		t.Fatalf("hijacked payload: %q, %v", payload, err)
	}
	if _, err := server.Write([]byte("reply-after-fin")); err != nil {
		t.Fatal(err)
	}
	_ = server.CloseWrite()
	payload, err = io.ReadAll(reply)
	if err != nil || string(payload) != "reply-after-fin" {
		t.Fatalf("downstream after FIN: %q, %v", payload, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completed CONNECT retained its handler")
	}
}

func TestCONNECTUpstreamEOFClosesIdleClient(t *testing.T) {
	e, _, server, reply, done := connectFixture(t, "")
	if _, err := server.Write([]byte("finished")); err != nil {
		t.Fatal(err)
	}
	_ = server.CloseWrite()
	payload, err := io.ReadAll(reply)
	if err != nil || string(payload) != "finished" {
		t.Fatalf("downstream: %q, %v", payload, err)
	}
	// The client deliberately leaves upload open after receiving server EOF.
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("upstream EOF retained an idle CONNECT upload and its sockets")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.conns) != 0 {
		t.Fatal("completed CONNECT retained tracked sockets")
	}
}

func TestCONNECTRelayRawTCPBoundsIdleAndResetsOnActivity(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "silent-peer"
		if active {
			name = "active-downstream"
		}
		t.Run(name, func(t *testing.T) {
			client, ingress := drainTCPPair(t)
			out, server := drainTCPPair(t)
			done := make(chan error, 1)
			go func() {
				done <- tunengine.RelayHalfClosedTCP(
					&hijackedConn{Conn: ingress, reader: bufio.NewReader(ingress)}, out, 300*time.Millisecond)
			}()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			if _, err := client.Write([]byte("request")); err != nil {
				t.Fatal(err)
			}
			if err := client.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			payload, err := io.ReadAll(server)
			if err != nil || string(payload) != "request" {
				t.Fatalf("raw TCP upload/FIN: %q, %v", payload, err)
			}
			if active {
				// Stay active longer than one drain interval; each real reply must
				// extend it instead of being cut off by a fixed post-FIN deadline.
				for range 5 {
					time.Sleep(100 * time.Millisecond)
					if _, err := server.Write([]byte("reply")); err != nil {
						t.Fatal(err)
					}
					payload := make([]byte, 5)
					if _, err := io.ReadFull(client, payload); err != nil || string(payload) != "reply" {
						t.Fatalf("active reply lost after upload FIN: %q, %v", payload, err)
					}
				}
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("raw TCP connection survived the idle drain")
			}
			if payload, err := io.ReadAll(client); err != nil || len(payload) != 0 {
				t.Fatalf("idle drain did not close the client: %q, %v", payload, err)
			}
		})
	}
}
