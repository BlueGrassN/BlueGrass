package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestChinaResponseParser(t *testing.T) {
	for _, tc := range []struct{ host, body, want string }{
		{"myip.ipip.net", "当前 IP：8.8.4.4  来自于：中国 测试\n", "8.8.4.4"},
		{"4.ipw.cn", "1.1.1.1\r\n", "1.1.1.1"},
		{"myip.ipip.net", "8.8.4.4", ""},
		{"myip.ipip.net", "当前 IP：8.8.4.4", ""},
		{"myip.ipip.net", "当前 IP：8.8.4.4  来自于：", ""},
		{"myip.ipip.net", "当前 IP：8.8.4.4  来自于：测试\n当前 IP：1.1.1.1  来自于：测试", ""},
		{"myip.ipip.net", "当前 IP：8.8.4.4  来自于：<html>测试</html>", ""},
		{"4.ipw.cn", "<html>8.8.4.4</html>", ""},
		{"4.ipw.cn", "8.8.4.4\n1.1.1.1", ""},
		{"4.ipw.cn", "x8.8.4.4y", ""},
		{"4.ipw.cn", "008.8.4.4", ""},
		{"4.ipw.cn", "10.0.0.1", ""},
		{"4.ipw.cn", "198.19.255.255", ""},
		{"4.ipw.cn", "::ffff:8.8.4.4", ""},
		{"4.ipw.cn", "8.8.4.4" + strings.Repeat(" ", 1024), ""},
		{"4.ipw.cn.example.com", "8.8.4.4", ""},
	} {
		got, err := parseChinaIPv4(tc.host, []byte(tc.body))
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("host %s, body %q: got %q, %v", tc.host, tc.body, got, err)
		}
	}
}

func TestRoutingTLSVerifiesIssuerAndDomainOnLiteralPath(t *testing.T) {
	requests := make(chan *http.Request, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		_, _ = io.WriteString(w, "8.8.4.4\n")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	target := netip.MustParseAddrPort(server.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	for _, path := range []string{"domain", "literal_ip"} {
		client, transport, url := routingHTTPClient(pool, "example.com", target, path)
		got := requestHTTPS(context.Background(), client, url, "example.com", func(body []byte) (string, error) { return parseChinaIPv4("4.ipw.cn", body) })
		transport.CloseIdleConnections()
		if !got.OK || got.PublicIPv4 != "8.8.4.4" {
			t.Fatalf("verified %s request failed: %+v", path, got)
		}
		r := <-requests
		if r.Host != "example.com" || r.TLS.ServerName != "example.com" {
			t.Fatalf("lost hostname/SNI: host=%s sni=%s", r.Host, r.TLS.ServerName)
		}
	}
	for _, tc := range []struct {
		host  string
		roots *x509.CertPool
	}{
		{"example.com", x509.NewCertPool()},
		{"wrong.invalid", pool},
	} {
		client, transport, url := routingHTTPClient(tc.roots, tc.host, target, "literal_ip")
		got := requestHTTPS(context.Background(), client, url, tc.host, func(body []byte) (string, error) { return parseChinaIPv4("4.ipw.cn", body) })
		transport.CloseIdleConnections()
		if got.OK || got.Error != "tls_verify" {
			t.Fatalf("untrusted issuer/wrong hostname accepted: %+v", got)
		}
	}
}

func TestRoutingTargetsKeepFakeIPSeparate(t *testing.T) {
	got := routingAddresses([]netip.Addr{
		netip.MustParseAddr("198.18.0.4"), netip.MustParseAddr("10.2.3.4"),
		netip.MustParseAddr("8.8.4.4"), netip.MustParseAddr("8.8.4.4"),
		netip.MustParseAddr("::1"),
	})
	if len(got.ResolvedIPv4) != 1 || got.ResolvedIPv4[0] != "8.8.4.4" || len(got.SyntheticIPv4) != 1 || got.SyntheticIPv4[0] != "198.18.0.4" || got.RejectedAddressCount != 2 {
		t.Fatalf("bad address provenance: %+v", got)
	}
	if got.domainTarget().String() != "198.18.0.4" || got.literalTarget(netip.Addr{}).String() != "8.8.4.4" {
		t.Fatal("domain/FakeIP and literal targets were conflated")
	}
	fakeOnly := routingAddresses([]netip.Addr{netip.MustParseAddr("198.19.0.1")})
	if fakeOnly.literalTarget(netip.Addr{}).IsValid() || fakeOnly.literalTarget(netip.MustParseAddr("198.18.1.1")).IsValid() {
		t.Fatal("synthetic literal target accepted")
	}
	if fakeOnly.literalTarget(netip.MustParseAddr("1.1.1.1")).String() != "1.1.1.1" {
		t.Fatal("cached physical target lost")
	}
}

func TestRoutingCanceledRunIsBoundedAndRetainsEndpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	got := probeRouting(ctx, explicitResolver{server: "127.0.0.1:1"}, x509.NewCertPool(), nil)
	if time.Since(start) > time.Second || got.Counts.Total != 6 || got.Counts.OK != 0 || len(got.China) != 6 {
		t.Fatalf("canceled result: %+v", got)
	}
	for _, row := range got.China {
		if row.Domain == "" || row.Endpoint == "" || row.Classification != "unverified" || row.OK || row.Error == "" {
			t.Fatalf("lost identity/falsely claimed CN: %+v", row)
		}
	}
}

// A real local DNS responder; a missing explicit resolver hook would fail these
// tests or contact a different server. No public endpoint is contacted.
func routingTestDNS(t *testing.T, answer [4]byte) (explicitResolver, <-chan string) {
	t.Helper()
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	queries := make(chan string, 16)
	go func() {
		buf := make([]byte, 512)
		for {
			n, peer, err := server.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			offset := 12
			var labels []string
			for offset < n && buf[offset] != 0 {
				length := int(buf[offset])
				if offset+1+length >= n {
					break
				}
				labels = append(labels, string(buf[offset+1:offset+1+length]))
				offset += 1 + length
			}
			end := offset + 5
			if end > n || binary.BigEndian.Uint16(buf[offset+1:offset+3]) != 1 {
				continue
			}
			queries <- strings.Join(labels, ".")
			response := bytes.Clone(buf[:end])
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[6:8], 1)
			binary.BigEndian.PutUint16(response[8:10], 0)
			binary.BigEndian.PutUint16(response[10:12], 0)
			response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
			response = append(response, answer[:]...)
			_, _ = server.WriteToUDP(response, peer)
		}
	}()
	return explicitResolver{server: server.LocalAddr().String()}, queries
}

