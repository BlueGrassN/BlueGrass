//go:build windows

package winservice

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows"
)

const (
	acceptanceProbeEnv     = "BLUEGRASS_ELEVATED_TUN_PROBE"
	acceptanceProbeRunEnv  = "BLUEGRASS_ELEVATED_TUN_RUN"
	acceptanceProbeTestRun = "^TestElevatedTUNBusinessProbe$"
	acceptanceProbePrefix  = "BG_PROBE:"
	acceptanceProbeLimit   = 5 * time.Second
	acceptanceProbeOK      = "OK"
	acceptanceProbeOff     = "PROBE_DISABLED"
	acceptanceProbeInput   = "PROBE_INVALID_REQUEST"
	acceptanceProbeChild   = "PROBE_CHILD_FAILED"
	acceptanceProbeOutput  = "PROBE_INVALID_OUTPUT"
	acceptanceProbeTime    = "PROBE_DEADLINE"
	acceptanceProbeDial    = "PROBE_CONNECT_FAILED"
	acceptanceProbeDenied  = "PROBE_POLICY_DENIED"
	acceptanceProbeTLS     = "PROBE_TLS_FAILED"
	acceptanceProbeHTTP    = "PROBE_HTTP_FAILED"
	acceptanceProbeJSON    = "PROBE_HTTP_RESPONSE"
	acceptanceProbeDNS     = "PROBE_DNS_FAILED"
	acceptanceProbeSTUN    = "PROBE_STUN_FAILED"
	acceptanceProbePacket  = "PROBE_STUN_RESPONSE"
)

// These interfaces are shared with the opt-in elevated acceptance parent.
// IP is an in-memory observation only: the parent must log booleans, not IP,
// requests, results, child output, or node data.
type acceptanceProbeRequest struct {
	Mode   string `json:"mode"`
	Target string `json:"target"`
	Host   string `json:"host"`
}

type acceptanceProbeResult struct {
	Success    bool   `json:"success"`
	IP         string `json:"ip"`
	Fake       bool   `json:"fake"`
	DomainFake bool   `json:"domainFake,omitempty"` // HTTPS socket connected to a FakeIP via OS DNS.
	Code       string `json:"code"`
}

// runAcceptanceProbe never logs or persists child stdout. Transport failures
// (including expected WFP blocks) are results, not orchestration errors.
// BLUEGRASS_ELEVATED_TUN_RUN must be the absolute directory containing the
// staged helper, matching dir; it is not a boolean opt-in flag.
func runAcceptanceProbe(ctx context.Context, dir string, request acceptanceProbeRequest) (acceptanceProbeResult, error) {
	runDir := os.Getenv(acceptanceProbeRunEnv)
	if !acceptanceProbeRunDirectory(runDir, dir) {
		return acceptanceProbeFailure(acceptanceProbeOff), errors.New(acceptanceProbeOff)
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) > 4096 {
		return acceptanceProbeFailure(acceptanceProbeInput), errors.New(acceptanceProbeInput)
	}
	// Allow startup time in addition to the child's five-second network budget.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "BlueGrass.TunProbe.exe"),
		"-test.run="+acceptanceProbeTestRun, "-test.v")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	// Only the Windows runtime environment is needed. Do not pass credentials,
	// proxy configuration, profiling options, or the parent's node data.
	for _, key := range []string{"SystemRoot", "WINDIR", "SystemDrive", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, acceptanceProbeEnv+"="+string(data),
		acceptanceProbeRunEnv+"="+runDir, "GOTRACEBACK=none")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		code := acceptanceProbeChild
		if ctx.Err() != nil {
			code = acceptanceProbeTime
		}
		return acceptanceProbeFailure(code), errors.New(code)
	}
	return parseAcceptanceProbeOutput(stdout.Bytes())
}

func parseAcceptanceProbeOutput(output []byte) (acceptanceProbeResult, error) {
	invalid := func() (acceptanceProbeResult, error) {
		return acceptanceProbeFailure(acceptanceProbeOutput), errors.New(acceptanceProbeOutput)
	}
	var result acceptanceProbeResult
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte(acceptanceProbePrefix)) {
			continue
		}
		if found {
			return invalid()
		}
		found = true
		decoder := json.NewDecoder(bytes.NewReader(line[len(acceptanceProbePrefix):]))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
			return invalid()
		}
	}
	if scanner.Err() != nil || !found {
		return invalid()
	}
	switch result.Code {
	case acceptanceProbeOK, acceptanceProbeOff, acceptanceProbeInput,
		acceptanceProbeTime, acceptanceProbeDial, acceptanceProbeDenied, acceptanceProbeTLS,
		acceptanceProbeHTTP, acceptanceProbeJSON, acceptanceProbeDNS,
		acceptanceProbeSTUN, acceptanceProbePacket:
	default:
		return invalid()
	}
	if result.Success != (result.Code == acceptanceProbeOK) ||
		(!result.Success && (result.IP != "" || result.Fake || result.DomainFake)) {
		return invalid()
	}
	if result.DomainFake && (result.IP == "" || result.Fake) {
		return invalid()
	}
	if result.IP != "" {
		ip, err := netip.ParseAddr(result.IP)
		if err != nil || !acceptancePublicIPv4(ip) || result.Fake {
			return invalid()
		}
	}
	return result, nil
}

