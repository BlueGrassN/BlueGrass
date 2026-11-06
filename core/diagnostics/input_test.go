package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestCustomDNSServerCountBounds(t *testing.T) {
	for _, count := range []int{0, 1, 3, 4, 8, 9} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			servers := make([]DNSServer, count)
			for i := range servers {
				servers[i] = DNSServer{Address: "127.0.0.1:" + strconv.Itoa(5300+i), Network: "udp"}
			}
			req := Request{Entry: &Entry{Host: "entry.invalid", Port: "443", DNSMode: "custom", DNSServers: servers}}
			wantValid := count >= 1 && count <= 8
			if valid := ValidateRequest(req) == nil; valid != wantValid {
				t.Errorf("package validation for %d DNS candidates: valid=%v, want %v", count, valid, wantValid)
			}
			data, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeRequest(bytes.NewReader(data))
			if (err == nil) != wantValid {
				t.Fatalf("stdin validation for %d DNS candidates: %v", count, err)
			}
			if wantValid {
				if len(decoded.Entry.DNSServers) != count {
					t.Fatal("DNS candidates were truncated")
				}
				for i, server := range decoded.Entry.DNSServers {
					if server != servers[i] {
						t.Fatal("DNS candidate order changed")
					}
				}
			} else {
				r := result(t, (Runner{}).Run(context.Background(), req), "entry_dns")
				if r.Status != Skipped || r.Code != "invalid_request" {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestRejectUnsafeInputBeforeNetwork(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{} {}`, `{"timeout_ms":0,"timeout_ms":100}`, `{"binding":{"connected":true,"connected":false}}`,
		`{"api_base_url":"http://example.com"}`, `{"api_base_url":"https://example.com?token=secret"}`,
		`{"api_base_url":"https://u:p@example.com"}`, `{"proxy":{"kind":"http_connect","address":"8.8.8.8:80"}}`,
		`{"proxy":{"kind":"socks5","address":"localhost:1234"}}`, `{"proxy":{"kind":"socks5","address":"127.0.0.1:1234","password":"secret"}}`,
		`{"timeout_ms":999999}`, `{"timeout_ms":1}`,
		`{"exit_endpoints":[{"url":"https://example.com","ipv4":"192.0.2.1"}]}`,
		`{"entry":{"host":"entry.invalid","port":"443","dns_mode":"custom"}}`,
		`{"entry":{"host":"entry.invalid","port":"443","dns_mode":"custom","dns_servers":[{"address":"dns.example.com:53","network":"udp"}]}}`,
		`{"udp":{"address":"8.8.8.8:53","protocol":"dns"}}`, strings.Repeat("x", MaxInputBytes+1),
	} {
		if _, err := DecodeRequest(strings.NewReader(input)); err == nil || err.Error() != "invalid_request" {
			t.Errorf("accepted unsafe input or reflected input: %v", err)
		}
	}
	runner := Runner{DirectDial: func(context.Context, string, string) (net.Conn, error) {
		t.Error("invalid request performed network I/O")
		return nil, io.EOF
	}}
	report := runner.Run(context.Background(), Request{APIBaseURL: "https://u:p@example.com"})
	for _, r := range report.Results {
		if r.Status != Skipped {
			t.Fatal(r)
		}
	}
}

func TestRequestDecodesDocumentedShape(t *testing.T) {
	input := `{"api_base_url":"https://client-api.sunip.vip/api/client/v1","binding":{"connected":true,"generation":42,"resource_id":"opaque-id"},"entry":{"host":"entry.example.com","port":"443","dns_mode":"custom","dns_servers":[{"address":"1.1.1.1:53","network":"tcp"}]},"proxy":{"kind":"socks5","address":"127.0.0.1:1080"},"expected_exit_ip":"8.8.8.8","exit_endpoints":[{"url":"https://example.com?format=json","ipv4":"1.1.1.1"}],"udp":{"address":"8.8.4.4:9999","protocol":"echo_nonce_v1"},"timeout_ms":500}`
	request, err := DecodeRequest(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if request.Binding.Generation != 42 || request.Entry.DNSServers[0].Network != "tcp" {
		t.Fatal("request fields discarded")
	}
}
