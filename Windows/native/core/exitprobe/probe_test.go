package exitprobe

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type wireReply struct {
	status         int
	body, location string
	stall          bool
}

// The actual HTTP transport performs TLS over in-memory pipes. Only the
// physical dial and test root trust are supplied by the fixture; no listener,
// public DNS, remote proxy or real endpoint is used.
type httpsFixture struct {
	mu          sync.Mutex
	calls       []string
	requests    []*http.Request
	serverNames []string
	replies     map[string]wireReply
	certificate tls.Certificate
	roots       *x509.CertPool
}

func newHTTPSFixture(t *testing.T, replies map[string]wireReply) *httpsFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"api.ipify.org", "icanhazip.com", "checkip.amazonaws.com"},
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &httpsFixture{replies: replies, certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots: roots}
}

func (f *httpsFixture) dial(_ context.Context, network, address string) (net.Conn, error) {
	f.mu.Lock()
	f.calls = append(f.calls, address)
	reply, exists := f.replies[address]
	f.mu.Unlock()
	if network != "tcp" {
		return nil, errors.New("fixture non-TCP")
	}
	if !exists {
		return nil, errors.New("fixture network failure with sensitive detail")
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		// Like a real accepted socket, this server's lifetime is independent of
		// the client's completed DialContext. Closing the client unblocks it.
		conn := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{f.certificate}, MinVersion: tls.VersionTLS12})
		if err := conn.Handshake(); err != nil {
			return
		}
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, request)
		f.serverNames = append(f.serverNames, conn.ConnectionState().ServerName)
		f.mu.Unlock()
		if reply.stall {
			_, _ = io.Copy(io.Discard, conn)
			return
		}
		status := reply.status
		if status == 0 {
			status = 200
		}
		header := make(http.Header)
		if reply.location != "" {
			header.Set("Location", reply.location)
		}
		response := http.Response{StatusCode: status, Status: fmt.Sprintf("%d fixture", status), ProtoMajor: 1, ProtoMinor: 1,
			Header: header, Body: io.NopCloser(strings.NewReader(reply.body)), ContentLength: int64(len(reply.body)), Close: true}
		_ = response.Write(conn)
	}()
	return client, nil
}

func (f *httpsFixture) run(ctx context.Context, options Options, budget probeBudget) (string, error) {
	transport := newTransport(f.dial)
	transport.TLSClientConfig.RootCAs = f.roots // private test seam, never a public option
	return probe(ctx, transport, options, budget)
}

func TestProbeFirstValidIPv4ObservationStopsFallback(t *testing.T) {
	for _, body := range []string{`{"ip":"9.9.9.9"}`, `{"ip":"8.8.4.4"}`} {
		f := newHTTPSFixture(t, map[string]wireReply{
			"api.ipify.org:443": {body: body},
			"icanhazip.com:443": {body: "1.1.1.1\n"},
		})
		got, err := f.run(context.Background(), Options{}, defaultBudget)
		if err != nil || got == "" || got == "1.1.1.1" || len(f.calls) != 1 {
			t.Fatal("valid evidence was replaced by fallback", got, err)
		}
	}
}

func TestProbeFallbackOrderFormatsAndNoRedirect(t *testing.T) {
	for _, first := range []wireReply{{body: `{"ip":"9.9.9.9","ip":"1.1.1.1"}`},
		{body: strings.Repeat("x", 4097)}, {status: 302, location: "https://untrusted.example/"}, {status: 500}, {body: `{"ip":"192.168.1.1"}`}} {
		f := newHTTPSFixture(t, map[string]wireReply{
			"api.ipify.org:443":         first,
			"icanhazip.com:443":         {body: "not an address\n"},
			"checkip.amazonaws.com:443": {body: "8.8.4.4\r\n"},
		})
		got, err := f.run(context.Background(), Options{}, defaultBudget)
		if err != nil || got != "8.8.4.4" {
			t.Fatal(got, err)
		}
		if strings.Join(f.calls, ",") != "api.ipify.org:443,icanhazip.com:443,checkip.amazonaws.com:443" {
			t.Fatal("wrong/redirected dial sequence", f.calls)
		}
		for i, request := range f.requests {
			if request.Host != f.serverNames[i] || request.Method != "GET" || request.Header.Get("Authorization") != "" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatal("hostname/SNI or credential boundary lost")
			}
		}
		if f.requests[0].URL.RequestURI() != "/?format=json" || f.requests[1].URL.RequestURI() != "/" || f.requests[2].URL.RequestURI() != "/" {
			t.Fatal("endpoint URLs changed")
		}
	}
}

func TestIPv4OnlyProbeSkipsIPv6JSONAndPlainResponses(t *testing.T) {
	f := newHTTPSFixture(t, map[string]wireReply{"api.ipify.org:443": {body: `{"ip":"2606:4700::1111"}`}, "icanhazip.com:443": {body: "2606:4700:4700::1111\n"}, "checkip.amazonaws.com:443": {body: "1.1.1.1\n"}})
	got, err := f.run(context.Background(), Options{}, defaultBudget)
	if err != nil || got != "1.1.1.1" || len(f.calls) != 3 {
		t.Fatal("IPv6 prevented fallback to a displayable IPv4", got, err)
	}
}