func acceptanceProbeFailure(code string) acceptanceProbeResult {
	return acceptanceProbeResult{Code: code}
}

func acceptanceNetworkFailure(err error, fallback string) acceptanceProbeResult {
	if errors.Is(err, windows.WSAEACCES) {
		return acceptanceProbeFailure(acceptanceProbeDenied)
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return acceptanceProbeFailure(acceptanceProbeDial)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return acceptanceProbeFailure(acceptanceProbeTime)
	}
	return acceptanceProbeFailure(fallback)
}

// Normal go test skips this even when acceptance variables were inherited:
// only the explicitly named helper executable and exact test selection run it.
func TestElevatedTUNBusinessProbe(t *testing.T) {
	raw := os.Getenv(acceptanceProbeEnv)
	run := flag.Lookup("test.run")
	executable, err := os.Executable()
	if err != nil || run == nil || !acceptanceProbeChildEnabled(
		os.Getenv(acceptanceProbeRunEnv), raw, executable, run.Value.String()) {
		t.Skip("explicit elevated acceptance child only")
	}
	result := acceptanceProbeFailure(acceptanceProbeInput)
	var request acceptanceProbeRequest
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) <= 4096 && decoder.Decode(&request) == nil && decoder.Decode(new(any)) == io.EOF {
		ctx, cancel := context.WithTimeout(t.Context(), acceptanceProbeLimit)
		result = performAcceptanceProbe(ctx, request)
		cancel()
	}
	// This is a private machine-readable pipe to the parent, never a test log.
	// The parent must not forward this stream or serialize the result to disk.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(acceptanceProbeOutput)
	}
	data = append(append([]byte(acceptanceProbePrefix), data...), '\n')
	if _, err := os.Stdout.Write(data); err != nil {
		t.Fatal(acceptanceProbeOutput)
	}
}

func acceptanceProbeChildEnabled(runDir, request, executable, testRun string) bool {
	return acceptanceProbeRunDirectory(runDir, filepath.Dir(executable)) && request != "" &&
		strings.EqualFold(filepath.Base(executable), "BlueGrass.TunProbe.exe") &&
		testRun == acceptanceProbeTestRun
}

func acceptanceProbeRunDirectory(runDir, expectedDir string) bool {
	return filepath.IsAbs(runDir) && filepath.IsAbs(expectedDir) &&
		strings.EqualFold(filepath.Clean(runDir), filepath.Clean(expectedDir))
}

