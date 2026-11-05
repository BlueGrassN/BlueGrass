package diagnostics

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"bluegrass/native/core/gate"
	"github.com/miekg/dns"
)

func (r Runner) getJSON(ctx context.Context, target string, dial DialFunc) ([]byte, error) {
	// Transport may detach the request deadline before invoking DialContext.
	// Preserve both its cancellation and the original bounded probe context.
	boundedDial := func(transportCtx context.Context, network, address string) (net.Conn, error) {
		dialCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(transportCtx, cancel)
		defer stop()
		defer cancel()
		return dial(dialCtx, network, address)
	}
	transport := &http.Transport{Proxy: nil, DialContext: boundedDial, DisableKeepAlives: true, DisableCompression: true,
		TLSClientConfig:     &tls.Config{RootCAs: r.RootCAs, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 8192}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errInvalid
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "BlueGrass-Diagnostics/1")
	response, err := client.Do(request)
	if err != nil {
		return nil, errInvalid
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errInvalid
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 {
		return nil, errInvalid
	}
	return body, nil
}

func (r Runner) apiHealth(ctx context.Context, base string, dial DialFunc) Result {
	if base == "" {
		return skip("not_configured")
	}
	u, _ := safeHTTPS(base, false)
	u.Path = strings.TrimRight(u.Path, "/") + "/health"
	u.RawPath = ""
	body, err := r.getJSON(ctx, u.String(), dial)
	if err != nil {
		return fail("https_health_failed")
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
		ServerTime time.Time       `json:"server_time"`
		Error      json.RawMessage `json:"error,omitempty"`
	}
	if strictJSON(body, &envelope) != nil || !envelope.Success || envelope.Data.Status != "ok" || envelope.ServerTime.IsZero() || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return fail("invalid_health_response")
	}
	return pass("https_health_ok")
}

func resolveEntry(ctx context.Context, entry *Entry) ([]string, Result) {
	if entry == nil {
		return nil, skip("not_configured")
	}
	if ip, err := netip.ParseAddr(entry.Host); err == nil {
		return []string{ip.String()}, skip("literal_address")
	}
	if entry.DNSMode != "custom" {
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", entry.Host)
		if err != nil {
			return nil, fail("system_dns_failed")
		}
		var values []string
		for _, ip := range addresses {
			if ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast() {
				values = append(values, ip.Unmap().String())
				if len(values) == 4 {
					break
				}
			}
		}
		if len(values) == 0 {
			return nil, fail("dns_no_addresses")
		}
		return values, pass("system_dns_answer")
	}
	for i, server := range entry.DNSServers {
		// Reserve time for later candidates; a blackholed primary must not
		// consume the entire probe budget before an explicit fallback is tried.
		deadline, _ := ctx.Deadline()
		candidateCtx, cancel := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(entry.DNSServers)-i))
		var addresses []string
		for _, kind := range []uint16{dns.TypeA, dns.TypeAAAA} {
			q := new(dns.Msg)
			q.SetQuestion(dns.Fqdn(entry.Host), kind)
			answer, _, err := (&dns.Client{Net: server.Network, Timeout: 3 * time.Second}).ExchangeContext(candidateCtx, q, server.Address)
			if err != nil || answer == nil || answer.Truncated || answer.Rcode != dns.RcodeSuccess {
				continue
			}
			// Only records belonging to the queried owner or its explicit CNAME
			// chain are considered, never unrelated extra-section records.
			owners := map[string]bool{strings.ToLower(q.Question[0].Name): true}
			for step := 0; step < 8; step++ {
				for _, rr := range answer.Answer {
					if c, ok := rr.(*dns.CNAME); ok && owners[strings.ToLower(c.Hdr.Name)] {
						owners[strings.ToLower(c.Target)] = true
					}
				}
			}
			for _, rr := range answer.Answer {
				if !owners[strings.ToLower(rr.Header().Name)] {
					continue
				}
				var s string
				switch a := rr.(type) {
				case *dns.A:
					if kind == dns.TypeA {
						s = a.A.String()
					}
				case *dns.AAAA:
					if kind == dns.TypeAAAA {
						s = a.AAAA.String()
					}
				}
				ip, err := netip.ParseAddr(s)
				if err == nil && !ip.IsUnspecified() && !ip.IsMulticast() {
					addresses = append(addresses, ip.Unmap().String())
					if len(addresses) == 4 {
						break
					}
				}
			}
			if len(addresses) >= 4 {
				break
			}
		}
		cancel()
		if len(addresses) > 0 {
			res := pass("custom_dns_answer")
			res.Candidate = i + 1
			res.Attempts = i + 1
			return addresses, res
		}
	}
	res := fail("custom_dns_failed")
	res.Attempts = len(entry.DNSServers)
	return nil, res
}