func TestProbeCountryCompatibilityIsExplicitAndJSONStrict(t *testing.T) {
	for _, allow := range []bool{false, true} {
		for _, value := range []string{`{"ip":"9.9.9.9","ip":"8.8.8.8"}`, `{"ip":"9.9.9.9"} {}`, `{"ip":null}`, `{"ip":1}`, `[]`,
			`{"ip":"127.0.0.1"}`, `{"ip":"198.18.1.1"}`, `{"ip":"::1"}`, `{"ip":"fd00::1"}`, `{"ip":"::ffff:8.8.8.8"}`,
			`{"ip":"9.9.9.9","extra":true}`, `{"ip":"9.9.9.9","country":null}`, `{"ip":"9.9.9.9","country":"ZZ","country":"XX"}`, strings.Repeat(" ", 4097)} {
			if _, err := parseJSON([]byte(value), allow); err == nil {
				t.Errorf("ambiguous/invalid JSON accepted (%d bytes)", len(value))
			}
		}
		_, err := parseJSON([]byte(`{"ip":"1.1.1.1","country":"ZZ"}`), allow)
		if (err == nil) != allow {
			t.Fatal("country compatibility leaked between engines")
		}
		if got, err := parseJSON([]byte(`{"ip":"1.1.1.1"}`), allow); err != nil || got != "1.1.1.1" {
			t.Fatal("valid JSON rejected")
		}
	}
}

func TestProbePlainResponseRequiresOneBoundedIPAddress(t *testing.T) {
	for _, value := range []string{"", "8.8.8.8\n1.1.1.1", `{"ip":"8.8.8.8"}`, "IP: 8.8.8.8", "8.8.8.8\x00", "127.0.0.1", "::ffff:8.8.8.8", strings.Repeat(" ", 4097)} {
		if _, err := parsePlain([]byte(value)); err == nil {
			t.Fatal("invalid plain address accepted")
		}
	}
	if got, err := parsePlain([]byte(" \t8.8.8.8\r\n")); err != nil || got != "8.8.8.8" {
		t.Fatal("plain service newline rejected")
	}
}

func TestProbeTLSVerificationEnvironmentProxyAndAllowlist(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	f := newHTTPSFixture(t, map[string]wireReply{"api.ipify.org:443": {body: `{"ip":"8.8.8.8"}`}})
	transport := newTransport(f.dial)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.RootCAs != nil || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("unsafe production TLS/proxy defaults")
	}
	// No test root here: the actual production helper must reject our untrusted
	// certificate while still dialing only the three allowlisted selected paths.
	if got, err := Probe(context.Background(), f.dial, Options{}); err == nil || got != "" {
		t.Fatal("untrusted TLS proof accepted")
	}
	if len(f.calls) != 3 || len(f.requests) != 0 {
		t.Fatal("TLS failure escaped selected dial/allowlist")
	}
	for _, target := range []string{"api.ipify.org:80", "api.ipify.org.evil:443", "8.8.8.8:443", "API.IPIFY.ORG:443", "api.ipify.org.:443", "user@api.ipify.org:443"} {
		if _, ok := AllowedHost("tcp", target); ok {
			t.Fatal("untrusted probe target accepted", target)
		}
	}
	if _, ok := AllowedHost("udp", "api.ipify.org:443"); ok {
		t.Fatal("UDP probe target accepted")
	}
}

func TestProbePerEndpointAndTotalBudgetCancellation(t *testing.T) {
	if defaultBudget.total != 20*time.Second || defaultBudget.attempt <= 0 || defaultBudget.attempt >= defaultBudget.total {
		t.Fatal("production budget changed")
	}
	f := newHTTPSFixture(t, map[string]wireReply{"api.ipify.org:443": {stall: true}, "icanhazip.com:443": {body: "8.8.8.8\n"}})
	got, err := f.run(context.Background(), Options{}, probeBudget{total: time.Second, attempt: 100 * time.Millisecond})
	if err != nil || got != "8.8.8.8" || len(f.calls) != 2 {
		t.Fatal("endpoint timeout prevented bounded fallback", got, err)
	}
	var mu sync.Mutex
	calls := 0
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	_, err = probe(context.Background(), newTransport(dial), Options{}, probeBudget{total: 100 * time.Millisecond, attempt: 60 * time.Millisecond})
	mu.Lock()
	count := calls
	mu.Unlock()
	if err == nil || count != 2 || time.Since(start) > time.Second {
		t.Fatal("fallback reset total budget", count, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := count
	_, err = Probe(ctx, dial, Options{})
	mu.Lock()
	count = calls
	mu.Unlock()
	if err == nil || count != before {
		t.Fatal("canceled probe started another dial")
	}
}

func TestProbeSanitizesAllFailedEndpointErrors(t *testing.T) {
	_, err := Probe(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("secret-protocol-password")
	}, Options{})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("raw dial error escaped")
	}
}

func TestSelectedDialRetainsAttemptDeadlineAndCancellation(t *testing.T) {
	// net/http normally detaches DialContext from request cancellation to let
	// future requests reuse a pending dial. A controlled probe may not do that.
	deadlines := make(chan bool, 3)
	finished := make(chan struct{}, 3)
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		deadlines <- ok && time.Until(deadline) <= 100*time.Millisecond
		if ok {
			<-ctx.Done()
		}
		finished <- struct{}{}
		return nil, errors.New("fixture canceled")
	}
	_, err := probe(context.Background(), newTransport(dial), Options{}, probeBudget{total: time.Second, attempt: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("canceled dials accepted")
	}
	for range 3 {
		select {
		case bounded := <-deadlines:
			if !bounded {
				t.Error("selected DNS/connect dial lost its attempt deadline")
			}
		case <-time.After(time.Second):
			t.Fatal("missing selected dial")
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("selected dial survived probe cancellation")
		}
	}
}
