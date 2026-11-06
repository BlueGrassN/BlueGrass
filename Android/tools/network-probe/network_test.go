package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDNSUsesChosenServerAndAOnly(t *testing.T) {
	// A local DNS server makes an external resolver fallback observable as failure.
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	queries := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 512)
		for {
			n, peer, err := server.ReadFromUDP(buf)
			if err != nil {
				return
			}
			query := bytes.Clone(buf[:n])
			if n < 12 {
				continue
			}
			offset := 12
			for offset < n && query[offset] != 0 {
				offset += int(query[offset]) + 1
			}
			end := offset + 5
			if end > n {
				continue
			}
			queries <- bytes.Clone(query[12:end])
			response := bytes.Clone(query[:end])
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 1)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			// A 192.0.2.77, TTL 60, compressed name pointing at the question.
			response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 77)
			_, _ = server.WriteToUDP(response, peer)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := (explicitResolver{server: server.LocalAddr().String()}).lookup(ctx, "network-probe.invalid")
	if err != nil || len(ips) != 1 || ips[0].String() != "192.0.2.77" {
		t.Fatalf("lookup = %v, err = %v", ips, err)
	}
	select {
	case query := <-queries:
		want := append([]byte{13}, []byte("network-probe")...)
		want = append(want, 7)
		want = append(want, []byte("invalid")...)
		want = append(want, 0, 0, 1, 0, 1)
		if !bytes.Equal(query, want) {
			t.Fatalf("non-A or unexpected DNS question: %x", query)
		}
	case <-ctx.Done():
		t.Fatal("chosen DNS server was not queried")
	}
}

func TestDNSFailureCannotUseSystemResolver(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close() // Keep it open and silent until the resolver deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	ips, err := (explicitResolver{server: server.LocalAddr().String()}).lookup(ctx, "example.com")
	if len(ips) != 0 || errorCode(err, "unexpected") != "timeout" {
		t.Fatalf("lookup = %v, err = %v", ips, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("DNS deadline not respected")
	}
}

func TestHTTPSRequiresTrustAndLoadsAndroidPEM(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"ip":"8.8.4.4"}`) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	ctx := context.Background()
	empty := x509.NewCertPool()
	bad := getHTTPS(ctx, explicitResolver{}, empty, server.URL, true)
	if bad.OK || bad.Error != "tls_verify" {
		t.Fatalf("untrusted certificate accepted: %+v", bad)
	}
	dir := t.TempDir()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "01234567.0"), cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.0"), []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	loaded, code := appendAndroidRoots(ctx, pool, dir)
	if loaded != 1 || code != "android_ca_partial" {
		t.Fatalf("loaded %d, code %q", loaded, code)
	}
	// Poison every proxy environment variable. The trusted request must remain direct.
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	good := getHTTPS(ctx, explicitResolver{}, pool, server.URL, true)
	if !good.OK || good.PublicIPv4 != "8.8.4.4" {
		t.Fatalf("loaded certificate not used: %+v", good)
	}
	client, transport := newHTTPClient(explicitResolver{}, pool)
	defer transport.CloseIdleConnections()
	if transport.Proxy != nil {
		t.Fatal("environment proxy hook enabled")
	}
	// A trusted issuer must not disable hostname verification.
	transport.TLSClientConfig.ServerName = "wrong.invalid"
	response, err := client.Get(server.URL)
	if response != nil {
		response.Body.Close()
	}
	if errorCode(err, "unexpected") != "tls_verify" {
		t.Fatalf("wrong hostname accepted: %v", err)
	}
}

func TestHTTPSRejectsRedirectMalformedAndOversizedBody(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"redirect", "", "http_status", 302},
		{"malformed", `{"ip":`, "https_body", 200},
		{"trailing_json", `{"ip":"8.8.4.4"}{}`, "https_body", 200},
		{"oversized", strings.Repeat("x", 1025), "https_body", 200},
		{"private", `{"ip":"10.1.2.3"}`, "invalid_public_ipv4", 200},
		{"ipv6", `{"ip":"2001:4860:4860::8888"}`, "invalid_public_ipv4", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://must-not-follow.invalid")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			got := getHTTPS(context.Background(), explicitResolver{}, pool, server.URL, true)
			if got.OK || got.Error != tc.code {
				t.Fatalf("result: %+v", got)
			}
		})
	}
}

func TestHTTPSBodyDeadlineAndPlainAlternate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "8.8.4.4\n")
	}))
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	got := getHTTPS(context.Background(), explicitResolver{}, pool, server.URL, false)
	if !got.OK || got.PublicIPv4 != "8.8.4.4" {
		t.Fatalf("plain alternate: %+v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	got = getHTTPS(ctx, explicitResolver{}, pool, server.URL+"/slow", false)
	if got.OK || got.Error != "timeout" || time.Since(start) > time.Second {
		t.Fatalf("unbounded body: %+v", got)
	}
}

func TestPublicIPv4(t *testing.T) {
	for _, value := range []string{"8.8.4.4", "1.1.1.1"} {
		if !isPublicIPv4(netip.MustParseAddr(value)) {
			t.Errorf("rejected public %s", value)
		}
	}
	for _, value := range []string{"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.64.0.1", "127.0.0.1", "169.254.1.1", "172.19.0.2", "192.168.1.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "::1", "::ffff:8.8.4.4"} {
		if isPublicIPv4(netip.MustParseAddr(value)) {
			t.Errorf("accepted non-public %s", value)
		}
	}
}

func TestArgumentsAndSanitizedReport(t *testing.T) {
	for _, value := range []string{"10.0.2.3:53", "172.19.0.2:53"} {
		got, ok := parseArgs([]string{"--dns-server", value})
		if !ok || got.DNSServer != value || got.RoutingCheck {
			t.Fatalf("rejected literal %q", value)
		}
	}
	for _, args := range [][]string{nil, {"--help"}, {"--dns-server", "dns.google:53"}, {"--dns-server", "10.0.2.3:54"}, {"--dns-server", "[::1]:53"}, {"--dns-server", "0.0.0.0:53"}, {"--dns-server", "user:private-input@example.com:53"}, {"--dns-server", "10.0.2.3:53", "extra"}} {
		r, code := run(args)
		if code != 2 || r.Error != "invalid_arguments" || r.DNSServer != "" || len(r.HTTPS.Attempts) != 0 {
			t.Fatalf("invalid args report: %+v, %d", r, code)
		}
		body, err := json.Marshal(r)
		if err != nil || strings.Contains(string(body), "private-input") {
			t.Fatalf("unsanitized report: %s", body)
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			t.Fatal(err)
		}
		dns := object["dns"].(map[string]any)
		if dns["ok"] != false || dns["error"] != "not_run" {
			t.Fatalf("missing embedded status: %s", body)
		}
	}
}