func performAcceptanceProbe(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	// Also bound callers that do not use the child test's context.
	ctx, cancel := context.WithTimeout(ctx, acceptanceProbeLimit)
	defer cancel()
	var result acceptanceProbeResult
	switch request.Mode {
	case "https", "https-domain":
		result = acceptanceHTTPS(ctx, request)
	case "http":
		result = acceptanceHTTP(ctx, request)
	case "tls":
		result = acceptanceTLS(ctx, request)
	case "dns-udp", "dns-tcp":
		result = acceptanceDNS(ctx, request)
	case "system-dns":
		host := request.Host
		if host == "" {
			host = "example.com"
		}
		if (host != "example.com" && !strings.HasSuffix(host, ".example.com")) || request.Target != "" {
			return acceptanceProbeFailure(acceptanceProbeInput)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			result = acceptanceProbeFailure(acceptanceProbeDNS)
			break
		}
		result = acceptanceProbeResult{Success: true, Code: acceptanceProbeOK}
		for _, address := range addresses {
			result.Fake = result.Fake || acceptanceFakeIP(address.IP)
		}
	case "stun":
		result = acceptanceSTUNRequest(ctx, request)
	case "tcp":
		target, ok := acceptanceLiteralTarget(request.Target, "")
		if !ok {
			return acceptanceProbeFailure(acceptanceProbeInput)
		}
		conn, err := (&net.Dialer{Timeout: acceptanceProbeLimit}).DialContext(ctx, "tcp", target)
		if err != nil {
			result = acceptanceProbeFailure(acceptanceProbeDial)
			break
		}
		conn.Close()
		result = acceptanceProbeResult{Success: true, Code: acceptanceProbeOK}
	default:
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	if ctx.Err() != nil {
		return acceptanceProbeFailure(acceptanceProbeTime)
	}
	return result
}

// A numeric port and parsed literal address guarantee no resolver is used.
// Fixed-port modes accept either a bare IP or IP:port; TCP needs an explicit port.
func acceptanceLiteralTarget(target, fixedPort string) (string, bool) {
	if ip, err := netip.ParseAddr(target); err == nil && fixedPort != "" && ip.Zone() == "" {
		target = net.JoinHostPort(ip.String(), fixedPort)
	}
	endpoint, err := netip.ParseAddrPort(target)
	if err != nil || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" ||
		endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() {
		return "", false
	}
	if fixedPort != "" {
		_, port, err := net.SplitHostPort(target)
		if err != nil || port != fixedPort {
			return "", false
		}
	}
	return endpoint.String(), true
}

func acceptanceHTTPS(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	return acceptanceHTTPSVia(ctx, request, (&net.Dialer{Timeout: acceptanceProbeLimit}).DialContext)
}

func acceptanceHTTPSVia(ctx context.Context, request acceptanceProbeRequest, dial func(context.Context, string, string) (net.Conn, error)) acceptanceProbeResult {
	host := request.Host
	if host == "" {
		host = "api.ipify.org"
	}
	path, ok := acceptanceHTTPSPath(host)
	if !ok {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	target, literal := acceptanceLiteralTarget(request.Target, "443")
	network := "tcp"
	if request.Mode == "https-domain" {
		// Deliberately separate from the pinned-IP probe: use the default OS
		// resolver, with no proxy, so the real TUN FakeIP/domain path is tested.
		if request.Target != "" || !acceptanceChinaHost(host) {
			return acceptanceProbeFailure(acceptanceProbeInput)
		}
		target, network = net.JoinHostPort(host, "443"), "tcp4"
	} else if !literal {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	if literal && acceptanceChinaHost(host) && !acceptancePublicIPv4(netip.MustParseAddrPort(target).Addr()) {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	domainFake := false
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			// Own the whole attempt's deadline; net/http can detach its dial
			// context when considering connection reuse.
			conn, err := dial(ctx, network, target)
			if err == nil && request.Mode == "https-domain" {
				if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
					domainFake = acceptanceFakeIP(remote.IP)
				}
			}
			return conn, err
		},
		TLSClientConfig:        &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    acceptanceProbeLimit,
		ResponseHeaderTimeout:  acceptanceProbeLimit,
		MaxResponseHeaderBytes: 8192,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: acceptanceProbeLimit,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+path, nil)
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	req.Header.Set("Accept", "application/json, text/plain")
	response, err := client.Do(req)
	if err != nil {
		return acceptanceNetworkFailure(err, acceptanceProbeHTTP)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return acceptanceProbeFailure(acceptanceProbeJSON)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbeHTTP)
	}
	var ip string
	if host == "www.cloudflare.com" {
		ip, err = parseAcceptanceTrace(body)
	} else if acceptanceChinaHost(host) {
		ip, err = parseAcceptanceChinaResponse(host, body)
	} else {
		ip, err = parseAcceptanceHTTPResponse(body)
	}
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbeJSON)
	}
	return acceptanceProbeResult{Success: true, IP: ip, DomainFake: domainFake, Code: acceptanceProbeOK}
}

func acceptanceChinaHost(host string) bool {
	return host == "myip.ipip.net" || host == "4.ipw.cn"
}

// The URL path, certificate SNI and response parser are selected together from
// this allowlist. Requests cannot supply an arbitrary URL or disable TLS checks.
func acceptanceHTTPSPath(host string) (string, bool) {
	switch host {
	case "api.ipify.org", "api64.ipify.org":
		return "/?format=json", true
	case "www.cloudflare.com":
		return "/cdn-cgi/trace", true
	case "myip.ipip.net", "4.ipw.cn":
		return "/", true
	default:
		return "", false
	}
}

func parseAcceptanceChinaResponse(host string, body []byte) (string, error) {
	invalid := errors.New(acceptanceProbeJSON)
	if len(body) > 4096 || !acceptanceChinaHost(host) {
		return "", invalid
	}
	value := strings.TrimSpace(string(body))
	if host == "myip.ipip.net" {
		if !strings.HasPrefix(value, "当前 IP：") || strings.ContainsAny(value, "\r\n<>") {
			return "", invalid
		}
		address, location, ok := strings.Cut(strings.TrimPrefix(value, "当前 IP："), "来自于：")
		if !ok || strings.TrimSpace(location) == "" || strings.Contains(location, "当前 IP：") || strings.Contains(location, "来自于：") {
			return "", invalid
		}
		value = strings.TrimSpace(address)
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !acceptancePublicIPv4(ip) {
		return "", invalid
	}
	return ip.String(), nil
}

func parseAcceptanceTrace(body []byte) (string, error) {
	if len(body) > 4096 {
		return "", errors.New(acceptanceProbeJSON)
	}
	var address string
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "ip=") {
			continue
		}
		if address != "" {
			return "", errors.New(acceptanceProbeJSON)
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(strings.TrimPrefix(line, "ip=")))
		if err != nil || !acceptancePublicIPv4(ip) {
			return "", errors.New(acceptanceProbeJSON)
		}
		address = ip.String()
	}
	if address == "" {
		return "", errors.New(acceptanceProbeJSON)
	}
	return address, nil
}

