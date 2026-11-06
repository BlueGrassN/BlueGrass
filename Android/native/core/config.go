package bridge

import (
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"encoding/json"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

type config struct {
	Version     int                   `json:"version"`
	Host        string                `json:"host"`
	Port        int                   `json:"port"`
	Protocol    protocolconfig.Config `json:"protocolConfig"`
	Expires     *string               `json:"expiresAtUtc"`
	Now         string                `json:"effectiveNowUtc"`
	RoutingMode routingpolicy.Mode    `json:"routingMode"`
}

// Validate accepts only resource configuration, never arbitrary core options.
// Error messages are fixed identifiers; none retain input or nested errors.
func Validate(raw string) error { _, err := parseConfig(raw); return err }
func parseConfig(raw string) (config, error) {
	c := config{RoutingMode: routingpolicy.Global}
	if len(raw) == 0 || len(raw) > 32768 {
		return c, errConfig
	}
	d := json.NewDecoder(strings.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return c, errConfig
	}
	allowed := map[string]bool{"version": true, "host": true, "port": true, "protocolConfig": true, "expiresAtUtc": true, "effectiveNowUtc": true, "routingMode": true}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return c, errConfig
		}
		name, ok := key.(string)
		if !ok || !allowed[name] || seen[name] {
			return c, errConfig
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return c, errConfig
		}
		if name == "routingMode" {
			var mode string
			if json.Unmarshal(value, &mode) != nil || (mode != string(routingpolicy.Global) && mode != string(routingpolicy.BypassCN)) {
				return c, errConfig
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return c, errConfig
	}
	for key := range allowed {
		if key != "routingMode" && !seen[key] {
			return c, errConfig
		}
	}
	if _, err = d.Token(); err != io.EOF {
		return c, errConfig
	}
	if json.Unmarshal([]byte(raw), &c) != nil || c.Version != 1 || c.Protocol.Validate() != nil || c.Port < 1 || c.Port > 65535 {
		return config{}, errConfig
	}
	if err := c.Protocol.Supported(); err != nil {
		return config{}, err
	}
	if len(c.Host) > 253 || c.Host == "" || strings.TrimSpace(c.Host) != c.Host {
		return config{}, errConfig
	}
	if ip, e := netip.ParseAddr(c.Host); e == nil {
		if !gate.PublicIPv4(ip.String()) {
			return config{}, errConfig
		}
	} else {
		for _, label := range strings.Split(c.Host, ".") {
			if !hostLabel.MatchString(label) {
				return config{}, errConfig
			}
		}
		if !strings.Contains(c.Host, ".") {
			return config{}, errConfig
		}
	}
	now, err := utcTime(c.Now)
	if err != nil {
		return config{}, errConfig
	}
	if c.Expires != nil {
		expiry, e := utcTime(*c.Expires)
		if e != nil || !now.Before(expiry) {
			return config{}, errConfig
		}
	}
	return c, nil
}

var hostLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func utcTime(value string) (time.Time, error) {
	if !strings.HasSuffix(value, "Z") {
		return time.Time{}, errConfig
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errConfig
	}
	return parsed, nil
}
