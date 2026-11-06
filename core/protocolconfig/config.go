// Package protocolconfig is the versioned, closed resource-protocol contract.
// It accepts no native kernel JSON, routing policy, paths or resolver overrides.
package protocolconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
)

var ErrInvalidConfig = errors.New("INVALID_PROTOCOL_CONFIG")
var ErrUnsupportedProtocol = errors.New("XRAY_UNSUPPORTED_PROTOCOL")

// Validate preserves the wire schema, including unsupported resources. Supported
// is checked only at the connection and physical-permission boundaries.
func (c Config) Supported() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Type == "tuic" || c.Type == "anytls" {
		return ErrUnsupportedProtocol
	}
	return nil
}

// Config must be bound by the controller to Profile's resource and entitlement.
// Secrets are credentials, never diagnostic text. Do not log or persist Config.
type Config struct {
	Version     int          `json:"version"`
	Type        string       `json:"type"`
	TLS         *TLS         `json:"tls,omitempty"`
	SOCKS5      *SOCKS5      `json:"socks5,omitempty"`
	Shadowsocks *Shadowsocks `json:"shadowsocks,omitempty"`
	VMess       *VMess       `json:"vmess,omitempty"`
	VLESS       *VLESS       `json:"vless,omitempty"`
	Trojan      *Trojan      `json:"trojan,omitempty"`
	HTTP        *HTTP        `json:"http,omitempty"`
	Hysteria2   *Hysteria2   `json:"hysteria2,omitempty"`
	TUIC        *TUIC        `json:"tuic,omitempty"`
	AnyTLS      *AnyTLS      `json:"anytls,omitempty"`
}
type TLS struct {
	ServerName string   `json:"serverName"`
	ALPN       []string `json:"alpn,omitempty"`
}
type SOCKS5 struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}
type Shadowsocks struct {
	Method   string `json:"method"`
	Password string `json:"password"`
}
type VMess struct {
	UUID      string `json:"uuid"`
	Security  string `json:"security"`
	Transport string `json:"transport"`
}
type VLESS struct {
	UUID      string `json:"uuid"`
	Transport string `json:"transport"`
	Flow      string `json:"flow,omitempty"`
}
type Trojan struct {
	Password  string `json:"password"`
	Transport string `json:"transport"`
}
type HTTP struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}
type Hysteria2 struct {
	Password string `json:"password"`
	Obfs     *Obfs  `json:"obfs,omitempty"`
}
type Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}
type TUIC struct {
	UUID              string `json:"uuid"`
	Password          string `json:"password"`
	CongestionControl string `json:"congestionControl"`
	UDPRelayMode      string `json:"udpRelayMode"`
}
type AnyTLS struct {
	Password string `json:"password"`
}

// UnmarshalJSON rejects unknown, duplicate and case-variant keys at every level.
// A failed decode clears the receiver so an earlier credential cannot survive.
func (c *Config) UnmarshalJSON(data []byte) error {
	*c = Config{}
	if len(data) > 16*1024 || strictShape(data, reflect.TypeFor[Config]()) != nil {
		return ErrInvalidConfig
	}
	type plain Config
	var p plain
	if json.Unmarshal(data, &p) != nil {
		return ErrInvalidConfig
	}
	value := Config(p)
	if value.Validate() != nil {
		return ErrInvalidConfig
	}
	*c = value
	return nil
}

func strictShape(data []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidConfig
	}
	if typ.Kind() == reflect.Struct {
		d := json.NewDecoder(bytes.NewReader(data))
		first, err := d.Token()
		if err != nil || first != json.Delim('{') {
			return ErrInvalidConfig
		}
		fields := make(map[string]reflect.Type)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f.Type
		}
		seen := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			field, exists := fields[key]
			if err != nil || !ok || !exists || seen[key] {
				return ErrInvalidConfig
			}
			seen[key] = true
			var raw json.RawMessage
			if d.Decode(&raw) != nil || strictShape(raw, field) != nil {
				return ErrInvalidConfig
			}
		}
		last, err := d.Token()
		if err != nil || last != json.Delim('}') {
			return ErrInvalidConfig
		}
		if _, err = d.Token(); err != io.EOF {
			return ErrInvalidConfig
		}
	} else if typ.Kind() == reflect.Slice {
		var items []json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return ErrInvalidConfig
		}
		for _, item := range items {
			if strictShape(item, typ.Elem()) != nil {
				return ErrInvalidConfig
			}
		}
	} else if json.Unmarshal(data, reflect.New(typ).Interface()) != nil {
		return ErrInvalidConfig
	}
	return nil
}

