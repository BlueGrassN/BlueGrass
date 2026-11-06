package main

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type routingReport struct {
	Counts counts          `json:"counts"`
	China  []routingResult `json:"china"`
}

type routingResult struct {
	status
	routingAddressInfo
	Protocol       string `json:"protocol"`
	Endpoint       string `json:"endpoint"`
	Domain         string `json:"domain"`
	Path           string `json:"path"`
	DNS            status `json:"dns"`
	TargetIPv4     string `json:"target_ipv4"`
	TargetKind     string `json:"target_kind"`
	TargetSource   string `json:"target_source"`
	Classification string `json:"classification"`
	PublicIPv4     string `json:"public_ipv4"`
	MappedPort     uint16 `json:"mapped_port"`
	Attempts       int    `json:"attempts"`
}

type routingAddressInfo struct {
	ResolvedIPv4         []string `json:"resolved_ipv4"`
	SyntheticIPv4        []string `json:"synthetic_ipv4"`
	RejectedAddressCount int      `json:"rejected_address_count"`
	usable               []netip.Addr
}

type routingEndpoint struct {
	host, protocol string
	port           uint16
}

var routingEndpoints = []routingEndpoint{
	{"myip.ipip.net", "https", 443},
	{"4.ipw.cn", "https", 443},
	{"stun.miwifi.com", "stun", 3478},
}

var routingSTUNFallback = routingEndpoint{"stun.qq.com", "stun", 3478}
var syntheticIPv4 = netip.MustParsePrefix("198.18.0.0/15")

func routingHost(host string) bool {
	for _, endpoint := range routingEndpoints {
		if host == endpoint.host {
			return true
		}
	}
	return host == routingSTUNFallback.host
}

// Match the two existing project's plain-text formats, never scrape arbitrary
// text for an IPv4-looking substring. Location text is neither logged nor trusted.
func parseChinaIPv4(host string, body []byte) (string, error) {
	invalid := probeError("invalid_public_ipv4")
	if len(body) > 1024 {
		return "", probeError("https_body")
	}
	value := strings.TrimSpace(string(body))
	switch host {
	case "myip.ipip.net":
		if !strings.HasPrefix(value, "当前 IP：") || strings.ContainsAny(value, "\r\n<>") {
			return "", invalid
		}
		address, location, found := strings.Cut(strings.TrimPrefix(value, "当前 IP："), "来自于：")
		if !found || strings.TrimSpace(location) == "" || strings.Contains(location, "当前 IP：") || strings.Contains(location, "来自于：") {
			return "", invalid
		}
		value = strings.TrimSpace(address)
	case "4.ipw.cn":
	default:
		return "", invalid
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !isPublicIPv4(ip) {
		return "", invalid
	}
	return ip.String(), nil
}

func routingAddresses(ips []netip.Addr) routingAddressInfo {
	result := routingAddressInfo{ResolvedIPv4: []string{}, SyntheticIPv4: []string{}}
	seen := make(map[netip.Addr]bool)
	for _, ip := range ips {
		if seen[ip] {
			continue
		}
		seen[ip] = true
		switch {
		case isPublicIPv4(ip):
			result.ResolvedIPv4 = append(result.ResolvedIPv4, ip.String())
		case syntheticIPv4.Contains(ip):
			result.SyntheticIPv4 = append(result.SyntheticIPv4, ip.String())
		default:
			result.RejectedAddressCount++
			continue
		}
		result.usable = append(result.usable, ip)
	}
	return result
}

func (a routingAddressInfo) domainTarget() netip.Addr {
	if len(a.usable) == 0 {
		return netip.Addr{}
	}
	return a.usable[0]
}

func (a routingAddressInfo) literalTarget(cached netip.Addr) netip.Addr {
	if cached.IsValid() {
		if isPublicIPv4(cached) {
			return cached
		}
		return netip.Addr{}
	}
	if len(a.ResolvedIPv4) == 0 {
		return netip.Addr{}
	}
	return netip.MustParseAddr(a.ResolvedIPv4[0])
}

// Resolution is done once through the configured resolver by the caller. Pin
// this attempt to that answer (or a validated parent-provided physical address).
// Both URL forms retain normal issuer and original-domain certificate checks.
func routingHTTPClient(roots *x509.CertPool, domain string, target netip.AddrPort, path string) (*http.Client, *http.Transport, string) {
	client, transport := newHTTPClient(explicitResolver{}, roots)
	transport.TLSClientConfig.ServerName = domain
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: stageTimeout}).DialContext(ctx, "tcp4", target.String())
	}
	host := domain
	if path == "literal_ip" {
		host = target.Addr().String()
	}
	if target.Port() != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(int(target.Port())))
	}
	return client, transport, "https://" + host + "/"
}