func TestAcceptanceTraceResponse(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{"ip=8.8.8.8\nwarp=off\n", true},
		{"warp=off\n", false},
		{"ip=8.8.8.8\nip=1.1.1.1\n", false},
		{"ip=127.0.0.1\n", false},
		{"ip=not-an-ip\n", false},
	} {
		_, err := parseAcceptanceTrace([]byte(tc.body))
		if (err == nil) != tc.ok {
			t.Fatal("unexpected trace parsing result")
		}
	}
}

func TestAcceptanceDeniedIsDistinctFromApplicationFailure(t *testing.T) {
	wrapped := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", windows.WSAEACCES)}
	if result := acceptanceNetworkFailure(wrapped, acceptanceProbeHTTP); result.Code != acceptanceProbeDenied {
		t.Fatal("access denial was not preserved")
	}
	if result := acceptanceNetworkFailure(errors.New("fixture response failure"), acceptanceProbeHTTP); result.Code != acceptanceProbeHTTP {
		t.Fatal("application failure was classified as blocking")
	}
	for _, err := range []error{x509.UnknownAuthorityError{}, &tls.CertificateVerificationError{Err: x509.HostnameError{}}, io.EOF, windows.WSAECONNRESET} {
		if result := acceptanceNetworkFailure(err, acceptanceProbeHTTP); result.Code != acceptanceProbeHTTP {
			t.Fatal("TLS/HTTP failure after connect was classified as WFP blocking")
		}
	}
}

// Gateway reachability is application-level evidence: a locally accepted TCP
// connection is insufficient. Any final HTTP status is useful (including an
// authentication error); do not follow redirects or consume response content.
func acceptanceHTTP(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	target, ok := acceptanceLiteralTarget(request.Target, "80")
	if !ok || request.Host != "" {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: acceptanceProbeLimit}).DialContext(ctx, "tcp", target)
		},
		ResponseHeaderTimeout:  acceptanceProbeLimit,
		MaxResponseHeaderBytes: 8192,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: acceptanceProbeLimit,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+"/", nil)
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	response, err := client.Do(req)
	if err != nil {
		return acceptanceNetworkFailure(err, acceptanceProbeHTTP)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		return acceptanceProbeFailure(acceptanceProbeJSON)
	}
	return acceptanceProbeResult{Success: true, Code: acceptanceProbeOK}
}

