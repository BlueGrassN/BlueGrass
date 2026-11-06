package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"
)

type probeError string

func (e probeError) Error() string { return string(e) }

func errorCode(err error, fallback string) string {
	var code probeError
	if errors.As(err, &code) {
		return string(code)
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return "tls_verify"
	}
	return fallback
}

type explicitResolver struct{ server string }

func (r explicitResolver) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	var queried atomic.Bool
	resolver := &net.Resolver{
		PreferGo: true, StrictErrors: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			transport := "udp4"
			if strings.HasPrefix(network, "tcp") {
				transport = "tcp4"
			}
			queried.Store(true)
			// Ignore every resolver-selected address, including TCP fallback.
			return (&net.Dialer{Timeout: stageTimeout}).DialContext(ctx, transport, r.server)
		},
	}
	// ip4 requests A records only; the trailing dot prevents search suffixes.
	ips, err := resolver.LookupNetIP(ctx, "ip4", host+".")
	if err != nil {
		return nil, probeError(errorCode(err, "dns_failed"))
	}
	if !queried.Load() {
		return nil, probeError("dns_not_queried")
	}
	if len(ips) == 0 {
		return nil, probeError("dns_no_a")
	}
	return ips, nil
}

func probeDNS(parent context.Context, resolver explicitResolver) (result dnsResult) {
	start := time.Now()
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	result.Host = "example.com"
	ctx, cancel := context.WithTimeout(parent, stageTimeout)
	defer cancel()
	ips, err := resolver.lookup(ctx, result.Host)
	if err != nil {
		result.Error = errorCode(err, "dns_failed")
		return
	}
	result.ACount = len(ips)
	result.OK = true
	return
}

func newHTTPClient(resolver explicitResolver, roots *x509.CertPool) (*http.Client, *http.Transport) {
	transport := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: stageTimeout, ResponseHeaderTimeout: stageTimeout,
		DisableKeepAlives: true, DisableCompression: true,
		MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, probeError("connect_failed")
			}
			var ips []netip.Addr
			if literal, err := netip.ParseAddr(host); err == nil {
				if !literal.Is4() {
					return nil, probeError("connect_failed")
				}
				ips = []netip.Addr{literal}
			} else {
				ips, err = resolver.lookup(ctx, host)
				if err != nil {
					return nil, err
				}
			}
			var dialErr error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: stageTimeout}).DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				dialErr = err
				if ctx.Err() != nil {
					break
				}
			}
			return nil, probeError(errorCode(dialErr, "connect_failed"))
		},
	}
	client := &http.Client{
		Transport: transport, Timeout: stageTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, transport
}

func getHTTPS(parent context.Context, resolver explicitResolver, roots *x509.CertPool, url string, jsonBody bool) (result httpsAttempt) {
	start := time.Now()
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	result.URL = url
	if roots == nil {
		result.Error = "tls_no_roots"
		return
	}
	ctx, cancel := context.WithTimeout(parent, stageTimeout)
	defer cancel()
	client, transport := newHTTPClient(resolver, roots)
	defer transport.CloseIdleConnections()
	return requestHTTPS(ctx, client, url, "", func(body []byte) (string, error) {
		value := strings.TrimSpace(string(body))
		if jsonBody {
			var decoded struct {
				IP string `json:"ip"`
			}
			if json.Unmarshal(body, &decoded) != nil {
				return "", probeError("https_body")
			}
			value = decoded.IP
		}
		ip, err := netip.ParseAddr(value)
		if err != nil || !isPublicIPv4(ip) {
			return "", probeError("invalid_public_ipv4")
		}
		return ip.String(), nil
	})
}

// Shared bounded HTTP reader. Parsers return only an observation or a fixed code.
func requestHTTPS(ctx context.Context, client *http.Client, url, host string, parse func([]byte) (string, error)) (result httpsAttempt) {
	start := time.Now()
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	result.URL = url
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = "https_failed"
		return
	}
	if host != "" {
		request.Host = host
	}
	response, err := client.Do(request)
	if err != nil {
		result.Error = errorCode(err, "https_failed")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		result.Error = "http_status"
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	if err != nil {
		result.Error = errorCode(err, "https_body")
		return
	}
	if len(body) > 1024 {
		result.Error = "https_body"
		return
	}
	ip, err := parse(body)
	if err != nil {
		result.Error = errorCode(err, "invalid_public_ipv4")
		return
	}
	result.PublicIPv4 = ip
	result.OK = true
	return
}

func probeHTTPS(ctx context.Context, resolver explicitResolver, roots *x509.CertPool) (result httpsResult) {
	start := time.Now()
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	result.Attempts = []httpsAttempt{}
	for i, url := range []string{ipifyURL, alternateURL} {
		attempt := getHTTPS(ctx, resolver, roots, url, i == 0)
		result.Attempts = append(result.Attempts, attempt)
		result.Endpoint = url
		result.PublicIPv4 = attempt.PublicIPv4
		result.OK, result.Error = attempt.OK, attempt.Error
		if result.OK || ctx.Err() != nil || roots == nil {
			break
		}
	}
	return
}

func probeSTUN(parent context.Context, resolver explicitResolver, server string) (result stunResult) {
	start := time.Now()
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	result.Server = server
	ctx, cancel := context.WithTimeout(parent, stageTimeout)
	defer cancel()
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		result.Error = "stun_io"
		return
	}
	ips, err := resolver.lookup(ctx, host)
	if err != nil {
		result.Error = errorCode(err, "dns_failed")
		return
	}
	result = stunAddress(ctx, net.JoinHostPort(ips[0].String(), port))
	result.Server = server
	return
}

// The caller owns hostname resolution and the overall per-attempt deadline.
func stunAddress(ctx context.Context, address string) (result stunResult) {
	// One remote IPv4 and one OS-selected local socket per transaction.
	conn, err := (&net.Dialer{Timeout: stageTimeout}).DialContext(ctx, "udp4", address)
	if err != nil {
		result.Error = errorCode(err, "stun_io")
		return
	}
	defer conn.Close()
	var transaction [12]byte
	if _, err := rand.Read(transaction[:]); err != nil {
		result.Error = "random_failed"
		return
	}
	mapped, attempts, code := exchangeSTUN(ctx, conn, transaction)
	result.Attempts, result.Error = attempts, code
	if code != "" {
		return
	}
	if mapped.Port() == 0 || !isPublicIPv4(mapped.Addr()) {
		result.Error = "invalid_public_ipv4"
		return
	}
	result.PublicIPv4, result.MappedPort, result.OK = mapped.Addr().String(), mapped.Port(), true
	return
}

var nonPublicIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
}

func isPublicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicIPv4 {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
