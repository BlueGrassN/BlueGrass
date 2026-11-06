package localengine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func reviewOpen(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) *Local {
	t.Helper()
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	e.mu.Lock()
	e.observed = "1.1.1.1"
	e.gate = true
	e.dial = dial
	e.mu.Unlock()
	return e
}

func TestReviewPauseClosesHTTPUploadClient(t *testing.T) {
	entered := make(chan struct{})
	server, upstream := net.Pipe()
	defer server.Close()
	e := reviewOpen(t, func(context.Context, string, string) (net.Conn, error) { close(entered); return upstream, nil })
	client, err := net.Dial("tcp4", e.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = fmt.Fprint(client, "POST http://example.com/upload HTTP/1.1\r\nHost: example.com\r\nContent-Length: 1024\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no dial")
	}
	if err = e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	data, err := io.ReadAll(client)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("HTTP client still open after Pause; received %d bytes, err=%v", len(data), err)
	}
}

func TestReviewPauseClosesCONNECTClient(t *testing.T) {
	server, upstream := net.Pipe()
	defer server.Close()
	e := reviewOpen(t, func(context.Context, string, string) (net.Conn, error) { return upstream, nil })
	client, err := net.Dial("tcp4", e.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	e.SetBusinessGate(false)
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, err = reader.ReadByte()
	if err != io.EOF {
		t.Fatalf("CONNECT survived pause: %v", err)
	}
}

func TestReviewUpstreamFailureNeverFallsBack(t *testing.T) {
	e := reviewOpen(t, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fake failure") })
	client, err := net.Dial("tcp4", e.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	fmt.Fprint(client, "GET http://invalid.example/ HTTP/1.1\r\nHost: invalid.example\r\n\r\n")
	client.SetReadDeadline(time.Now().Add(time.Second))
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 502 {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestReviewCONNECTPreservesHalfCloseResponse(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.ReadAll(conn)
		fmt.Fprint(conn, "reply-after-eof")
	}()
	e := reviewOpen(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
	})
	raw, err := net.Dial("tcp4", e.Address())
	if err != nil {
		t.Fatal(err)
	}
	client := raw.(*net.TCPConn)
	defer client.Close()
	fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	fmt.Fprint(client, "request")
	client.CloseWrite()
	client.SetReadDeadline(time.Now().Add(time.Second))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	<-ended
	if string(result) != "reply-after-eof" {
		t.Fatalf("upstream response lost after client half-close: got %q", result)
	}
}
