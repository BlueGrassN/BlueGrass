package localengine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"bluegrass/native/core/protocolconfig"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/freedom"
)

func xrayFixtureListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func xrayFixtureOutbound(t *testing.T, protocol, entry string) *protocolconfig.Outbound {
	t.Helper()
	c := protocolconfig.Config{Version: 1, Type: protocol}
	switch protocol {
	case "socks5":
		c.SOCKS5 = &protocolconfig.SOCKS5{Username: "review", Password: "review-only"}
	case "http":
		c.HTTP = &protocolconfig.HTTP{Username: "review", Password: "review-only"}
	}
	selected, err := protocolconfig.Build(c, netip.MustParseAddrPort("8.8.8.8:8443"))
	if err != nil {
		t.Fatal(err)
	}
	// Test-only loopback substitution, identical to native_forwarding_test.go.
	var s map[string]any
	if err = json.Unmarshal(*selected.Settings, &s); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(entry)
	n, _ := strconv.Atoi(port)
	s["address"], s["port"] = host, n
	b, _ := json.Marshal(s)
	raw := json.RawMessage(b)
	selected.Settings = &raw
	o, err := protocolconfig.StartOutbound(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func xrayFixtureServer(t *testing.T) string {
	t.Helper()
	l := xrayFixtureListener(t)
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	raw, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "none", "access": "none", "error": "none"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "review", "pass": "review-only"}}, "udp": false, "ip": "127.0.0.1"}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	})
	var c conf.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	pb, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	i, err := core.New(pb)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func TestXrayCONNECTRetainsDelayedReplyAfterClientHalfClose(t *testing.T) {
	for _, halfClose := range []bool{false, true} {
		t.Run(fmt.Sprint(halfClose), func(t *testing.T) {
			target := xrayFixtureListener(t)
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := target.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				payload := make([]byte, 7)
				if _, err = io.ReadFull(c, payload); err != nil {
					return
				}
				if string(payload) != "request" {
					return
				}
				fmt.Fprint(c, "ready")
				// Application reply is delayed after acknowledging a complete request.
				time.Sleep(100 * time.Millisecond)
				fmt.Fprint(c, "delayed-response")
			}()
			o := xrayFixtureOutbound(t, "socks5", xrayFixtureServer(t))
			e := reviewOpen(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
				return o.DialContext(ctx, network, target.Addr().String())
			})
			raw, err := net.Dial("tcp4", e.Address())
			if err != nil {
				t.Fatal(err)
			}
			client := raw.(*net.TCPConn)
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			fmt.Fprint(client, "CONNECT example.invalid:443 HTTP/1.1\r\nHost: example.invalid:443\r\n\r\n")
			reader := bufio.NewReader(client)
			response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("CONNECT failed: %v", err)
			}
			fmt.Fprint(client, "request")
			ready := make([]byte, 5)
			if _, err = io.ReadFull(reader, ready); err != nil || string(ready) != "ready" {
				t.Fatalf("no request acknowledgement: %q %v", ready, err)
			}
			if halfClose {
				if err = client.CloseWrite(); err != nil {
					t.Fatal(err)
				}
			}
			reply := make([]byte, len("delayed-response"))
			n, err := io.ReadFull(reader, reply)
			client.Close()
			<-done
			if err != nil || string(reply) != "delayed-response" {
				t.Fatalf("halfClose=%v got=%q err=%v", halfClose, reply[:n], err)
			}
		})
	}
}