func entryTCP(ctx context.Context, entry *Entry, addresses []string, dial DialFunc) Result {
	if entry == nil {
		return skip("not_configured")
	}
	if len(addresses) == 0 {
		return skip("dns_unavailable")
	}
	attempts := 0
	for i, ip := range addresses {
		deadline, _ := ctx.Deadline()
		attemptCtx, cancel := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(addresses)-i))
		attempts++
		conn, err := dial(attemptCtx, "tcp", net.JoinHostPort(ip, entry.Port))
		expired := contextExpired(attemptCtx)
		cancel()
		if err == nil {
			conn.Close()
			if !expired {
				res := pass("tcp_connected")
				res.Attempts = attempts
				return res
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	res := fail("tcp_connect_failed")
	res.Attempts = attempts
	return res
}

func parseExit(data []byte) (string, error) {
	var response struct {
		IP      string `json:"ip"`
		Country string `json:"country,omitempty"`
	}
	if len(data) > 4096 || strictJSON(data, &response) != nil {
		return "", errInvalid
	}
	// D33 exit display is IPv4-only; IPv6 permits the next configured fallback.
	if !gate.PublicIPv4(response.IP) {
		return "", errInvalid
	}
	return response.IP, nil
}

func (r Runner) proxyExit(ctx context.Context, req Request, dial DialFunc) Result {
	if !active(req) {
		return skip("no_active_binding")
	}
	if len(req.ExitEndpoints) == 0 {
		return skip("probe_not_configured")
	}
	if dial == nil {
		return skip("selected_channel_unavailable")
	}
	for i, endpoint := range req.ExitEndpoints {
		u, _ := safeHTTPS(endpoint.URL, true)
		port := u.Port()
		if port == "" {
			port = "443"
		}
		pinned := func(ctx context.Context, network, address string) (net.Conn, error) {
			return dial(ctx, "tcp", net.JoinHostPort(endpoint.IPv4, port))
		}
		deadline, _ := ctx.Deadline()
		attemptCtx, cancel := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(req.ExitEndpoints)-i))
		body, err := r.getJSON(attemptCtx, endpoint.URL, pinned)
		expired := contextExpired(attemptCtx)
		cancel()
		if err != nil || expired {
			continue
		}
		observed, err := parseExit(body)
		if err != nil {
			continue
		}
		res := pass("observed_exit")
		res.Attempts = i + 1
		res.ObservedIP = observed
		return res // The first valid observation is final.
	}
	res := fail("exit_probe_failed")
	res.Attempts = len(req.ExitEndpoints)
	return res
}

func proxyUDP(ctx context.Context, req Request, dial DialFunc) Result {
	if !active(req) {
		return skip("no_active_binding")
	}
	if req.UDP == nil {
		return skip("probe_not_configured")
	}
	if dial == nil {
		return skip("udp_channel_unavailable")
	}
	conn, err := dial(ctx, "udp", req.UDP.Address)
	if err != nil {
		return fail("udp_channel_failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return fail("udp_deadline_failed")
	}
	// Versioned fixed marker plus 32 unpredictable bytes. Only exact echo is
	// accepted; receiving an arbitrary packet is not proof of a round trip.
	payload := make([]byte, 40)
	copy(payload, "BGDIAG1\x00")
	if _, err := rand.Read(payload[8:]); err != nil {
		return fail("nonce_failed")
	}
	if n, err := conn.Write(payload); err != nil || n != len(payload) {
		return fail("udp_send_failed")
	}
	reply := make([]byte, 512)
	n, err := conn.Read(reply)
	if err != nil {
		return fail("udp_receive_failed")
	}
	if !bytes.Equal(reply[:n], payload) {
		return fail("udp_reply_mismatch")
	}
	return pass("udp_nonce_round_trip")
}
