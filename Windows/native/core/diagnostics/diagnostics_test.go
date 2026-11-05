package diagnostics

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func result(t *testing.T, report Report, id string) Result {
	t.Helper()
	for _, r := range report.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("missing result %s: %+v", id, report)
	return Result{}
}

func binding() *Binding {
	return &Binding{Connected: true, Generation: 7, ResourceID: "resource-secret"}
}

func TestEmptyRequestDoesNotDialAndNeverPasses(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("unexpected dial")
		return nil, io.EOF
	}
	report := (Runner{DirectDial: dial, SelectedDial: dial}).Run(context.Background(), Request{})
	if len(report.Results) < 10 {
		t.Fatalf("missing explicit scope: %+v", report)
	}
	for _, r := range report.Results {
		if r.Status != Skipped {
			t.Fatalf("not skipped: %+v", r)
		}
	}
}

func tlsFixture(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *x509.CertPool, DialFunc) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	return server, pool, dial
}

func TestAPIHealthActualTLSAndEnvelope(t *testing.T) {
	var hits atomic.Int32
	server, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/client/v1/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unsafe request: %s", r.URL.Path)
		}
		io.WriteString(w, `{"success":true,"data":{"status":"ok"},"server_time":"2026-10-12T00:00:00Z"}`)
	})
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	req := Request{APIBaseURL: server.URL + "/api/client/v1"}
	report := (Runner{RootCAs: pool, DirectDial: dial}).Run(context.Background(), req)
	if r := result(t, report, "api_health"); r.Status != Passed || hits.Load() != 1 {
		t.Fatalf("%+v hits=%d", r, hits.Load())
	}
	if r := result(t, report, "physical_bypass"); r.Status != Skipped {
		t.Fatal(r)
	}
	// A trusted response body cannot bypass certificate verification.
	report = (Runner{DirectDial: dial}).Run(context.Background(), req)
	if r := result(t, report, "api_health"); r.Status != Failed {
		t.Fatal(r)
	}
}

func TestAPIRejectsRedirectInvalidEnvelopeAndLargeBody(t *testing.T) {
	for _, body := range []string{`{"success":false,"data":{"status":"ok"},"server_time":"2026-10-12T00:00:00Z"}`, `{"success":true,"data":{"status":"ok"}}`, strings.Repeat("x", 4097), `{"success":true,"success":false}`, `{"success":true,"data":{"status":"ok"},"server_time":"2026-10-12T00:00:00Z"} {}`} {
		t.Run(body[:min(25, len(body))], func(t *testing.T) {
			server, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if r := result(t, (Runner{RootCAs: pool, DirectDial: dial}).Run(context.Background(), Request{APIBaseURL: server.URL}), "api_health"); r.Status != Failed {
				t.Fatal(r)
			}
		})
	}
	var hits atomic.Int32
	server, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Location", "/redirected")
		w.WriteHeader(302)
	})
	if r := result(t, (Runner{RootCAs: pool, DirectDial: dial}).Run(context.Background(), Request{APIBaseURL: server.URL}), "api_health"); r.Status != Failed || hits.Load() != 1 {
		t.Fatal(r)
	}
}

func TestEntryLiteralRealTCPAndDNSIsSkipped(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan bool, 1)
	go func() {
		c, err := listener.Accept()
		if err == nil {
			c.Close()
			accepted <- true
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	report := (Runner{}).Run(context.Background(), Request{Entry: &Entry{Host: host, Port: port}})
	if r := result(t, report, "entry_dns"); r.Status != Skipped || r.Code != "literal_address" {
		t.Fatal(r)
	}
	if r := result(t, report, "entry_tcp"); r.Status != Passed {
		t.Fatal(r)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no TCP accept")
	}
}

func TestCustomDNSActualExchangeAndNoSystemFallback(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		hits.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 127.0.0.1")
			reply.Answer = []dns.RR{rr}
		}
		w.WriteMsg(reply)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	go func() {
		c, err := listener.Accept()
		if err == nil {
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	req := Request{TimeoutMS: 300, Entry: &Entry{Host: "entry.invalid", Port: port, DNSMode: "custom", DNSServers: []DNSServer{{Address: packet.LocalAddr().String(), Network: "udp"}}}}
	report := (Runner{}).Run(context.Background(), req)
	if r := result(t, report, "entry_dns"); r.Status != Passed || hits.Load() == 0 {
		t.Fatal(r)
	}
	if r := result(t, report, "entry_tcp"); r.Status != Passed {
		t.Fatal(r)
	}
	req.Entry.Host = "localhost"
	req.Entry.DNSServers[0].Address = "127.0.0.1:1"
	report = (Runner{}).Run(context.Background(), req)
	if r := result(t, report, "entry_dns"); r.Status != Failed {
		t.Fatal(r)
	}
	if r := result(t, report, "entry_tcp"); r.Status != Skipped {
		t.Fatal(r)
	}
}

func TestProxyExitActualTLSIPv4PinAndValidObservationStopsFallback(t *testing.T) {
	for _, observed := range []string{"8.8.8.8", "1.1.1.1", "8.8.4.4"} {
		t.Run(observed, func(t *testing.T) {
			var hits atomic.Int32
			_, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Host != "example.com" || r.TLS.ServerName != "example.com" {
					t.Error("IPv4 pin lost the original TLS/HTTP hostname")
				}
				io.WriteString(w, `{"ip":"`+observed+`","country":"test"}`)
			})
			selected := func(ctx context.Context, network, address string) (net.Conn, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("selected dial lost the probe deadline")
				}
				if network != "tcp" || address != "8.8.4.4:443" {
					t.Errorf("not pinned: %s %s", network, address)
				}
				return dial(ctx, network, address)
			}
			req := Request{Binding: binding(), ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}, {URL: "https://example.com", IPv4: "8.8.4.4"}}}
			report := (Runner{RootCAs: pool, SelectedDial: selected}).Run(context.Background(), req)
			r := result(t, report, "proxy_exit")
			if r.Status != Passed || r.Code != "observed_exit" || hits.Load() != 1 || r.ObservedIP != observed {
				t.Fatalf("%+v hits=%d", r, hits.Load())
			}
			if report.Generation != 7 || report.ResourceFingerprint == "" {
				t.Fatal(report)
			}
			data, _ := json.Marshal(report)
			if bytes.Contains(data, []byte("resource-secret")) {
				t.Fatal("resource leaked")
			}
		})
	}
}