func TestRoutingDomainDNSAndCachedLiteralKeepSeparateProvenance(t *testing.T) {
	resolver, queries := routingTestDNS(t, [4]byte{198, 18, 0, 7})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows := probeRoutingEndpoint(ctx, resolver, nil, routingEndpoints[0], netip.MustParseAddr("117.149.203.79"), []string{"domain", "literal_ip"})
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	domain, literal := rows[0], rows[1]
	if !domain.DNS.OK || domain.Domain != "myip.ipip.net" || domain.TargetIPv4 != "198.18.0.7" || domain.TargetKind != "synthetic" || domain.TargetSource != "dns_answer" || domain.Error != "tls_no_roots" {
		t.Fatalf("lost DNS/domain provenance: %+v", domain)
	}
	if literal.Domain != domain.Domain || literal.Endpoint != domain.Endpoint || literal.TargetIPv4 != "117.149.203.79" || literal.TargetKind != "public" || literal.TargetSource != "routing_ip_flag" || literal.Error != "tls_no_roots" {
		t.Fatalf("literal target reused FakeIP/disabled TLS: %+v", literal)
	}
	select {
	case query := <-queries:
		if query != "myip.ipip.net" {
			t.Fatalf("query = %q", query)
		}
	default:
		t.Fatal("explicit DNS was not queried")
	}
	select {
	case query := <-queries:
		t.Fatalf("unexpected second resolution: %s", query)
	default:
	}
}

func TestRoutingPrivateDNSAnswersAreNotDialedOrLoggedAndSTUNFallsBack(t *testing.T) {
	resolver, queries := routingTestDNS(t, [4]byte{10, 23, 45, 67})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got := probeRouting(ctx, resolver, nil, nil)
	if len(got.China) != 8 || got.Counts.Total != 8 || got.Counts.Failed != 8 {
		t.Fatalf("missing failed paths/fallback: %+v", got)
	}
	for _, row := range got.China {
		if row.RejectedAddressCount != 1 || row.TargetIPv4 != "" || row.Attempts != 0 || len(row.ResolvedIPv4) != 0 || len(row.SyntheticIPv4) != 0 || !row.DNS.OK {
			t.Fatalf("non-public DNS answer used as target: %+v", row)
		}
	}
	if got.China[6].Endpoint != "stun.qq.com:3478" || got.China[7].Path != "literal_ip" {
		t.Fatalf("wrong fallback: %+v", got.China[6:])
	}
	body, err := json.Marshal(got)
	if err != nil || bytes.Contains(body, []byte("10.23.45.67")) {
		t.Fatalf("private address logged: %s", body)
	}
	seen := make(map[string]int)
	for len(queries) > 0 {
		seen[<-queries]++
	}
	for _, host := range []string{"myip.ipip.net", "4.ipw.cn", "stun.miwifi.com", "stun.qq.com"} {
		if seen[host] != 1 {
			t.Fatalf("DNS queries: %v", seen)
		}
	}
}

func TestRoutingSTUNReportsMappedExitUsingSharedTransactionChecks(t *testing.T) {
	server, _ := stunTestSocket(t)
	target := netip.MustParseAddrPort(server.LocalAddr().String())
	response := decodeHex(t, "0101000c2112a442000102030405060708090a0b002000080001a147291aa046") // 8.8.4.4:32853
	go func() {
		buf := make([]byte, 64)
		n, peer, err := server.ReadFromUDP(buf)
		if err != nil || n != 20 {
			return
		}
		copy(response[8:20], buf[8:20])
		_, _ = server.WriteToUDP(response, peer)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	row := probeRoutingPath(ctx, nil, routingEndpoint{"stun.miwifi.com", "stun", target.Port()}, routingResult{Domain: "stun.miwifi.com", Path: "literal_ip", Classification: "unverified"}, target.Addr())
	if !row.OK || row.PublicIPv4 != "8.8.4.4" || row.MappedPort != 32853 || row.Attempts != 1 || row.Classification != "unverified" {
		t.Fatalf("bad STUN exit observation: %+v", row)
	}
}
