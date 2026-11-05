// Package exitprobe performs the single bounded HTTPS exit-observation flow
// shared by both engines. It has no expected-IP argument: the first valid public
// IPv4 address is an observation. IPv6 responses are invalid and permit fallback.
package exitprobe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"bluegrass/native/core/gate"
)

const TotalTimeout = 20 * time.Second
const maxBody = 4096

type DialFunc func(context.Context, string, string) (net.Conn, error)
type Options struct {
	// Only the TUN engine's existing JSON contract permits the optional string
	// country field. It is ignored; it cannot affect observation or routing.
	AllowCountry bool
}

type endpoint struct {
	host, url string
	json      bool
}

// No user/profile/IPC endpoint override. These are temporary third-party
// endpoints until the separately deployed first-party service is available.
var fixedEndpoints = [...]endpoint{
	{"api.ipify.org", "https://api.ipify.org/?format=json", true},
	{"icanhazip.com", "https://icanhazip.com/", false},
	{"checkip.amazonaws.com", "https://checkip.amazonaws.com/", false},
}

var errProbe = errors.New("EXIT_PROBE_FAILED")
var errResponse = errors.New("INVALID_EXIT_RESPONSE")

type probeBudget struct{ total, attempt time.Duration }

var defaultBudget = probeBudget{total: TotalTimeout, attempt: 7 * time.Second}

// AllowedHost is also used by the TUN adapter before proxied DNS resolution.
// It accepts only the exact fixed hostnames, TCP and HTTPS port 443.
func AllowedHost(network, address string) (string, bool) {
	if network != "tcp" {
		return "", false
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return "", false
	}
	for _, endpoint := range fixedEndpoints {
		if host == endpoint.host {
			return host, true
		}
	}
	return "", false
}

func ValidObservedAddress(value string) bool {
	return gate.PublicIPv4(value)
}

func Probe(ctx context.Context, dial DialFunc, options Options) (string, error) {
	if dial == nil {
		return "", errProbe
	}
	return probe(ctx, newTransport(dial), options, defaultBudget)
}

func newTransport(dial DialFunc) *http.Transport {
	return &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, ok := AllowedHost(network, address); !ok {
			return nil, errProbe
		}
		return dial(ctx, network, address)
	}, DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 8192}
}

func probe(ctx context.Context, transport *http.Transport, options Options, budget probeBudget) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, budget.total)
	defer cancel()
	for _, endpoint := range fixedEndpoints {
		if ctx.Err() != nil {
			return "", errProbe
		}
		attempt, stop := context.WithTimeout(ctx, budget.attempt)
		address, err := request(attempt, transport, endpoint, options)
		stop()
		if err == nil {
			if ctx.Err() != nil {
				return "", errProbe
			}
			return address, nil // The first valid observation ends fallback.
		}
	}
	return "", errProbe
}

func request(ctx context.Context, template *http.Transport, endpoint endpoint, options Options) (string, error) {
	// net/http detaches its dial context from request deadlines so another
	// request could reuse that dial. Each probe attempt instead owns its DNS,
	// connect and TLS lifetime; nothing may carry into a fallback or new session.
	transport := template.Clone()
	transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		// Xray dispatch completes its upstream handshake asynchronously. The
		// attempt context must remain live until the HTTP/TLS exchange finishes.
		return template.DialContext(ctx, network, address)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return "", errProbe
	}
	if endpoint.json {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "text/plain")
	}
	response, err := client.Do(req)
	if err != nil {
		return "", errProbe
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errResponse
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || ctx.Err() != nil {
		return "", errResponse
	}
	if endpoint.json {
		return parseJSON(data, options.AllowCountry)
	}
	return parsePlain(data)
}

func parseJSON(data []byte, allowCountry bool) (string, error) {
	if len(data) > maxBody {
		return "", errResponse
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", errResponse
	}
	fields := map[string]bool{}
	ip := ""
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] || name != "ip" && (!allowCountry || name != "country") {
			return "", errResponse
		}
		fields[name] = true
		// Decoding null into a string succeeds in encoding/json; token checking
		// keeps every accepted field explicitly a JSON string.
		value, err := d.Token()
		text, isString := value.(string)
		if err != nil || !isString {
			return "", errResponse
		}
		if name == "ip" {
			ip = text
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return "", errResponse
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !ValidObservedAddress(ip) {
		return "", errResponse
	}
	return netip.MustParseAddr(ip).String(), nil
}

func parsePlain(data []byte) (string, error) {
	if len(data) > maxBody {
		return "", errResponse
	}
	value := strings.Trim(string(data), " \t\r\n")
	if !ValidObservedAddress(value) {
		return "", errResponse
	}
	return netip.MustParseAddr(value).String(), nil
}