func TestExitFallbackAfterTransportFailureAndInvalidResponse(t *testing.T) {
	var calls atomic.Int32
	_, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/invalid" {
			io.WriteString(w, `{"ip":"not-an-ip"}`)
			return
		}
		io.WriteString(w, `{"ip":"8.8.8.8"}`)
	})
	runner := Runner{RootCAs: pool, SelectedDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if address == "1.1.1.1:443" {
			return nil, io.EOF
		}
		return dial(ctx, network, address)
	}}
	req := Request{Binding: binding(), ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "1.1.1.1"}, {URL: "https://example.com/invalid", IPv4: "8.8.4.4"}, {URL: "https://example.com", IPv4: "8.8.4.4"}}}
	if r := result(t, runner.Run(context.Background(), req), "proxy_exit"); r.Status != Passed || r.Attempts != 3 || calls.Load() != 3 {
		t.Fatal(r)
	}
}

func TestRealUDPSilenceTimesOutWithoutDirectFallback(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	var received atomic.Int32
	go func() {
		b := make([]byte, 128)
		if _, _, err := packet.ReadFrom(b); err == nil {
			received.Add(1)
		}
	}()
	var dials atomic.Int32
	runner := Runner{DirectDial: func(context.Context, string, string) (net.Conn, error) {
		t.Error("UDP used direct fallback")
		return nil, io.EOF
	}, SelectedDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "udp", packet.LocalAddr().String())
	}}
	start := time.Now()
	report := runner.Run(context.Background(), Request{Binding: binding(), TimeoutMS: 100, UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}})
	if r := result(t, report, "proxy_udp"); r.Status != Failed || received.Load() != 1 || dials.Load() != 1 || time.Since(start) > time.Second {
		t.Fatal(r)
	}
}

