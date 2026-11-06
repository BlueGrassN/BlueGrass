package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"bluegrass/native/core/gate"
)

const MaxInputBytes = 8192

var errInvalid = errors.New("invalid_request")

// DecodeRequest consumes exactly one bounded JSON object. Parser details and
// submitted values are deliberately never returned in an error.
func DecodeRequest(reader io.Reader) (Request, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxInputBytes+1))
	var req Request
	if err != nil || len(data) > MaxInputBytes || strictJSON(data, &req) != nil || ValidateRequest(req) != nil {
		return Request{}, errInvalid
	}
	return req, nil
}

// Reject duplicate keys at every nesting level as well as unknown fields,
// trailing JSON and null. The token pass is bounded by the input size/depth.
func strictJSON(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return errInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 16 {
			return errInvalid
		}
		t, err := d.Token()
		if err != nil {
			return errInvalid
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return errInvalid
				}
				seen[name] = true
				if value(depth+1) != nil {
					return errInvalid
				}
			}
		case '[':
			for d.More() {
				if value(depth+1) != nil {
					return errInvalid
				}
			}
		default:
			return errInvalid
		}
		_, err = d.Token()
		return err
	}
	if value(0) != nil {
		return errInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return errInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return errInvalid
	}
	return nil
}

func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == s
}
func literalEndpoint(s string) (netip.Addr, bool) {
	h, p, err := net.SplitHostPort(s)
	if err != nil || !validPort(p) {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(h)
	return ip, err == nil && ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast()
}

func validHost(s string) bool {
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func safeHTTPS(s string, exit bool) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || len(s) > 512 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || !validHost(u.Hostname()) || u.Port() != "" && !validPort(u.Port()) {
		return nil, errInvalid
	}
	if u.RawQuery != "" && (!exit || u.RawQuery != "format=json") {
		return nil, errInvalid
	}
	return u, nil
}

func ValidateRequest(req Request) error {
	if req.TimeoutMS != 0 && (req.TimeoutMS < 100 || req.TimeoutMS > 10000) || len(req.ExitEndpoints) > 3 {
		return errInvalid
	}
	if req.Binding != nil && len(req.Binding.ResourceID) > 256 {
		return errInvalid
	}
	if req.APIBaseURL != "" {
		if _, err := safeHTTPS(req.APIBaseURL, false); err != nil {
			return errInvalid
		}
	}
	if req.Entry != nil {
		e := req.Entry
		if !validHost(e.Host) || !validPort(e.Port) || len(e.DNSServers) > 8 {
			return errInvalid
		}
		if e.DNSMode != "" && e.DNSMode != "system" && e.DNSMode != "custom" {
			return errInvalid
		}
		if e.DNSMode == "custom" && len(e.DNSServers) == 0 || e.DNSMode != "custom" && len(e.DNSServers) > 0 {
			return errInvalid
		}
		for _, s := range e.DNSServers {
			if _, ok := literalEndpoint(s.Address); !ok || s.Network != "udp" && s.Network != "tcp" {
				return errInvalid
			}
		}
	}
	for _, e := range req.ExitEndpoints {
		if _, err := safeHTTPS(e.URL, true); err != nil || !gate.PublicIPv4(e.IPv4) {
			return errInvalid
		}
	}
	if req.UDP != nil {
		ip, ok := literalEndpoint(req.UDP.Address)
		if !ok || !gate.PublicIPv4(ip.String()) || req.UDP.Protocol != "echo_nonce_v1" {
			return errInvalid
		}
	}
	if req.Proxy != nil {
		ip, ok := literalEndpoint(req.Proxy.Address)
		if !ok || !ip.IsLoopback() || req.Proxy.Kind != "http_connect" && req.Proxy.Kind != "socks5" {
			return errInvalid
		}
	}
	return nil
}
