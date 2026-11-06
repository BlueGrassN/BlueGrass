//go:build windows && amd64

package winservice

// Explicit opt-in administrator acceptance. Ordinary go test skips every host
// mutation. The launcher starts an independent recovery watchdog first.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"bluegrass/native/core/engine"
	"bluegrass/native/core/entrydns"
	"bluegrass/native/core/exitprobe"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
	"github.com/miekg/dns"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const acceptanceAPI = "https://client-api.sunip.vip/api/client/v1"
const acceptanceAccount = "elevated-acceptance"
const acceptanceRoutingEnv = "BLUEGRASS_ELEVATED_TUN_ROUTING"

func acceptanceRoutingMode() (routingpolicy.Mode, error) {
	return routingpolicy.Normalize(os.Getenv(acceptanceRoutingEnv))
}

func TestAcceptanceRoutingEnvironment(t *testing.T) {
	for _, tc := range []struct {
		value, want string
	}{
		{"", "global"}, {"global", "global"}, {"bypass_cn", "bypass_cn"},
		{"GLOBAL", ""}, {" bypass_cn", ""}, {"direct", ""},
	} {
		t.Setenv(acceptanceRoutingEnv, tc.value)
		mode, err := acceptanceRoutingMode()
		if string(mode) != tc.want || (err == nil) != (tc.want != "") {
			t.Fatal("acceptance routing environment was not strictly normalized")
		}
	}
}

func acceptanceRunDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("BLUEGRASS_ELEVATED_TUN_RUN")
	if dir == "" {
		t.Skip("explicit elevated host acceptance only")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("ABSOLUTE_RUN_DIRECTORY_REQUIRED")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("ADMINISTRATOR_REQUIRED")
	}
	return dir
}

func acceptanceEvent(dir, stage string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["stage"], fields["time"] = stage, time.Now().UTC().Format(time.RFC3339Nano)
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		_ = json.NewEncoder(f).Encode(fields)
		_ = f.Close()
	}
}

func acceptanceWrite(dir, name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), b, 0600)
}

func acceptanceCheck(t *testing.T, dir, stage string, err error) {
	t.Helper()
	if err == nil {
		acceptanceEvent(dir, stage, map[string]any{"success": true})
		return
	}
	code := "OPERATION_FAILED"
	message := err.Error()
	if len(message) < 100 && strings.Trim(message, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") == "" {
		code = message
	}
	var errno syscall.Errno
	errors.As(err, &errno)
	causes := []string{}
	var collect func(error)
	collect = func(value error) {
		if value == nil || len(causes) > 16 {
			return
		}
		message := value.Error()
		if len(message) < 100 && strings.Trim(message, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") == "" {
			causes = append(causes, message)
		}
		if joined, ok := value.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				collect(child)
			}
		} else {
			collect(errors.Unwrap(value))
		}
	}
	collect(err)
	acceptanceEvent(dir, stage, map[string]any{"success": false, "code": code, "errno": uint32(errno), "causes": causes})
	t.Fatalf("%s: %s (errno=%d)", stage, code, errno)
}

// Read-only object query before acquiring the production manager. New itself
// reconciles stale state, so it must never be used as a presence probe.
func acceptanceWFP() (bool, error) {
	dll := windows.NewLazySystemDLL("fwpuclnt.dll")
	var handle windows.Handle
	r, _, _ := dll.NewProc("FwpmEngineOpen0").Call(0, 10, 0, 0, uintptr(unsafe.Pointer(&handle)))
	if r != 0 {
		return false, syscall.Errno(r)
	}
	defer dll.NewProc("FwpmEngineClose0").Call(uintptr(handle))
	present := false
	for _, item := range []struct {
		name, guid string
		missing    uintptr
	}{
		{"FwpmProviderGetByKey0", winprotect.ProviderGUID, 0x80320005},
		{"FwpmSubLayerGetByKey0", winprotect.SublayerGUID, 0x80320007},
	} {
		key, e := windows.GUIDFromString(item.guid)
		if e != nil {
			return false, e
		}
		var object uintptr
		r, _, _ := dll.NewProc(item.name).Call(uintptr(handle), uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&object)))
		if r == 0 {
			present = true
			dll.NewProc("FwpmFreeMemory0").Call(uintptr(unsafe.Pointer(&object)))
		} else if r != item.missing {
			return false, syscall.Errno(r)
		}
	}
	return present, nil
}

type acceptanceOwned struct {
	PID       int    `json:"pid"`
	Run       string `json:"run"`
	LogicalID string `json:"logicalId"`
}