// TLS acceptance requires peer traffic and certificate verification. A locally
// accepted TCP SYN in the TUN stack is not evidence of a working IPv6 route.
func acceptanceTLS(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	target, ok := acceptanceLiteralTarget(request.Target, "443")
	if !ok || request.Host == "" || len(request.Host) > 253 ||
		strings.ContainsAny(request.Host, "/\\:@ \t\r\n") {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	conn, err := (&net.Dialer{Timeout: acceptanceProbeLimit}).DialContext(ctx, "tcp", target)
	if err != nil {
		return acceptanceNetworkFailure(err, acceptanceProbeDial)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return acceptanceProbeFailure(acceptanceProbeTLS)
	}
	secure := tls.Client(conn, &tls.Config{ServerName: request.Host, MinVersion: tls.VersionTLS12})
	if err := secure.HandshakeContext(ctx); err != nil {
		return acceptanceNetworkFailure(err, acceptanceProbeTLS)
	}
	return acceptanceProbeResult{Success: true, Code: acceptanceProbeOK}
}

func acceptanceDNS(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	if request.Host != "" && request.Host != "example.com" {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	target := request.Target
	if target == "" {
		target = "8.8.8.8:53"
	}
	target, ok := acceptanceLiteralTarget(target, "53")
	if !ok {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	message := new(dns.Msg)
	message.SetQuestion("example.com.", dns.TypeA)
	client := &dns.Client{Net: strings.TrimPrefix(request.Mode, "dns-"), Timeout: acceptanceProbeLimit}
	response, _, err := client.ExchangeContext(ctx, message, target)
	if err != nil || response == nil || !response.Response || response.Id != message.Id ||
		response.Opcode != dns.OpcodeQuery || response.Rcode != dns.RcodeSuccess || response.Truncated ||
		len(response.Question) != 1 || response.Question[0] != message.Question[0] {
		return acceptanceProbeFailure(acceptanceProbeDNS)
	}
	result := acceptanceProbeFailure(acceptanceProbeDNS)
	for _, answer := range response.Answer {
		if a, ok := answer.(*dns.A); ok && a.Hdr.Class == dns.ClassINET && a.A.To4() != nil {
			result.Success = true
			result.Code = acceptanceProbeOK
			result.Fake = result.Fake || acceptanceFakeIP(a.A)
		}
	}
	return result
}

func acceptanceFakeIP(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 198 && v4[1]&0xfe == 18
}

func acceptanceSTUNRequest(ctx context.Context, request acceptanceProbeRequest) acceptanceProbeResult {
	return acceptanceSTUNVia(ctx, request, (&net.Dialer{Timeout: acceptanceProbeLimit}).DialContext)
}

func acceptanceSTUNVia(ctx context.Context, request acceptanceProbeRequest, dial func(context.Context, string, string) (net.Conn, error)) acceptanceProbeResult {
	// Official public endpoint: stun.cloudflare.com:3478.
	// https://developers.cloudflare.com/realtime/sfu/get-started/connection-patterns/
	// The parent resolves it before WFP activation; this child accepts literals only.
	target, ok := acceptanceLiteralTarget(request.Target, "3478")
	if !ok || request.Host != "" && request.Host != "stun.cloudflare.com" {
		return acceptanceProbeFailure(acceptanceProbeInput)
	}
	// RFC 8489: 20-byte Binding Request, including a fresh 96-bit transaction ID.
	packet := make([]byte, 20)
	binary.BigEndian.PutUint16(packet, 0x0001)
	binary.BigEndian.PutUint32(packet[4:], 0x2112a442)
	if _, err := rand.Read(packet[8:]); err != nil {
		return acceptanceProbeFailure(acceptanceProbeSTUN)
	}
	conn, err := dial(ctx, "udp", target)
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbeDial)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return acceptanceProbeFailure(acceptanceProbeSTUN)
	}
	// A socket deadline bounds Read/Write; cancellation also interrupts them.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	response := make([]byte, 65535)
	var n int
	for delay := 500 * time.Millisecond; ; delay *= 2 {
		if ctx.Err() != nil {
			return acceptanceProbeFailure(acceptanceProbeTime)
		}
		if n, err := conn.Write(packet); err != nil || n != len(packet) {
			return acceptanceProbeFailure(acceptanceProbeSTUN)
		}
		until := time.Now().Add(delay)
		if until.After(deadline) {
			until = deadline
		}
		_ = conn.SetReadDeadline(until)
		n, err = conn.Read(response)
		if err == nil {
			break
		}
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() || !time.Now().Before(deadline) {
			return acceptanceProbeFailure(acceptanceProbeSTUN)
		}
	}
	ip, err := parseSTUNAddress(response[:n], packet[8:])
	if err != nil {
		return acceptanceProbeFailure(acceptanceProbePacket)
	}
	address, err := netip.ParseAddr(ip)
	if err != nil || !acceptancePublicIPv4(address) {
		return acceptanceProbeFailure(acceptanceProbePacket)
	}
	return acceptanceProbeResult{Success: true, IP: ip, Code: acceptanceProbeOK}
}

func parseSTUNAddress(packet, transactionID []byte) (string, error) {
	invalid := errors.New(acceptanceProbePacket)
	if len(packet) < 20 || len(transactionID) != 12 ||
		binary.BigEndian.Uint16(packet) != 0x0101 ||
		binary.BigEndian.Uint32(packet[4:]) != 0x2112a442 ||
		!bytes.Equal(packet[8:20], transactionID) {
		return "", invalid
	}
	size := int(binary.BigEndian.Uint16(packet[2:]))
	if size%4 != 0 || size != len(packet)-20 {
		return "", invalid
	}
	var mapped, xorMapped string
	for offset := 20; offset < len(packet); {
		if len(packet)-offset < 4 {
			return "", invalid
		}
		kind := binary.BigEndian.Uint16(packet[offset:])
		size := int(binary.BigEndian.Uint16(packet[offset+2:]))
		start := offset + 4
		padded := (size + 3) &^ 3
		if padded > len(packet)-start {
			return "", invalid
		}
		value := packet[start : start+size]
		offset = start + padded
		if kind != 0x0001 && kind != 0x0020 {
			continue
		}
		if len(value) < 4 || value[0] != 0 {
			return "", invalid
		}
		if value[1] == 2 && len(value) == 20 {
			continue // Well-formed IPv6 is not an IPv4 observation.
		}
		if value[1] != 1 || len(value) != 8 {
			return "", invalid
		}
		var ip [4]byte
		copy(ip[:], value[4:])
		if kind == 0x0020 {
			for i := range ip {
				ip[i] ^= packet[4+i]
			}
			if xorMapped == "" {
				xorMapped = netip.AddrFrom4(ip).String()
			}
		} else if mapped == "" {
			mapped = netip.AddrFrom4(ip).String()
		}
	}
	if xorMapped != "" {
		return xorMapped, nil
	}
	if mapped != "" {
		return mapped, nil
	}
	return "", invalid
}

func parseAcceptanceHTTPResponse(body []byte) (string, error) {
	invalid := errors.New(acceptanceProbeJSON)
	if len(body) > 4096 {
		return "", invalid
	}
	// The ipify response is exactly one string-valued "ip" field. Token parsing
	// rejects duplicate fields, null, trailing JSON, and unexpected payload data.
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", invalid
	}
	token, err = decoder.Token()
	if err != nil || token != "ip" {
		return "", invalid
	}
	token, err = decoder.Token()
	value, ok := token.(string)
	if err != nil || !ok {
		return "", invalid
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || decoder.Decode(new(any)) != io.EOF {
		return "", invalid
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !acceptancePublicIPv4(ip) {
		return "", invalid
	}
	return ip.String(), nil
}

func acceptancePublicIPv4(ip netip.Addr) bool {
	if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func TestAcceptanceProbeSTUNParsing(t *testing.T) {
	transaction := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	// Synthetic RFC 5737 address; never a live public-IP observation.
	mapped := []byte{0, 1, 0x12, 0x34, 192, 0, 2, 1}
	xorMapped := []byte{0, 1, 0x33, 0x26, 0xe1, 0x12, 0xa6, 0x43}
	attribute := func(kind uint16, value []byte) []byte {
		b := make([]byte, 4+(len(value)+3)&^3)
		binary.BigEndian.PutUint16(b, kind)
		binary.BigEndian.PutUint16(b[2:], uint16(len(value)))
		copy(b[4:], value)
		return b
	}
	message := func(attrs ...[]byte) []byte {
		b := make([]byte, 20)
		binary.BigEndian.PutUint16(b, 0x0101)
		binary.BigEndian.PutUint32(b[4:], 0x2112a442)
		copy(b[8:], transaction)
		for _, attr := range attrs {
			b = append(b, attr...)
		}
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)-20))
		return b
	}
	change := func(b []byte, at int, value byte) []byte {
		b[at] = value
		return b
	}
	valid := func() []byte { return message(attribute(0x0020, xorMapped)) }
	ipv6 := make([]byte, 20)
	ipv6[1] = 2
	for _, tc := range []struct {
		name   string
		packet []byte
		tx     []byte
		want   string
	}{
		{"xor", valid(), transaction, "192.0.2.1"},
		{"mapped", message(attribute(1, mapped)), transaction, "192.0.2.1"},
		{"xor-preferred", message(attribute(1, []byte{0, 1, 0, 1, 192, 0, 2, 2}), attribute(0x20, xorMapped)), transaction, "192.0.2.1"},
		{"padding", message(attribute(0x8022, []byte{1}), attribute(0x20, xorMapped)), transaction, "192.0.2.1"},
		{"short-header", valid()[:19], transaction, ""},
		{"transaction", change(valid(), 8, 99), transaction, ""},
		{"short-transaction", valid(), transaction[:11], ""},
		{"magic", change(valid(), 4, 0), transaction, ""},
		{"error-response", change(valid(), 1, 0x11), transaction, ""},
		{"request-type", change(valid(), 0, 0), transaction, ""},
		{"length-overflow", change(valid(), 2, 0xff), transaction, ""},
		{"unaligned-length", change(valid(), 3, 11), transaction, ""},
		{"attribute-overflow", change(valid(), 23, 12), transaction, ""},
		{"trailing-byte", append(valid(), 0), transaction, ""},
		{"bad-attribute-after-address", message(attribute(0x20, xorMapped), []byte{0, 1, 0, 8}), transaction, ""},
		{"short-address", message(attribute(0x20, xorMapped[:4])), transaction, ""},
		{"reserved-byte", message(attribute(0x20, []byte{1, 1, 0, 0, 1, 2, 3, 4})), transaction, ""},
		{"unknown-family", message(attribute(0x20, []byte{0, 3, 0, 0, 1, 2, 3, 4})), transaction, ""},
		{"ipv6", message(attribute(0x20, ipv6)), transaction, ""},
		{"ipv6-mapped", message(attribute(1, ipv6)), transaction, ""},
		{"ipv6-then-ipv4", message(attribute(0x20, ipv6), attribute(0x20, xorMapped)), transaction, "192.0.2.1"},
		{"ipv6-short", message(attribute(0x20, ipv6[:16])), transaction, ""},
		{"no-address", message(attribute(0x8022, []byte("test"))), transaction, ""},
		{"empty", message(), transaction, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSTUNAddress(tc.packet, tc.tx)
			if (err == nil) != (tc.want != "") || got != tc.want {
				t.Fatal("STUN parse outcome mismatch")
			}
			if err != nil && err.Error() != "PROBE_STUN_RESPONSE" {
				t.Fatal("STUN error must be a constant code")
			}
		})
	}
}

func TestAcceptanceProbeHTTPParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"public", `{"ip":"8.8.8.8"}`, true},
		{"whitespace", " \n{\"ip\":\"1.1.1.1\"}\n", true},
		{"private", `{"ip":"10.0.0.1"}`, false},
		{"loopback", `{"ip":"127.0.0.1"}`, false},
		{"link-local", `{"ip":"169.254.1.1"}`, false},
		{"shared", `{"ip":"100.64.0.1"}`, false},
		{"fake", `{"ip":"198.19.255.255"}`, false},
		{"documentation", `{"ip":"192.0.2.1"}`, false},
		{"unspecified", `{"ip":"0.0.0.0"}`, false},
		{"multicast", `{"ip":"224.0.0.1"}`, false},
		{"reserved", `{"ip":"240.0.0.1"}`, false},
		{"ipv6", `{"ip":"2606:4700:4700::1111"}`, false},
		{"mapped-ipv6", `{"ip":"::ffff:8.8.8.8"}`, false},
		{"missing", `{}`, false},
		{"null", `{"ip":null}`, false},
		{"number", `{"ip":123}`, false},
		{"duplicate", `{"ip":"8.8.8.8","ip":"1.1.1.1"}`, false},
		{"unknown", `{"ip":"8.8.8.8","extra":"node-data"}`, false},
		{"trailing-json", `{"ip":"8.8.8.8"}{}`, false},
		{"malformed", `{"ip":`, false},
		{"oversized", `{"ip":"8.8.8.8"}` + strings.Repeat(" ", 4096), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip, err := parseAcceptanceHTTPResponse([]byte(tc.body))
			if (err == nil) != tc.ok || (ip != "") != tc.ok {
				t.Fatal("HTTP parse outcome mismatch")
			}
			if err != nil && err.Error() != "PROBE_HTTP_RESPONSE" {
				t.Fatal("HTTP error must be a constant code")
			}
		})
	}
}

