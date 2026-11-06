//go:build windows && amd64

package winservice

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestAcceptanceSTUNRetransmitsLostDatagram(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	valid := make(chan bool, 1)
	go func() {
		first, second := make([]byte, 64), make([]byte, 64)
		n, _, err := server.ReadFrom(first)
		if err != nil {
			valid <- false
			return
		}
		m, peer, err := server.ReadFrom(second)
		if err != nil || n != 20 || m != 20 || !bytes.Equal(first[:n], second[:m]) {
			valid <- false
			return
		}
		reply := make([]byte, 32)
		copy(reply, second[:20])
		binary.BigEndian.PutUint16(reply, 0x0101)
		binary.BigEndian.PutUint16(reply[2:], 12)
		binary.BigEndian.PutUint16(reply[20:], 0x0020)
		binary.BigEndian.PutUint16(reply[22:], 8)
		reply[25] = 1
		binary.BigEndian.PutUint16(reply[26:], 3478^0x2112)
		for i := 0; i < 4; i++ {
			reply[28+i] = 8 ^ reply[4+i]
		}
		_, err = server.WriteTo(reply, peer)
		valid <- err == nil
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result := acceptanceSTUNVia(ctx, acceptanceProbeRequest{Mode: "stun", Target: "1.1.1.1:3478"}, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp4", server.LocalAddr().String())
	})
	if !result.Success || result.IP != "8.8.8.8" || !<-valid {
		t.Fatal("STUN retransmission did not preserve and complete its transaction")
	}
}
