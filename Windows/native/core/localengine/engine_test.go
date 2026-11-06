package localengine

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestListenerBlocksBusinessBeforePreparation(t *testing.T) {
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	response, err := http.Get("http://" + e.Address() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("closed gate returned %d", response.StatusCode)
	}
	if err := e.SetBusinessGate(true); err == nil {
		t.Fatal("unprepared engine opened")
	}
}

func TestCloseReleasesListenerBeforeServeRegistersIt(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The Serve goroutine created by New may not have run before an early EOF.
	e := &Local{listener: listener, server: &http.Server{}, session: ctx, cancel: cancel, conns: make(map[net.Conn]struct{})}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal("closed engine retained its unregistered listener", err)
	}
	_ = replacement.Close()
}

func TestGateCloseCancelsDialAndRejectsLateConnection(t *testing.T) {
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	entered, resume := make(chan struct{}), make(chan struct{})
	peer, remote := net.Pipe()
	defer peer.Close()
	e.mu.Lock()
	e.observed, e.gate = "1.1.1.1", true
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		close(entered)
		<-resume
		return remote, nil
	}
	e.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := e.businessDial(context.Background(), "tcp", "example.com:443"); done <- err }()
	<-entered
	if err := e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-done; err == nil {
		t.Fatal("late dial escaped closed gate")
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := peer.Read(b[:]); err != io.EOF {
		t.Fatalf("late remote connection not closed: %v", err)
	}
}

func TestClosedGateClosesEstablishedConnections(t *testing.T) {
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	peer, remote := net.Pipe()
	defer peer.Close()
	e.mu.Lock()
	e.observed, e.gate = "1.1.1.1", true
	e.dial = func(context.Context, string, string) (net.Conn, error) { return remote, nil }
	e.mu.Unlock()
	conn, err := e.businessDial(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = e.SetBusinessGate(false); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = peer.Read(b[:]); err != io.EOF {
		t.Fatalf("live connection survived pause: %v", err)
	}
}

func TestBusinessDialKeepsAsyncHandshakeAliveUntilConnectionClose(t *testing.T) {
	e, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	peer, remote := net.Pipe()
	defer peer.Close()
	var flow context.Context
	e.gate = true
	e.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		flow = ctx
		return remote, nil
	}
	conn, err := e.businessDial(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if flow.Err() != nil {
		t.Fatal("async Xray handshake canceled on dial return")
	}
	_ = conn.Close()
	if flow.Err() == nil {
		t.Fatal("closed flow retained its lifetime")
	}
}
