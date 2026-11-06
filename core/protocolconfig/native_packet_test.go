package protocolconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/socks"
)

func TestNativeSOCKSUDPFramingBoundary(t *testing.T) {
	target := nativeEchoUDP(t)
	out := nativeSOCKSOutbound(t)
	p, err := out.ListenPacket(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	addr, _ := net.ResolveUDPAddr("udp", target)
	_ = p.SetDeadline(time.Now().Add(3 * time.Second))
	if n, err := p.WriteTo(nil, addr); n != 0 || !errors.Is(err, errPacketEmpty) {
		t.Fatalf("empty datagram silently accepted: n=%d err=%v", n, err)
	}
	for _, size := range []int{8183, 8192} {
		if n, err := p.WriteTo(make([]byte, size), addr); n != 0 || !errors.Is(err, errPacketSize) {
			t.Errorf("oversized payload %d reported n=%d err=%v", size, n, err)
		}
	}
	// Rejection must not poison the flow, and the largest admitted packet must
	// arrive intact through the actual pinned SOCKS encoder and decoder.
	want := bytes.Repeat([]byte{17}, 8182)
	if n, err := p.WriteTo(want, addr); err != nil || n != len(want) {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	b := make([]byte, 8192)
	n, _, err := p.ReadFrom(b)
	if err != nil || !bytes.Equal(b[:n], want) {
		t.Fatalf("echo n=%d err=%v", n, err)
	}
}

func TestNativeSOCKSAddressFramingUsesActualCodec(t *testing.T) {
	for _, tc := range []struct {
		address string
		limit   int
	}{
		{"127.0.0.1:53", 8182},
		{"[::1]:53", 8170},
		{"fixture.invalid:53", 8170}, // 3 + ATYP + domain length + 15 bytes + port
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ":53", 7932},
	} {
		t.Run(tc.address, func(t *testing.T) {
			dest, err := nativeDestination("udp", tc.address)
			if err != nil {
				t.Fatal(err)
			}
			limit := (&Outbound{packetProtocol: "socks"}).packetLimit(dest)
			if limit != tc.limit {
				t.Fatalf("payload limit=%d want=%d", limit, tc.limit)
			}
			packet := &nativePacket{maxPayload: limit}
			if n, err := packet.WriteTo(make([]byte, limit+1), nativeAddr{"udp", tc.address}); n != 0 || !errors.Is(err, errPacketSize) {
				t.Fatalf("oversize admission n=%d err=%v", n, err)
			}
			request := &protocol.RequestHeader{Address: dest.Address, Port: dest.Port}
			encoded, err := socks.EncodeUDPPacket(request, bytes.Repeat([]byte{19}, limit))
			if err != nil {
				t.Fatal(err)
			}
			defer encoded.Release()
			decoded, err := socks.DecodeUDPPacket(encoded)
			if err != nil || encoded.Len() != int32(limit) || decoded.Address.String() != dest.Address.String() {
				t.Fatalf("codec round trip length=%d err=%v", encoded.Len(), err)
			}
		})
	}
}

func TestNativeShadowsocksUDPFramingBoundary(t *testing.T) {
	for _, tc := range []struct {
		method string
		limit  int
	}{
		{"aes-128-gcm", 8153},
		{"aes-256-gcm", 8137},
		{"2022-blake3-aes-128-gcm", 8134},
		{"2022-blake3-chacha20-poly1305", 8110},
	} {
		t.Run(tc.method, func(t *testing.T) {
			password := "fixture-password"
			if strings.HasPrefix(tc.method, "2022-") {
				keyLen := 32
				if tc.method == "2022-blake3-aes-128-gcm" {
					keyLen = 16
				}
				password = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, keyLen))
			}
			target := nativeEchoUDP(t)
			entry := nativeXrayServer(t, "shadowsocks", map[string]any{"method": tc.method, "password": password, "network": "tcp,udp"})
			host, portString, _ := net.SplitHostPort(entry)
			port, _ := strconv.Atoi(portString)
			out := nativeFixtureOutbound(t, context.Background(), "shadowsocks", map[string]any{"address": host, "port": port, "method": tc.method, "password": password})
			p, err := out.ListenPacket(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			_ = p.SetDeadline(time.Now().Add(2 * time.Second))
			addr, _ := net.ResolveUDPAddr("udp", target)
			if n, err := p.WriteTo(make([]byte, tc.limit+1), addr); n != 0 || !errors.Is(err, errPacketSize) {
				t.Fatalf("oversize n=%d err=%v", n, err)
			}
			payload := bytes.Repeat([]byte{29}, tc.limit)
			if n, err := p.WriteTo(payload, addr); n != len(payload) || err != nil {
				t.Fatalf("boundary n=%d err=%v", n, err)
			}
			got := make([]byte, 8192)
			n, _, err := p.ReadFrom(got)
			if err != nil || !bytes.Equal(got[:n], payload) {
				t.Fatalf("boundary echo n=%d err=%v", n, err)
			}
		})
	}
}
