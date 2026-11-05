package nativehost

import (
	"bytes"
	"encoding/json"
	"errors"

	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
)

// Validate before calibrating the clock or invoking any engine preferences.
// Keep the original profile bytes for control's independent authenticated decode.
func decodeProfile(data []byte) (engine.Profile, error) {
	bad := errors.New("INVALID_CONFIGURATION")
	var p engine.Profile
	if control.ValidateJSON(data) != nil || json.Unmarshal(data, &p) != nil {
		return p, bad
	}
	fields, ok := profileObject(data, []string{"key", "protocol", "entry", "auth", "validUntilUtc", "configVersion", "configFingerprint", "mode"}, []string{"protocolConfig", "expectedExitIpv4", "routingMode"})
	if !ok {
		return p, bad
	}
	if _, present := fields["routingMode"]; present && p.RoutingMode != "global" && p.RoutingMode != "bypass_cn" {
		return p, bad
	}
	for _, nested := range []struct {
		name string
		keys []string
	}{
		{"key", []string{"environment", "dataSource", "accountKey", "kind", "sourceId"}},
		{"entry", []string{"host", "port"}},
		{"auth", []string{"username", "password"}},
	} {
		if _, ok := profileObject(fields[nested.name], nested.keys, nil); !ok {
			return p, bad
		}
	}
	if len(p.Key.Environment) > 2048 || len(p.Key.DataSource) > 64 || len(p.Key.AccountKey) > 128 ||
		len(p.Key.SourceID) > 128 || len(p.Key.Kind) > 32 || len(p.Entry.Host) > 253 ||
		len(p.Auth.Username) > 255 || len(p.Auth.Password) > 255 {
		return p, bad
	}
	// Config's typed decoder above enforces its closed schema and 16 KiB bound.
	// The engine's shared resolver never infers non-SOCKS protocols from Auth.
	_, err := p.EffectiveProtocol()
	if err != nil {
		return p, bad
	}
	return p, nil
}

// Unlike encoding/json, reject missing, null and case-variant profile fields.
// Only the explicitly nullable resource deadline is permitted to be null.
func profileObject(data []byte, required, optional []string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, false
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, present := fields[key]; !present {
			return nil, false
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || key != "validUntilUtc" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return fields, true
}
