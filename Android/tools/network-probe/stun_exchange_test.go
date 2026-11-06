package main

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func stunTestSocket(t *testing.T) (*net.UDPConn, net.Conn) {
	t.Helper()
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	client, err := net.Dial("udp4", server.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client
}

func TestSTUNRetrySameTransactionAndSocket(t *testing.T) {
	server, client := stunTestSocket(t)
	response := decodeHex(t, validResponseHex)
	requests := make(chan [][]byte, 1)
	go func() {
		_ = server.SetDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 256)
		n, firstPeer, err := server.ReadFromUDP(buf)
		if err != nil {
			requests <- nil
			return
		}
		first := bytes.Clone(buf[:n])
		// Drop the first request. A retry must reuse both bytes and source port.
		n, peer, err := server.ReadFromUDP(buf)
		if err != nil || peer.String() != firstPeer.String() {
			requests <- nil
			return
		}
		second := bytes.Clone(buf[:n])
		wrong := bytes.Clone(response)
		wrong[8] ^= 0xff
		_, _ = server.WriteToUDP(wrong, peer)
		_, _ = server.WriteToUDP([]byte{1, 2, 3}, peer)
		_, _ = server.WriteToUDP(response, peer)
		requests <- [][]byte{first, second}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mapping, attempts, code := exchangeSTUN(ctx, client, testTransaction)
	if code != "" || mapping.String() != "192.0.2.1:32853" || attempts != 2 {
		t.Fatalf("mapping = %v, attempts = %d, code = %q", mapping, attempts, code)
	}
	got := <-requests
	if len(got) != 2 || !bytes.Equal(got[0], got[1]) || !bytes.Equal(got[0], decodeHex(t, "000100002112a442000102030405060708090a0b")) {
		t.Fatalf("retry changed transaction or socket: %x", got)
	}
}

func TestSTUNDeadline(t *testing.T) {
	_, client := stunTestSocket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	mapping, attempts, code := exchangeSTUN(ctx, client, testTransaction)
	if mapping.IsValid() || attempts != 1 || code != "timeout" {
		t.Fatalf("mapping = %v, attempts = %d, code = %q", mapping, attempts, code)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline did not bound receive/retry")
	}
}
