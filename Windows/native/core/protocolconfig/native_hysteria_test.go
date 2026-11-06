//go:build windows

package protocolconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

func nativeHysteriaOutbound(t *testing.T) *Outbound {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := &key.PublicKey
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "review.invalid"}, DNSNames: []string{"review.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certLines := strings.Split(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), "\n")
	keyLines := strings.Split(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})), "\n")
	u, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := u.LocalAddr().(*net.UDPAddr).Port
	u.Close()
	raw, _ := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none", "access": "none", "error": "none"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "hysteria",
			"settings":       map[string]any{"version": 2, "clients": []any{map[string]any{"auth": "review-only"}}},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "hysteriaSettings": map[string]any{"version": 2}, "tlsSettings": map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificate": certLines, "key": keyLines}}}}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	})
	var c conf.Config
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	pb, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := core.New(pb)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	config := Config{Version: 1, Type: "hysteria2", TLS: &TLS{ServerName: "review.invalid"}, Hysteria2: &Hysteria2{Password: "review-only"}}
	selected, err := Build(config, netip.MustParseAddrPort("8.8.8.8:443"))
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{"version": 2, "address": "127.0.0.1", "port": port})
	j := json.RawMessage(settings)
	selected.Settings = &j
	// In-memory fixture certificate only; do not change system trust or production settings.
	selected.StreamSetting.TLSSettings.Certs = []*conf.TLSCertConfig{{CertStr: certLines, Usage: "verify"}}
	selected.StreamSetting.TLSSettings.DisableSystemRoot = true
	o, err := StartOutbound(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { o.Close() })
	return o
}

func TestNativeHysteriaCreatorClosePreservesOtherStream(t *testing.T) {
	target := nativeEchoTCP(t)
	out := nativeHysteriaOutbound(t)
	var conns []net.Conn
	echo := func(c net.Conn) {
		t.Helper()
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.WriteString(c, "ping"); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 4)
		if _, err := io.ReadFull(c, got); err != nil || string(got) != "ping" {
			t.Fatalf("Hysteria echo=%q err=%v", got, err)
		}
	}
	for range 2 {
		c, err := out.DialContext(context.Background(), "tcp", target)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
		echo(c)
	}
	if err := conns[0].Close(); err != nil {
		t.Fatal(err)
	}
	// Give an erroneously flow-owned AfterFunc time to close the shared socket.
	time.Sleep(50 * time.Millisecond)
	echo(conns[1])
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conns[1].Read(make([]byte, 1)); err == nil || err == io.EOF {
		t.Fatalf("session close did not abort live stream: %v", err)
	}
	if c, err := out.DialContext(context.Background(), "tcp", target); c != nil || err == nil {
		t.Fatal("closed session admitted a new stream")
	}
}

func TestNativeSessionMarkerRetiresPhysicalUDP(t *testing.T) {
	target := nativeEchoUDP(t)
	out := nativeFixtureOutbound(t, context.Background(), "freedom", map[string]any{})
	owner, ok := out.ctx.Value(outboundLifetimeKey{}).(context.Context)
	if !ok {
		t.Fatal("missing outbound lifetime marker")
	}
	flow, cancel := context.WithCancel(out.ctx)
	defer cancel()
	destination, err := xnet.ParseDestination("udp:" + target)
	if err != nil {
		t.Fatal(err)
	}
	d := &lifetimeSystemDialer{}
	raw, err := d.Dial(flow, nil, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, ok := raw.(*xnet.PacketConnWrapper); !ok {
		t.Fatal("physical UDP wrapper type changed")
	}
	cancel()
	time.Sleep(30 * time.Millisecond)
	if owner.Err() != nil {
		t.Fatal("creator canceled the owning session")
	}
	_ = raw.SetDeadline(time.Now().Add(time.Second))
	if _, err = raw.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 16)
	if n, err := raw.Read(reply); err != nil || string(reply[:n]) != "alive" {
		t.Fatalf("shared physical UDP closed with flow: %v", err)
	}
	_ = raw.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	var readErr error
	go func() { defer close(done); _, readErr = raw.Read(reply) }()
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	nativeAwait(t, done, "physical session UDP retirement")
	if !errors.Is(readErr, net.ErrClosed) || owner.Err() == nil {
		t.Fatalf("physical socket/session not closed: %v", readErr)
	}
}