func secret(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && !strings.ContainsAny(s, "\x00\r\n")
}
func pair(u, p string, limit int) bool {
	return u == "" && p == "" || secret(u) && secret(p) && len(u) <= limit && len(p) <= limit
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool {
	return uuidPattern.MatchString(s) && s != "00000000-0000-0000-0000-000000000000"
}
func validServerName(s string) bool {
	if len(s) == 0 || len(s) > 253 || strings.ContainsAny(s, "/\\:*? \t\r\n\x00") {
		return false
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.Is4() && ip.IsGlobalUnicast()
	}
	for _, label := range strings.Split(s, ".") {
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
func (c Config) Validate() error {
	if c.Version != 1 {
		return ErrInvalidConfig
	}
	count := 0
	for _, p := range []any{c.SOCKS5, c.Shadowsocks, c.VMess, c.VLESS, c.Trojan, c.HTTP, c.Hysteria2, c.TUIC, c.AnyTLS} {
		if !reflect.ValueOf(p).IsNil() {
			count++
		}
	}
	if count != 1 {
		return ErrInvalidConfig
	}
	requireTLS, allowTLS, valid := false, false, false
	switch c.Type {
	case "socks5":
		valid = c.SOCKS5 != nil && pair(c.SOCKS5.Username, c.SOCKS5.Password, 255)
	case "shadowsocks":
		if c.Shadowsocks != nil && secret(c.Shadowsocks.Password) {
			switch c.Shadowsocks.Method {
			case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
				valid = true
			case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
				key, err := base64.StdEncoding.Strict().DecodeString(c.Shadowsocks.Password)
				size := 32
				if c.Shadowsocks.Method == "2022-blake3-aes-128-gcm" {
					size = 16
				}
				valid = err == nil && len(key) == size
			}
		}
	case "vmess":
		allowTLS = true
		valid = c.VMess != nil && validUUID(c.VMess.UUID) && c.VMess.Transport == "tcp" && (c.VMess.Security == "aes-128-gcm" || c.VMess.Security == "chacha20-poly1305")
	case "vless":
		requireTLS = true
		valid = c.VLESS != nil && validUUID(c.VLESS.UUID) && c.VLESS.Transport == "tcp" && (c.VLESS.Flow == "" || c.VLESS.Flow == "xtls-rprx-vision")
	case "trojan":
		requireTLS = true
		valid = c.Trojan != nil && secret(c.Trojan.Password) && c.Trojan.Transport == "tcp"
	case "http", "https":
		requireTLS = c.Type == "https"
		valid = c.HTTP != nil && pair(c.HTTP.Username, c.HTTP.Password, 1024) && !strings.Contains(c.HTTP.Username, ":")
	case "hysteria2":
		requireTLS = true
		valid = c.Hysteria2 != nil && secret(c.Hysteria2.Password)
		if valid && c.Hysteria2.Obfs != nil {
			valid = c.Hysteria2.Obfs.Type == "salamander" && secret(c.Hysteria2.Obfs.Password)
		}
	case "tuic":
		requireTLS = true
		valid = c.TUIC != nil && validUUID(c.TUIC.UUID) && secret(c.TUIC.Password) && (c.TUIC.CongestionControl == "cubic" || c.TUIC.CongestionControl == "new_reno" || c.TUIC.CongestionControl == "bbr") && (c.TUIC.UDPRelayMode == "native" || c.TUIC.UDPRelayMode == "quic")
	case "anytls":
		requireTLS = true
		valid = c.AnyTLS != nil && secret(c.AnyTLS.Password)
	}
	if !valid || requireTLS && c.TLS == nil || c.TLS != nil && !requireTLS && !allowTLS {
		return ErrInvalidConfig
	}
	if c.TLS != nil {
		if !validServerName(c.TLS.ServerName) || len(c.TLS.ALPN) > 8 {
			return ErrInvalidConfig
		}
		seen := make(map[string]bool)
		for _, a := range c.TLS.ALPN {
			if a == "" || len(a) > 255 || seen[a] {
				return ErrInvalidConfig
			}
			seen[a] = true
			for _, b := range []byte(a) {
				if b < 33 || b > 126 {
					return ErrInvalidConfig
				}
			}
			if c.Type == "https" && a != "http/1.1" || c.Type == "hysteria2" && a != "h3" {
				return ErrInvalidConfig
			}
		}
	}
	return nil
}

// TransportRequirements describe the pinned implementation, NOT runtime UDP
// acceptance. Only the exact resolved entry is physical; SOCKS relay needs its
// separately authenticated callback. All others have a fixed upstream peer.
type TransportRequirements struct {
	EntryTCP, EntryUDP bool
	UDPTransport       string
}

func Requirements(c Config) (TransportRequirements, error) {
	if err := c.Supported(); err != nil {
		return TransportRequirements{}, err
	}
	r := TransportRequirements{EntryTCP: true, UDPTransport: "over-tcp"}
	switch c.Type {
	case "socks5":
		r.UDPTransport = "authenticated-socks-relay"
	case "shadowsocks":
		r.EntryUDP = true
		r.UDPTransport = "entry-datagram"
	case "http", "https":
		r.UDPTransport = "none"
	case "hysteria2", "tuic":
		r.EntryTCP = false
		r.EntryUDP = true
		r.UDPTransport = "quic"
	}
	return r, nil
}