func TestGenerationsRemainIndependentDuringCancellation(t *testing.T) {
	_, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"ip":"8.8.8.8"}`) })
	started := make(chan struct{})
	runner := Runner{RootCAs: pool, SelectedDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "1.1.1.1:443" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return dial(ctx, network, address)
	}}
	oldCtx, cancel := context.WithCancel(context.Background())
	old := Request{Binding: binding(), ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "1.1.1.1"}}}
	oldReports := make(chan Report, 1)
	go func() { oldReports <- runner.Run(oldCtx, old) }()
	<-started
	fresh := Request{Binding: &Binding{Connected: true, Generation: 8, ResourceID: "different-resource"}, ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}}}
	current := runner.Run(context.Background(), fresh)
	cancel()
	previous := <-oldReports
	if current.Cancelled || current.Generation != 8 || previous.Generation != 7 || !previous.Cancelled || current.ResourceFingerprint == previous.ResourceFingerprint {
		t.Fatal("binding isolation failed")
	}
	if r := result(t, current, "proxy_exit"); r.Status != Passed {
		t.Fatal(r)
	}
	for _, r := range previous.Results {
		if r.Status != Skipped {
			t.Fatal("cancelled generation retained a result")
		}
	}
}

func TestCustomDNSTCPFallbackAfterSilentPrimary(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := &dns.Server{Listener: listener, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		hits.Add(1)
		answer := new(dns.Msg)
		answer.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(q.Question[0].Name + " 1 IN A 127.0.0.1")
			answer.Answer = []dns.RR{rr}
		}
		w.WriteMsg(answer)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	ip, r := resolveEntry(ctx, &Entry{Host: "entry.invalid", Port: "443", DNSMode: "custom", DNSServers: []DNSServer{{Address: silent.LocalAddr().String(), Network: "udp"}, {Address: listener.Addr().String(), Network: "tcp"}}})
	if r.Status != Passed || r.Candidate != 2 || hits.Load() != 2 || len(ip) != 1 {
		t.Fatal(r)
	}
}

func TestExpiredProbeCannotPassEvenIfDialIgnoresContext(t *testing.T) {
	runner := Runner{DirectDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		<-ctx.Done()
		client, peer := net.Pipe()
		peer.Close()
		return client, nil
	}}
	report := runner.Run(context.Background(), Request{TimeoutMS: 100, Entry: &Entry{Host: "127.0.0.1", Port: "443"}})
	if r := result(t, report, "entry_tcp"); r.Status != Failed {
		t.Fatal(r)
	}
}

func TestCustomDNSEighthCandidateSharesProbeBudget(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := &dns.Server{Listener: listener, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		hits.Add(1)
		answer := new(dns.Msg)
		answer.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(q.Question[0].Name + " 1 IN A 127.0.0.1")
			answer.Answer = []dns.RR{rr}
		}
		w.WriteMsg(answer)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	servers := make([]DNSServer, 8)
	for i := 0; i < 7; i++ {
		servers[i] = DNSServer{Address: silent.LocalAddr().String(), Network: "udp"}
	}
	servers[7] = DNSServer{Address: listener.Addr().String(), Network: "tcp"}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	start := time.Now()
	report := (Runner{}).Run(context.Background(), Request{TimeoutMS: 1600, Entry: &Entry{Host: "entry.invalid", Port: port, DNSMode: "custom", DNSServers: servers}})
	r := result(t, report, "entry_dns")
	if r.Status != Passed || r.Candidate != 8 || r.Attempts != 8 || hits.Load() != 2 {
		t.Fatal(r)
	}
	// Seven silent candidates share ONE budget; they do not each receive it.
	if time.Since(start) > 2300*time.Millisecond {
		t.Fatal("DNS fallback multiplied the probe budget")
	}
	if r := result(t, report, "entry_tcp"); r.Status != Passed {
		t.Fatal(r)
	}
}

func TestProxyMissingPrerequisitesNeverDials(t *testing.T) {
	runner := Runner{SelectedDial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("must not open a session")
		return nil, io.EOF
	}}
	for _, req := range []Request{{}, {Binding: binding()}, {Binding: binding(), ExpectedExitIP: "8.8.8.8"}, {Binding: &Binding{Connected: false}, ExpectedExitIP: "8.8.8.8", ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}}}} {
		report := runner.Run(context.Background(), req)
		if r := result(t, report, "proxy_exit"); r.Status != Skipped {
			t.Fatal(r)
		}
		if r := result(t, report, "proxy_udp"); r.Status != Skipped {
			t.Fatal(r)
		}
	}
}

func TestUDPActualNonceRoundTripAndWrongReply(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		packet, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var received atomic.Int32
		go func() {
			buffer := make([]byte, 512)
			n, peer, err := packet.ReadFrom(buffer)
			if err == nil {
				received.Add(1)
				if wrong {
					buffer[0] ^= 1
				}
				packet.WriteTo(buffer[:n], peer)
			}
		}()
		runner := Runner{SelectedDial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "udp" || address != "8.8.4.4:9999" {
				t.Errorf("unexpected transport %s %s", network, address)
			}
			return (&net.Dialer{}).DialContext(ctx, "udp", packet.LocalAddr().String())
		}}
		report := runner.Run(context.Background(), Request{Binding: binding(), UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}})
		packet.Close()
		r := result(t, report, "proxy_udp")
		want := Passed
		if wrong {
			want = Failed
		}
		if r.Status != want || received.Load() != 1 {
			t.Fatal(r)
		}
		if r := result(t, report, "udp_exit_identity"); r.Status != Skipped {
			t.Fatal(r)
		}
	}
}

func TestCancellationAndTimeoutAreBoundedAndRedacted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	runner := Runner{SelectedDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, &net.OpError{Op: "credential-secret", Err: ctx.Err()}
	}}
	req := Request{Binding: binding(), UDP: &UDPEndpoint{Address: "8.8.4.4:9999", Protocol: "echo_nonce_v1"}, TimeoutMS: 100}
	go func() { <-started; cancel() }()
	report := runner.Run(ctx, req)
	if !report.Cancelled || result(t, report, "proxy_udp").Status != Skipped {
		t.Fatal(report)
	}
	data, _ := json.Marshal(report)
	if bytes.Contains(data, []byte("credential-secret")) {
		t.Fatal("error leaked")
	}
	start := time.Now()
	runner.SelectedDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	report = runner.Run(context.Background(), req)
	if result(t, report, "proxy_udp").Status != Failed || time.Since(start) > time.Second {
		t.Fatal(report)
	}
}

func TestExitParserStrictness(t *testing.T) {
	for _, body := range []string{`{"ip":"8.8.8.8","ip":"1.1.1.1"}`, `{"ip":"127.0.0.1"}`, `{"ip":"8.8.8.8","token":"secret"}`, `{"ip":"8.8.8.8"} []`, `{"ip":"2001:db8::1"}`} {
		if _, err := parseExit([]byte(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}