func TestAcceptanceChinaDetectorParsing(t *testing.T) {
	for _, tc := range []struct {
		name, host, body string
		ok               bool
	}{
		{"ipip", "myip.ipip.net", "当前 IP：8.8.8.8  来自于：中国 测试\n", true},
		{"ipw", "4.ipw.cn", "1.1.1.1\r\n", true},
		{"missing-prefix", "myip.ipip.net", "8.8.8.8", false},
		{"missing-location", "myip.ipip.net", "当前 IP：8.8.8.8", false},
		{"duplicate", "myip.ipip.net", "当前 IP：8.8.8.8  来自于：测试\n当前 IP：1.1.1.1  来自于：测试", false},
		{"html", "4.ipw.cn", "<html>8.8.8.8</html>", false},
		{"two-addresses", "4.ipw.cn", "8.8.8.8\n1.1.1.1", false},
		{"json", "4.ipw.cn", `{"ip":"8.8.8.8"}`, false},
		{"private", "4.ipw.cn", "10.0.0.1", false},
		{"fake", "4.ipw.cn", "198.18.0.1", false},
		{"ipv6", "4.ipw.cn", "2001:4860::8888", false},
		{"mapped", "4.ipw.cn", "::ffff:8.8.8.8", false},
		{"unlisted-host", "4.ipw.cn.example.com", "8.8.8.8", false},
		{"oversized", "4.ipw.cn", "8.8.8.8" + strings.Repeat(" ", 4096), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ip, err := parseAcceptanceChinaResponse(tc.host, []byte(tc.body))
			if (err == nil) != tc.ok || (ip != "") != tc.ok {
				t.Fatal("China detector parse outcome mismatch")
			}
			if err != nil && err.Error() != acceptanceProbeJSON {
				t.Fatal("detector error must be sanitized")
			}
		})
	}
}

func TestAcceptanceHTTPSHostAllowlist(t *testing.T) {
	for _, tc := range []struct {
		host, path string
		china      bool
	}{
		{"api.ipify.org", "/?format=json", false},
		{"api64.ipify.org", "/?format=json", false},
		{"www.cloudflare.com", "/cdn-cgi/trace", false},
		{"myip.ipip.net", "/", true},
		{"4.ipw.cn", "/", true},
		{"4.ipw.cn.example.com", "", false},
		{"myip.ipip.net:443", "", false},
		{"https://4.ipw.cn/", "", false},
		{"4.ipw.cn/path", "", false},
		{"", "", false},
	} {
		path, ok := acceptanceHTTPSPath(tc.host)
		if path != tc.path || ok != (tc.path != "") || acceptanceChinaHost(tc.host) != tc.china {
			t.Fatal("HTTPS URL/SNI/parser allowlist mismatch")
		}
	}
}