func acceptancePreflight(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "watchdog-ready.json")); err != nil {
		return errors.New("WATCHDOG_NOT_READY")
	}
	if _, err := os.Stat(filepath.Join(dir, "owned.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("RUN_ALREADY_CLAIMED")
	}
	present, err := acceptanceWFP()
	if err != nil {
		return err
	}
	if present {
		return errors.New("EXISTING_BLUEGRASS_WFP")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if strings.EqualFold(iface.Name, "BlueGrass") {
			return errors.New("EXISTING_BLUEGRASS_ADAPTER")
		}
	}
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "BlueGrass", "state.json"))
	if err == nil {
		var state struct {
			ProtectIntent bool `json:"protectIntent"`
		}
		if json.Unmarshal(data, &state) != nil || state.ProtectIntent {
			return errors.New("EXISTING_BLUEGRASS_INTENT")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type acceptanceNode struct {
	ID       string `json:"id"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Expires  string `json:"expires_at"`
}
type acceptanceClient struct {
	client *http.Client
	token  string
}

func (c *acceptanceClient) request(ctx context.Context, path string, body any, result any) error {
	var reader io.Reader
	method := "GET"
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
		method = "POST"
	}
	r, err := http.NewRequestWithContext(ctx, method, acceptanceAPI+path, reader)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(r)
	if err != nil {
		return errors.New("API_TRANSPORT_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("API_HTTP_%d", response.StatusCode)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope) != nil || !envelope.Success {
		return errors.New("API_RESPONSE_INVALID")
	}
	if result != nil {
		return json.Unmarshal(envelope.Data, result)
	}
	return nil
}
func acceptanceLogin(ctx context.Context, accountFile string) (*acceptanceClient, []acceptanceNode, error) {
	data, err := os.ReadFile(accountFile)
	if err != nil {
		return nil, nil, errors.New("TEST_ACCOUNT_UNAVAILABLE")
	}
	lines := strings.Split(strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff"), "\n")
	var values []string
	for _, s := range lines {
		if strings.TrimSpace(s) != "" {
			values = append(values, s)
		}
	}
	if len(values) != 2 {
		return nil, nil, errors.New("TEST_ACCOUNT_FORMAT")
	}
	c := &acceptanceClient{client: &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_REFUSED") }}}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, nil, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	deviceID := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	var session struct {
		AccessToken string `json:"access_token"`
	}
	err = c.request(ctx, "/auth/login", map[string]string{"username": strings.TrimSpace(values[0]), "password": values[1], "device_id": deviceID, "device_name": "BlueGrass elevated TUN acceptance"}, &session)
	if err != nil {
		return nil, nil, err
	}
	c.token = session.AccessToken
	if c.token == "" {
		return nil, nil, errors.New("API_TOKEN_MISSING")
	}
	var result []acceptanceNode
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		query := url.Values{"per_page": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Pagination struct {
				More bool   `json:"has_more"`
				Next string `json:"next_cursor"`
			} `json:"pagination"`
		}
		if err = c.request(ctx, "/nodes?"+query.Encode(), nil, &list); err != nil {
			return c, nil, err
		}
		for _, item := range list.Items {
			var node acceptanceNode
			if err = c.request(ctx, "/nodes/"+url.PathEscape(item.ID), nil, &node); err != nil {
				return c, nil, err
			}
			result = append(result, node)
		}
		if !list.Pagination.More {
			return c, result, nil
		}
		cursor = list.Pagination.Next
		if cursor == "" || seen[cursor] {
			return c, nil, errors.New("API_CURSOR_INVALID")
		}
		seen[cursor] = true
	}
	return c, nil, errors.New("API_PAGE_LIMIT")
}

func acceptanceSelect(ctx context.Context, dir string, nodes []acceptanceNode) (engine.Profile, string, error) {
	network, err := tunengine.SystemNetwork(ctx)
	if err != nil {
		return engine.Profile{}, "", errors.New("PRECHECK_NETWORK_UNAVAILABLE")
	}
	for i, n := range nodes {
		if n.Protocol != "socks5" {
			continue
		}
		ip, err := entrydns.ResolvePhysical(ctx, n.Host, entrydns.Policy{}, network.EntryDNS)
		if err != nil || !ip.Is4() || n.Port < 1 || n.Port > 65535 {
			acceptanceEvent(dir, "resource_entry_precheck", map[string]any{"ordinal": i + 1, "success": false})
			continue
		}
		deadline, err := time.Parse(time.RFC3339, n.Expires)
		if err != nil || !deadline.After(time.Now()) {
			continue
		}
		config := protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: n.Username, Password: n.Password}}
		probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		out, err := protocolconfig.NewOutbound(probeCtx, config, netip.AddrPortFrom(ip, uint16(n.Port)))
		observed := ""
		if err == nil {
			observed, err = exitprobe.Probe(probeCtx, out.DialContext, exitprobe.Options{})
			_ = out.Close()
		}
		cancel()
		acceptanceEvent(dir, "resource_precheck", map[string]any{"ordinal": i + 1, "success": err == nil})
		if err != nil {
			continue
		}
		fingerprint := sha256.Sum256([]byte(n.Protocol + ":" + n.ID))
		return engine.Profile{Key: engine.ResourceKey{Environment: acceptanceAPI, DataSource: "api_v2", AccountKey: acceptanceAccount, Kind: "subscription", SourceID: fmt.Sprintf("resource-%d", i+1)}, Protocol: n.Protocol, Entry: engine.Entry{Host: n.Host, Port: n.Port}, Auth: engine.Auth{Username: n.Username, Password: n.Password}, ValidUntilUTC: &deadline, ConfigVersion: 1, ConfigFingerprint: hex.EncodeToString(fingerprint[:]), Mode: "tun"}, observed, nil
	}
	return engine.Profile{}, "", errors.New("NO_WORKING_TEST_RESOURCE")
}

// Read-only live prerequisite check: no service, TUN, WFP or system-proxy changes.
func TestAcceptanceProxyUDPBaseline(t *testing.T) {
	account := os.Getenv("BLUEGRASS_PROXY_UDP_ACCOUNT")
	if account == "" {
		t.Skip("explicit live account check only")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, nodes, err := acceptanceLogin(ctx, account)
	if client != nil {
		defer func() {
			c, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if client.request(c, "/auth/logout", map[string]any{}, nil) != nil {
				t.Error("LOGOUT_FAILED")
			}
		}()
	}
	if err != nil {
		t.Fatal("LOGIN_FAILED")
	}
	profile, expected, err := acceptanceSelect(ctx, dir, nodes)
	if err != nil {
		t.Fatal("RESOURCE_UNAVAILABLE")
	}
	stun, err := acceptanceIPv4(ctx, "stun.cloudflare.com")
	if err != nil {
		t.Fatal("STUN_RESOLVE_FAILED")
	}
	rules, err := routingpolicy.Load()
	if err != nil {
		t.Fatal("RULES_UNAVAILABLE")
	}
	t.Logf("stunLiteralDirect=%t", rules.IsCN(netip.MustParseAddr(stun)))
	network, err := tunengine.SystemNetwork(ctx)
	if err != nil {
		t.Fatal("NETWORK_UNAVAILABLE")
	}
	ip, err := entrydns.ResolvePhysical(ctx, profile.Entry.Host, entrydns.Policy{}, network.EntryDNS)
	if err != nil {
		t.Fatal("ENTRY_UNAVAILABLE")
	}
	out, err := protocolconfig.NewOutbound(ctx, protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: profile.Auth.Username, Password: profile.Auth.Password}}, netip.AddrPortFrom(ip, uint16(profile.Entry.Port)))
	if err != nil {
		t.Fatal("OUTBOUND_UNAVAILABLE")
	}
	defer out.Close()
	probeCtx, done := context.WithTimeout(ctx, 8*time.Second)
	defer done()
	result := acceptanceSTUNVia(probeCtx, acceptanceProbeRequest{Mode: "stun", Target: net.JoinHostPort(stun, "3478")}, out.DialContext)
	t.Logf("proxyUDP=%t code=%s matchesSelected=%t", result.Success, result.Code, result.IP == expected)
	if !result.Success || result.IP != expected {
		t.Fatal("PROXY_UDP_UNAVAILABLE_OR_EXIT_MISMATCH")
	}
	for _, host := range []string{"myip.ipip.net", "4.ipw.cn"} {
		shortCtx, shortDone := context.WithTimeout(ctx, 3*time.Second)
		started := time.Now()
		_, shortErr := tunengine.LookupIPv4TCP(shortCtx, out.DialContext, "1.1.1.1:53", host)
		shortDone()
		t.Logf("host=%s shortProxyDNS=%t milliseconds=%d", host, shortErr == nil, time.Since(started).Milliseconds())
		started = time.Now()
		addresses, err := tunengine.LookupIPv4TCP(ctx, out.DialContext, "1.1.1.1:53", host)
		t.Logf("host=%s proxyDNS=%t addressCount=%d milliseconds=%d", host, err == nil, len(addresses), time.Since(started).Milliseconds())
		if err != nil {
			continue
		}
		for i, address := range addresses {
			if i > 1 {
				break
			}
			probeCtx, done := context.WithTimeout(ctx, 8*time.Second)
			result := acceptanceHTTPSVia(probeCtx, acceptanceProbeRequest{Mode: "https", Host: host, Target: net.JoinHostPort(address.String(), "443")}, out.DialContext)
			done()
			t.Logf("host=%s ordinal=%d proxyHTTPS=%t code=%s matchesSelected=%t", host, i+1, result.Success, result.Code, result.IP == expected)
		}
	}
}

func acceptanceIPv4(ctx context.Context, host string) (string, error) {
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return "", err
	}
	for _, a := range addresses {
		if ip, ok := netip.AddrFromSlice(a); ok && ip.Unmap().IsGlobalUnicast() {
			return ip.Unmap().String(), nil
		}
	}
	return "", errors.New("TARGET_IPV4_UNAVAILABLE")
}

func acceptanceProbe(t *testing.T, ctx context.Context, dir, stage string, request acceptanceProbeRequest, want bool) acceptanceProbeResult {
	t.Helper()
	result, err := runAcceptanceProbe(ctx, dir, request)
	matches := result.Success == want
	if !want {
		matches = !result.Success && (result.Code == acceptanceProbeDenied || result.Code == acceptanceProbeDial || result.Code == acceptanceProbeTime)
	}
	fields := map[string]any{"success": err == nil && matches, "requestSucceeded": result.Success, "code": result.Code, "fakeDNS": result.Fake, "domainFake": result.DomainFake}
	acceptanceEvent(dir, stage, fields)
	if err != nil || !matches {
		t.Fatalf("%s: unexpected probe result (%s)", stage, result.Code)
	}
	return result
}

func acceptanceHTTPSTarget(t *testing.T, ctx context.Context, dir string) acceptanceProbeRequest {
	t.Helper()
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip4", "www.cloudflare.com")
	acceptanceCheck(t, dir, "resolve_https_target", err)
	for i, ip := range addresses {
		if i >= 8 {
			break
		}
		request := acceptanceProbeRequest{Mode: "https", Target: net.JoinHostPort(ip.String(), "443"), Host: "www.cloudflare.com"}
		result, err := runAcceptanceProbe(ctx, dir, request)
		acceptanceEvent(dir, "baseline_https_candidate", map[string]any{"ordinal": i + 1, "success": err == nil && result.Success, "code": result.Code})
		if err == nil && result.Success {
			return request
		}
	}
	t.Fatal("NO_REACHABLE_BASELINE_HTTPS_TARGET")
	return acceptanceProbeRequest{}
}

// In-memory only. Never serialize the observed address or the probe result.
// The literal target is resolved once before WFP and reused across every phase;
// the separate domain probe intentionally exercises default OS DNS under TUN.
type acceptanceChinaBaseline struct {
	request             acceptanceProbeRequest
	observed            string
	literalCN, domainCN bool
}

func TestAcceptanceBypassRequiresChinaDomainBaseline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     routingpolicy.Mode
		baseline *acceptanceChinaBaseline
		ok       bool
	}{
		{"global-unavailable", routingpolicy.Global, nil, true},
		{"bypass-unavailable", routingpolicy.BypassCN, nil, false},
		{"bypass-literal-only", routingpolicy.BypassCN, &acceptanceChinaBaseline{observed: "8.8.8.8", literalCN: true}, false},
		{"bypass-domain-unavailable", routingpolicy.BypassCN, &acceptanceChinaBaseline{domainCN: true}, false},
		{"bypass-domain-unclassified", routingpolicy.BypassCN, &acceptanceChinaBaseline{observed: "8.8.8.8"}, false},
		{"bypass-domain-ready", routingpolicy.BypassCN, &acceptanceChinaBaseline{observed: "8.8.8.8", domainCN: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := acceptanceChinaBaselineReady(tc.mode, tc.baseline)
			if (err == nil) != tc.ok {
				t.Fatal("mandatory China domain baseline gate mismatch")
			}
			if err != nil && err.Error() != "CHINA_DOMAIN_BASELINE_UNAVAILABLE" {
				t.Fatal("baseline failure must be a constant code")
			}
		})
	}
}

func acceptanceChinaBaselineReady(mode routingpolicy.Mode, baseline *acceptanceChinaBaseline) error {
	if mode == routingpolicy.BypassCN && (baseline == nil || baseline.observed == "" || !baseline.domainCN) {
		return errors.New("CHINA_DOMAIN_BASELINE_UNAVAILABLE")
	}
	return nil
}

func acceptanceChinaTarget(ctx context.Context, dir string, rules *routingpolicy.Rules, mode routingpolicy.Mode) *acceptanceChinaBaseline {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for ordinal, host := range []string{"myip.ipip.net", "4.ipw.cn"} {
		domainCN := rules.Domain(host) == routingpolicy.Direct
		if mode == routingpolicy.BypassCN && !domainCN {
			acceptanceEvent(dir, "baseline_china_candidate", map[string]any{"ordinal": ordinal + 1, "success": false, "code": "CHINA_DOMAIN_NOT_CLASSIFIED"})
			continue
		}
		resolved, err := tunengine.LookupIPv4TCP(ctx, (&net.Dialer{Timeout: 5 * time.Second}).DialContext, routingpolicy.DomesticDNS+":53", host)
		if err != nil {
			acceptanceEvent(dir, "baseline_china_candidate", map[string]any{"ordinal": ordinal + 1, "success": false, "code": acceptanceProbeDNS})
			continue
		}
		for i, ip := range resolved {
			if i >= 2 || ctx.Err() != nil {
				break
			}
			if !acceptancePublicIPv4(ip) {
				continue
			}
			literalCN := rules.IsCN(ip)
			if !literalCN && !domainCN {
				acceptanceEvent(dir, "baseline_china_candidate", map[string]any{"ordinal": ordinal + 1, "success": false, "code": "CHINA_TARGET_NOT_CLASSIFIED"})
				continue
			}
			request := acceptanceProbeRequest{Mode: "https", Target: net.JoinHostPort(ip.String(), "443"), Host: host}
			result, err := runAcceptanceProbe(ctx, dir, request)
			usable := err == nil && result.Success && result.IP != ""
			acceptanceEvent(dir, "baseline_china_candidate", map[string]any{"ordinal": ordinal + 1, "success": usable, "code": result.Code, "literalCN": literalCN, "domainCN": domainCN})
			if usable {
				baseline := &acceptanceChinaBaseline{request: request, observed: result.IP, literalCN: literalCN, domainCN: domainCN}
				// Baseline connects directly to this domestic resolver's target,
				// retaining the original TLS host. The active domain probe below
				// separately requires Windows DNS to produce and use a FakeIP.
				if acceptanceChinaBaselineReady(mode, baseline) == nil {
					return baseline
				}
				break
			}
		}
	}
	return nil
}

func acceptanceChinaRouting(t *testing.T, ctx context.Context, dir, phase string, mode routingpolicy.Mode, baseline *acceptanceChinaBaseline, expected string) {
	t.Helper()
	acceptanceCheck(t, dir, phase+"_china_baseline_required", acceptanceChinaBaselineReady(mode, baseline))
	if baseline == nil {
		return
	}
	check := func(stage string, request acceptanceProbeRequest, observed string, requireFake bool) {
		distinct := observed != expected
		if mode == routingpolicy.Global && !distinct {
			acceptanceEvent(dir, stage+"_route", map[string]any{"checked": false, "distinguishable": false, "code": "CHINA_BASELINE_EQUALS_PROXY"})
			return
		}
		want := expected
		if mode == routingpolicy.BypassCN {
			want = observed
		}
		result := acceptanceProbe(t, ctx, dir, stage, request, true)
		matches := result.IP == want && (!requireFake || result.DomainFake)
		acceptanceEvent(dir, stage+"_route", map[string]any{"matches": matches, "distinguishable": distinct, "domainFake": result.DomainFake})
		if !matches {
			t.Fatal("CHINA_ROUTE_OR_FAKEIP_MISMATCH")
		}
	}
	if baseline.literalCN || mode == routingpolicy.Global {
		check(phase+"_china_literal", baseline.request, baseline.observed, false)
	}
	if baseline.domainCN {
		query := new(dns.Msg).SetQuestion(dns.Fqdn(baseline.request.Host), dns.TypeA)
		fresh, _, queryErr := (&dns.Client{Net: "udp", Timeout: 3 * time.Second}).ExchangeContext(ctx, query, "8.8.8.8:53")
		cached, cacheErr := net.DefaultResolver.LookupIP(ctx, "ip4", baseline.request.Host)
		matches := false
		if queryErr == nil && cacheErr == nil && fresh != nil {
			for _, answer := range fresh.Answer {
				if a, ok := answer.(*dns.A); ok {
					for _, value := range cached {
						matches = matches || a.A.Equal(value)
					}
				}
			}
		}
		acceptanceEvent(dir, phase+"_domain_cache_consistency", map[string]any{"success": matches, "freshDNS": queryErr == nil, "systemDNS": cacheErr == nil})
		check(phase+"_china_domain", acceptanceProbeRequest{Mode: "https-domain", Host: baseline.request.Host}, baseline.observed, true)
	} else {
		acceptanceEvent(dir, phase+"_china_domain", map[string]any{"checked": false, "code": "CHINA_DOMAIN_BASELINE_UNAVAILABLE"})
	}
}

func acceptanceGateway(ctx context.Context, dir string) string {
	routes, err := winipcfg.GetIPForwardTable2(windows.AF_INET)
	if err != nil {
		return ""
	}
	for _, r := range routes {
		ip := r.NextHop.Addr()
		if r.DestinationPrefix.Prefix().Bits() != 0 || !ip.IsPrivate() {
			continue
		}
		target := net.JoinHostPort(ip.String(), "80")
		result, _ := runAcceptanceProbe(ctx, dir, acceptanceProbeRequest{Mode: "http", Target: target})
		if result.Success {
			return target
		}
		break
	}
	return ""
}

func TestElevatedTUNAcceptance(t *testing.T) {
	dir := acceptanceRunDir(t)
	routingMode, err := acceptanceRoutingMode()
	acceptanceCheck(t, dir, "routing_mode", err)
	acceptanceCheck(t, dir, "clean_preflight", acceptancePreflight(dir))
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
	defer cancel()
	client, nodes, err := acceptanceLogin(ctx, os.Getenv("BLUEGRASS_ELEVATED_TUN_ACCOUNT"))
	if client != nil {
		defer func() {
			if client.token != "" {
				c, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				err := client.request(c, "/auth/logout", map[string]any{}, nil)
				acceptanceEvent(dir, "logout", map[string]any{"success": err == nil})
			}
		}()
	}
	acceptanceCheck(t, dir, "api_login_and_resource_list", err)
	acceptanceEvent(dir, "resources", map[string]any{"count": len(nodes), "filteredByExit": false})
	profile, expected, err := acceptanceSelect(ctx, dir, nodes)
	acceptanceCheck(t, dir, "select_working_resource", err)
	profile.RoutingMode = string(routingMode)
	https := acceptanceHTTPSTarget(t, ctx, dir)
	rules, err := routingpolicy.Load()
	acceptanceCheck(t, dir, "routing_rules", err)
	china := acceptanceChinaTarget(ctx, dir, rules, routingMode)
	acceptanceEvent(dir, "baseline_china", map[string]any{"available": china != nil, "routingMode": string(routingMode)})
	acceptanceCheck(t, dir, "baseline_china_required", acceptanceChinaBaselineReady(routingMode, china))
	stun, err := acceptanceIPv4(ctx, "stun.cloudflare.com")
	acceptanceCheck(t, dir, "resolve_stun_target", err)
	acceptanceCheck(t, dir, "record_public_recovery_probe", acceptanceWrite(dir, "recovery-probe.json", https))
	stunRequest := acceptanceProbeRequest{Mode: "stun", Target: net.JoinHostPort(stun, "3478")}
	stunBaseline, _ := runAcceptanceProbe(ctx, dir, stunRequest)
	acceptanceEvent(dir, "baseline_udp", map[string]any{"success": stunBaseline.Success, "code": stunBaseline.Code})
	v6request := acceptanceProbeRequest{Mode: "tls", Target: "[2606:4700:4700::1111]:443", Host: "cloudflare-dns.com"}
	v6baseline, _ := runAcceptanceProbe(ctx, dir, v6request)
	acceptanceEvent(dir, "baseline_ipv6", map[string]any{"available": v6baseline.Success})
	gateway := acceptanceGateway(ctx, dir)
	acceptanceEvent(dir, "baseline_lan", map[string]any{"available": gateway != ""})
	exe, err := os.Executable()
	acceptanceCheck(t, dir, "executable_path", err)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	acceptanceCheck(t, dir, "owner_identity", err)
	logical := "acceptance-" + filepath.Base(dir)
	acceptanceCheck(t, dir, "claim_test_state", acceptanceWrite(dir, "owned.json", acceptanceOwned{PID: os.Getpid(), Run: dir, LogicalID: logical}))
	manager, err := winprotect.New(exe, filepath.Join(filepath.Dir(os.Getenv("BLUEGRASS_ELEVATED_TUN_ACCOUNT")), "Windows", "dist", "BlueGrass-Windows-x64", "runtime", "BlueGrass.exe"))
	acceptanceCheck(t, dir, "open_real_wfp_manager", err)
	defer manager.Close()
	tunnel := newProtectedTunnel(manager, user.User.Sid.String(), filepath.Join(filepath.Dir(os.Getenv("BLUEGRASS_ELEVATED_TUN_ACCOUNT")), "Windows", "dist", "BlueGrass-Windows-x64", "runtime", "BlueGrass.exe"))
	defer func() {
		err := tunnel.Release()
		acceptanceEvent(dir, "deferred_release", map[string]any{"success": err == nil})
		_ = tunnel.Close()
	}()
	acceptanceCheck(t, dir, "bind_logical_session", tunnel.BindLogicalSession(profile.Key, logical))
	acceptanceCheck(t, dir, "prepare_real_wfp", tunnel.Prepare(ctx, profile))
	present, err := acceptanceWFP()
	if err == nil && !present {
		err = errors.New("WFP_OBJECTS_MISSING")
	}
	acceptanceCheck(t, dir, "wfp_present_before_tun", err)
	acceptanceProbe(t, ctx, dir, "blocked_before_tun", https, false)
	if china != nil {
		acceptanceProbe(t, ctx, dir, "china_blocked_before_tun", china.request, false)
	}
	acceptanceCheck(t, dir, "activate_real_tun", tunnel.SetBusinessGate(true))
	result := acceptanceProbe(t, ctx, dir, "https_through_real_tun", https, true)
	if result.IP != expected {
		t.Fatal("TUN_EXIT_DIFFERS_FROM_SELECTED_PROXY")
	}
	acceptanceEvent(dir, "exit_consistency", map[string]any{"matches": true})
	acceptanceChinaRouting(t, ctx, dir, "active", routingMode, china, expected)
	for _, mode := range []string{"dns-udp", "dns-tcp", "system-dns"} {
		dnsTarget := "8.8.8.8:53"
		dnsHost := "example.com"
		if mode == "system-dns" {
			dnsTarget = ""
			dnsHost = filepath.Base(dir) + ".example.com"
		}
		result := acceptanceProbe(t, ctx, dir, mode+"_through_real_tun", acceptanceProbeRequest{Mode: mode, Target: dnsTarget, Host: dnsHost}, true)
		if !result.Fake {
			t.Fatalf("%s: DNS did not enter the TUN FakeIP policy", mode)
		}
	}
	udp := acceptanceProbe(t, ctx, dir, "udp_stun_through_real_tun", stunRequest, true)
	acceptanceEvent(dir, "udp_exit_consistency", map[string]any{"matches": udp.IP == expected})
	if udp.IP != expected {
		t.Fatal("UDP_EXIT_DIFFERS_FROM_SELECTED_PROXY")
	}
	if v6baseline.Success {
		acceptanceProbe(t, ctx, dir, "public_ipv6_blocked", v6request, false)
	}
	if gateway != "" {
		acceptanceProbe(t, ctx, dir, "lan_enabled", acceptanceProbeRequest{Mode: "http", Target: gateway}, true)
	}
	upload, download := tunnel.Counters()
	if upload == 0 || download == 0 {
		t.Fatal("TUN_COUNTERS_DID_NOT_ADVANCE")
	}
	acceptanceEvent(dir, "traffic_counters", map[string]any{"uploadPositive": true, "downloadPositive": true})
	acceptanceCheck(t, dir, "pause_real_tun", tunnel.SetBusinessGate(false))
	acceptanceProbe(t, ctx, dir, "paused_business_blocked", https, false)
	if china != nil {
		acceptanceProbe(t, ctx, dir, "paused_china_blocked", china.request, false)
	}
	acceptanceCheck(t, dir, "resume_prepare", tunnel.Prepare(ctx, profile))
	acceptanceCheck(t, dir, "resume_activate", tunnel.SetBusinessGate(true))
	result = acceptanceProbe(t, ctx, dir, "resumed_business", https, true)
	if result.IP != expected {
		t.Fatal("RESUMED_EXIT_DIFFERS_FROM_SELECTED_PROXY")
	}
	acceptanceChinaRouting(t, ctx, dir, "resumed", routingMode, china, expected)
	acceptanceCheck(t, dir, "disconnect_release", tunnel.Release())
	present, err = acceptanceWFP()
	if err == nil && present {
		err = errors.New("WFP_OBJECTS_REMAIN")
	}
	acceptanceCheck(t, dir, "wfp_removed_after_disconnect", err)
	acceptanceProbe(t, ctx, dir, "physical_https_restored", https, true)
	if china != nil {
		restored := acceptanceProbe(t, ctx, dir, "physical_china_restored", china.request, true)
		if restored.IP != china.observed {
			t.Fatal("RESTORED_CHINA_EXIT_DIFFERS_FROM_BASELINE")
		}
	}
	result = acceptanceProbe(t, ctx, dir, "system_dns_restored", acceptanceProbeRequest{Mode: "system-dns", Host: "example.com"}, true)
	if result.Fake {
		t.Fatal("SYSTEM_DNS_RETAINED_FAKEIP_AFTER_DISCONNECT")
	}
	// Repeated adapter removal can expose cleanup races that a single
	// connect/disconnect misses. Each cycle keeps WFP until cleanup is confirmed.
	for cycle := 1; cycle <= 3; cycle++ {
		stage := fmt.Sprintf("cycle_%d_", cycle)
		acceptanceCheck(t, dir, stage+"binding", tunnel.BindLogicalSession(profile.Key, logical))
		acceptanceCheck(t, dir, stage+"prepare", tunnel.Prepare(ctx, profile))
		acceptanceCheck(t, dir, stage+"activate", tunnel.SetBusinessGate(true))
		acceptanceCheck(t, dir, stage+"pause", tunnel.SetBusinessGate(false))
		acceptanceCheck(t, dir, stage+"release", tunnel.Release())
	}
	if gateway != "" {
		acceptanceCheck(t, dir, "disable_lan", tunnel.SetAllowLAN(false))
		acceptanceCheck(t, dir, "lan_off_binding", tunnel.BindLogicalSession(profile.Key, logical+"-lan-off"))
		acceptanceCheck(t, dir, "lan_off_prepare", tunnel.Prepare(ctx, profile))
		acceptanceCheck(t, dir, "lan_off_activate", tunnel.SetBusinessGate(true))
		acceptanceProbe(t, ctx, dir, "lan_off_public_https", https, true)
		acceptanceProbe(t, ctx, dir, "lan_disabled", acceptanceProbeRequest{Mode: "http", Target: gateway}, false)
		acceptanceCheck(t, dir, "lan_off_release", tunnel.Release())
	}
	acceptanceCheck(t, dir, "logout_before_crash", client.request(ctx, "/auth/logout", map[string]any{}, nil))
	client.token = ""
	acceptanceCheck(t, dir, "crash_binding", tunnel.BindLogicalSession(profile.Key, logical+"-crash"))
	acceptanceCheck(t, dir, "crash_prepare", tunnel.Prepare(ctx, profile))
	acceptanceCheck(t, dir, "crash_activate", tunnel.SetBusinessGate(true))
	acceptanceProbe(t, ctx, dir, "crash_before_public_https", https, true)
	acceptanceCheck(t, dir, "crash_ready", acceptanceWrite(dir, "crash-ready.json", https))
	// Deliberately skip defers to exercise persistent WFP and independent repair.
	os.Exit(92)
}

func TestElevatedTUNCrashCheck(t *testing.T) {
	dir := acceptanceRunDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "crash-ready.json"))
	acceptanceCheck(t, dir, "crash_record", err)
	var request acceptanceProbeRequest
	acceptanceCheck(t, dir, "crash_request", json.Unmarshal(data, &request))
	present, err := acceptanceWFP()
	if err == nil && !present {
		err = errors.New("CRASH_DROPPED_PERSISTENT_WFP")
	}
	acceptanceCheck(t, dir, "crash_wfp_retained", err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	acceptanceProbe(t, ctx, dir, "crash_business_blocked", request, false)
}

func TestElevatedTUNRecovery(t *testing.T) {
	dir := acceptanceRunDir(t)
	if _, err := os.Stat(filepath.Join(dir, "recovered.json")); err == nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "owned.json"))
	if errors.Is(err, os.ErrNotExist) {
		acceptanceEvent(dir, "recovery_no_owned_changes", map[string]any{"success": true})
		return
	}
	acceptanceCheck(t, dir, "recovery_marker", err)
	var owned acceptanceOwned
	acceptanceCheck(t, dir, "recovery_owner", json.Unmarshal(data, &owned))
	if owned.Run != dir || owned.PID <= 0 {
		t.Fatal("RECOVERY_MARKER_MISMATCH")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(owned.PID))
	if err == nil {
		state, _ := windows.WaitForSingleObject(h, 0)
		_ = windows.CloseHandle(h)
		if state == uint32(windows.WAIT_TIMEOUT) {
			t.Fatal("ACCEPTANCE_WORKER_STILL_RUNNING")
		}
	}
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	acceptanceCheck(t, dir, "recovery_state_path", err)
	if data, err := os.ReadFile(filepath.Join(root, "BlueGrass", "state.json")); err == nil {
		var state struct {
			ProtectIntent bool   `json:"protectIntent"`
			Account       string `json:"accountKey"`
			Logical       string `json:"logicalSessionId"`
		}
		if json.Unmarshal(data, &state) != nil || (state.ProtectIntent && (state.Account != acceptanceAccount || !strings.HasPrefix(state.Logical, owned.LogicalID))) {
			t.Fatal("RECOVERY_STATE_NOT_OWNED_BY_THIS_RUN")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("RECOVERY_STATE_UNREADABLE")
	}
	acceptanceCheck(t, dir, "recover_owned_adapter", tunengine.RepairAdapter())
	acceptanceCheck(t, dir, "recover_owned_cache", ClearOwnedCache())
	acceptanceCheck(t, dir, "recover_owned_wfp", winprotect.Repair())
	present, err := acceptanceWFP()
	if err == nil && present {
		err = errors.New("WFP_REMAINS_AFTER_RECOVERY")
	}
	acceptanceCheck(t, dir, "recovery_wfp_absent", err)
	data, err = os.ReadFile(filepath.Join(dir, "recovery-probe.json"))
	acceptanceCheck(t, dir, "recovery_probe_record", err)
	var request acceptanceProbeRequest
	acceptanceCheck(t, dir, "recovery_probe_request", json.Unmarshal(data, &request))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acceptanceProbe(t, ctx, dir, "recovery_physical_https", request, true)
	result := acceptanceProbe(t, ctx, dir, "recovery_system_dns", acceptanceProbeRequest{Mode: "system-dns", Host: "example.com"}, true)
	if result.Fake {
		t.Fatal("RECOVERY_RETAINED_FAKE_DNS")
	}
	acceptanceCheck(t, dir, "recovery_complete", acceptanceWrite(dir, "recovered.json", map[string]any{"success": true}))
}