func probeRoutingPath(parent context.Context, roots *x509.CertPool, endpoint routingEndpoint, result routingResult, target netip.Addr) routingResult {
	start := time.Now()
	if !target.IsValid() {
		result.Error = "no_public_target"
		if result.Path == "domain" {
			result.Error = "no_safe_target"
			if !result.DNS.OK {
				result.Error = result.DNS.Error
			}
		}
		return result
	}
	result.TargetIPv4, result.TargetKind = target.String(), "public"
	if syntheticIPv4.Contains(target) {
		result.TargetKind = "synthetic"
	}
	ctx, cancel := context.WithTimeout(parent, stageTimeout)
	defer cancel()
	if ctx.Err() != nil {
		result.Error = "timeout"
		return result
	}
	address := netip.AddrPortFrom(target, endpoint.port)
	if endpoint.protocol == "https" {
		if roots == nil {
			result.Error = "tls_no_roots"
			return result
		}
		client, transport, url := routingHTTPClient(roots, endpoint.host, address, result.Path)
		defer transport.CloseIdleConnections()
		attempt := requestHTTPS(ctx, client, url, endpoint.host, func(body []byte) (string, error) { return parseChinaIPv4(endpoint.host, body) })
		result.status, result.PublicIPv4 = attempt.status, attempt.PublicIPv4
	} else {
		attempt := stunAddress(ctx, address.String())
		result.status, result.PublicIPv4 = attempt.status, attempt.PublicIPv4
		result.MappedPort, result.Attempts = attempt.MappedPort, attempt.Attempts
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

func probeRoutingEndpoint(parent context.Context, resolver explicitResolver, roots *x509.CertPool, endpoint routingEndpoint, cached netip.Addr, paths []string) []routingResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, stageTimeout)
	ips, err := resolver.lookup(ctx, endpoint.host)
	cancel()
	dns := status{OK: err == nil, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		dns.Error = errorCode(err, "dns_failed")
	}
	// Bound report size and work even with an unexpectedly large A response.
	if len(ips) > 64 {
		ips = nil
		dns.OK, dns.Error = false, "dns_answer_limit"
	}
	addresses := routingAddresses(ips)
	results := make([]routingResult, 0, len(paths))
	for _, path := range paths {
		result := routingResult{
			routingAddressInfo: addresses, DNS: dns, Protocol: endpoint.protocol,
			Domain: endpoint.host, Path: path, Classification: "unverified",
			Endpoint:     net.JoinHostPort(endpoint.host, strconv.Itoa(int(endpoint.port))),
			TargetSource: "dns_answer",
		}
		if endpoint.protocol == "https" {
			result.Endpoint = "https://" + endpoint.host + "/"
		}
		target := addresses.domainTarget()
		if path == "literal_ip" {
			target = addresses.literalTarget(cached)
			if cached.IsValid() {
				result.TargetSource = "routing_ip_flag"
			}
		}
		results = append(results, probeRoutingPath(parent, roots, endpoint, result, target))
	}
	return results
}

func probeRouting(ctx context.Context, resolver explicitResolver, roots *x509.CertPool, cached map[string]netip.Addr) routingReport {
	// Three endpoints in parallel, with a domain then literal transaction each.
	// All work, including the optional UDP fallback, shares the original 45s run.
	groups := make([][]routingResult, len(routingEndpoints))
	var ready sync.WaitGroup
	for i, endpoint := range routingEndpoints {
		ready.Add(1)
		go func(i int, endpoint routingEndpoint) {
			defer ready.Done()
			groups[i] = probeRoutingEndpoint(ctx, resolver, roots, endpoint, cached[endpoint.host], []string{"domain", "literal_ip"})
		}(i, endpoint)
	}
	ready.Wait()
	result := routingReport{China: []routingResult{}}
	var fallbackPaths []string
	for _, group := range groups {
		for _, row := range group {
			result.China = append(result.China, row)
			if row.Protocol == "stun" && !row.OK {
				fallbackPaths = append(fallbackPaths, row.Path)
			}
		}
	}
	if len(fallbackPaths) > 0 && ctx.Err() == nil {
		result.China = append(result.China, probeRoutingEndpoint(ctx, resolver, roots, routingSTUNFallback, cached[routingSTUNFallback.host], fallbackPaths)...)
	}
	result.Counts.Total = len(result.China)
	for _, row := range result.China {
		if row.OK {
			result.Counts.OK++
		}
	}
	result.Counts.Failed = result.Counts.Total - result.Counts.OK
	return result
}