func TestAcceptanceProbeOutputParsing(t *testing.T) {
	success := `BG_PROBE:{"success":true,"ip":"","fake":false,"code":"OK"}`
	for _, tc := range []struct {
		name, output string
		ok           bool
	}{
		{"success", success, true},
		{"verbose", "=== RUN   TestElevatedTUNBusinessProbe\r\n" + success + "\r\n--- PASS: TestElevatedTUNBusinessProbe\nPASS\n", true},
		{"blocked", `BG_PROBE:{"success":false,"code":"PROBE_TLS_FAILED"}`, true},
		{"fake", `BG_PROBE:{"success":true,"fake":true,"code":"OK"}`, true},
		{"public", `BG_PROBE:{"success":true,"ip":"8.8.8.8","code":"OK"}`, true},
		{"domain-fake", `BG_PROBE:{"success":true,"ip":"8.8.8.8","domainFake":true,"code":"OK"}`, true},
		{"domain-fake-no-ip", `BG_PROBE:{"success":true,"domainFake":true,"code":"OK"}`, false},
		{"domain-fake-failure", `BG_PROBE:{"domainFake":true,"code":"PROBE_HTTP_FAILED"}`, false},
		{"missing", "PASS\n", false},
		{"duplicate", success + "\n" + success, false},
		{"malformed", `BG_PROBE:{`, false},
		{"trailing-json", success + "{}", false},
		{"raw-error", `BG_PROBE:{"code":"dial tcp node-data"}`, false},
		{"unknown-field", `BG_PROBE:{"success":true,"code":"OK","node":"secret"}`, false},
		{"unsuccessful-ok", `BG_PROBE:{"success":false,"code":"OK"}`, false},
		{"successful-error", `BG_PROBE:{"success":true,"code":"PROBE_TLS_FAILED"}`, false},
		{"failure-address", `BG_PROBE:{"ip":"8.8.8.8","code":"PROBE_TLS_FAILED"}`, false},
		{"failure-fake", `BG_PROBE:{"fake":true,"code":"PROBE_DNS_FAILED"}`, false},
		{"invalid-address", `BG_PROBE:{"success":true,"ip":"node-data","code":"OK"}`, false},
		{"private-address", `BG_PROBE:{"success":true,"ip":"10.0.0.1","code":"OK"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseAcceptanceProbeOutput([]byte(tc.output))
			if (err == nil) != tc.ok {
				t.Fatal("child protocol outcome mismatch")
			}
			if err != nil && (err.Error() != acceptanceProbeOutput || result.IP != "" ||
				result.Success || result.Fake || result.Code != acceptanceProbeOutput) {
				t.Fatal("child protocol error was not sanitized")
			}
		})
	}
}

func TestAcceptanceProbeTargetParsing(t *testing.T) {
	for _, tc := range []struct {
		name, target, port string
		ok                 bool
	}{
		{"bare-ip", "192.0.2.1", "443", true},
		{"endpoint", "192.0.2.1:443", "443", true},
		{"http-gateway", "192.0.2.1:80", "80", true},
		{"bare-http-gateway", "192.0.2.1", "80", true},
		{"ipv6-tls", "[2606:4700:4700::1111]:443", "443", true},
		{"bare-ipv6", "2001:db8::1", "443", true},
		{"tcp-gateway", "192.0.2.1:12345", "", true},
		{"stun", "192.0.2.1:3478", "3478", true},
		{"dns", "8.8.8.8:53", "53", true},
		{"hostname", "api.ipify.org:443", "443", false},
		{"stun-hostname", "stun.cloudflare.com:3478", "3478", false},
		{"missing", "", "443", false},
		{"url", "https://192.0.2.1:443", "443", false},
		{"wrong-port", "192.0.2.1:80", "443", false},
		{"named-port", "192.0.2.1:https", "443", false},
		{"zero-port", "192.0.2.1:0", "", false},
		{"overflow-port", "192.0.2.1:65536", "", false},
		{"tcp-missing-port", "192.0.2.1", "", false},
		{"unspecified", "0.0.0.0:443", "443", false},
		{"multicast", "224.0.0.1:443", "443", false},
		{"zone", "[fe80::1%interface]:443", "443", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, ok := acceptanceLiteralTarget(tc.target, tc.port)
			if ok != tc.ok || (target != "") != tc.ok {
				t.Fatal("literal target outcome mismatch")
			}
		})
	}
}

func TestAcceptanceProbeFakeParsing(t *testing.T) {
	for _, tc := range []struct {
		name, address string
		fake          bool
	}{
		{"below", "198.17.255.255", false},
		{"start", "198.18.0.0", true},
		{"middle", "198.19.0.1", true},
		{"end", "198.19.255.255", true},
		{"above", "198.20.0.0", false},
		{"ipv6", "2001:db8::1", false},
		{"invalid", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if acceptanceFakeIP(net.ParseIP(tc.address)) != tc.fake {
				t.Fatal("fake address classification mismatch")
			}
		})
	}
}

func TestAcceptanceProbeChildGate(t *testing.T) {
	for _, tc := range []struct {
		name, runDir, request, executable, testRun string
		ok                                         bool
	}{
		{"explicit", `C:\acceptance`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, true},
		{"normalized-directory", `c:/ACCEPTANCE/`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, true},
		{"normal-go-test", `C:\acceptance`, "{}", `C:\acceptance\winservice.test.exe`, "", false},
		{"inherited-env", `C:\acceptance`, "{}", `C:\acceptance\winservice.test.exe`, acceptanceProbeTestRun, false},
		{"broad-selection", `C:\acceptance`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, "Test", false},
		{"no-selection", `C:\acceptance`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, "", false},
		{"no-opt-in", "", "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"legacy-flag", "1", "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"relative-directory", "acceptance", "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"drive-relative-directory", `C:acceptance`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"different-directory", `C:\other`, "{}", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"prefix-is-not-directory", `C:\acceptance`, "{}", `C:\acceptance-other\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"relative-executable", `C:\acceptance`, "{}", `BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
		{"no-request", `C:\acceptance`, "", `C:\acceptance\BlueGrass.TunProbe.exe`, acceptanceProbeTestRun, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if acceptanceProbeChildEnabled(tc.runDir, tc.request, tc.executable, tc.testRun) != tc.ok {
				t.Fatal("child opt-in guard mismatch")
			}
		})
	}
}
