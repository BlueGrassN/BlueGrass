package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeRequest is the strict reference wire decoder. Never forward decoder
// errors containing user input; this boundary returns only a fixed error code.
// In-process Apply callers are trusted typed producers; they must preserve null.
func DecodeRequest(data []byte) (ControlRequest, error) {
	bad := errors.New("INVALID_REQUEST")
	if ValidateJSON(data) != nil {
		return ControlRequest{}, bad
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var r ControlRequest
	if d.Decode(&r) != nil {
		return ControlRequest{}, bad
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return ControlRequest{}, bad
	}
	root, ok := object(data, []string{"context", "requestId", "sequence", "command", "params"}, nil)
	if !ok {
		return ControlRequest{}, bad
	}
	if _, ok = object(root["params"], nil, []string{"profile", "update"}); !ok {
		return ControlRequest{}, bad
	}
	contextRequired := []string{"schemaVersion", "clientEpoch", "runtimeEpoch", "logicalSessionId", "attemptId", "resourceKey"}
	var contextOptional []string
	if r.Command == Hello || r.Command == GetStatus {
		contextRequired = []string{"schemaVersion", "clientEpoch"}
		contextOptional = []string{"runtimeEpoch", "logicalSessionId", "attemptId", "resourceKey"}
	}
	x, valid := object(root["context"], contextRequired, contextOptional)
	if !valid {
		return ControlRequest{}, bad
	}
	if has(x, "resourceKey") {
		if _, valid = object(x["resourceKey"], []string{"environment", "dataSource", "accountKey", "kind", "sourceId"}, nil); !valid {
			return ControlRequest{}, bad
		}
	}
	var raw struct {
		Params struct {
			Profile map[string]json.RawMessage `json:"profile"`
			Update  map[string]json.RawMessage `json:"update"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return ControlRequest{}, bad
	}
	if r.Params.Profile != nil && !has(raw.Params.Profile, "validUntilUtc") {
		return ControlRequest{}, bad
	}
	if r.Params.Profile != nil {
		b, _ := json.Marshal(raw.Params.Profile)
		if !profileShape(b) {
			return ControlRequest{}, bad
		}
	}
	if r.Params.Update != nil && !has(raw.Params.Update, "validUntilUtc") {
		return ControlRequest{}, bad
	}
	if r.Params.Update != nil {
		b, _ := json.Marshal(raw.Params.Update)
		if _, ok := object(b, []string{"revision", "validUntilUtc", "unavailable"}, []string{"profile"}); !ok {
			return ControlRequest{}, bad
		}
	}
	if r.Params.Update != nil && r.Params.Update.Profile != nil {
		var p map[string]json.RawMessage
		if json.Unmarshal(raw.Params.Update["profile"], &p) != nil || !has(p, "validUntilUtc") || !profileShape(raw.Params.Update["profile"]) {
			return ControlRequest{}, bad
		}
	}
	return r, nil
}

// ValidateJSON also protects the outer private/service host envelope against
// duplicate keys, nested allocation abuse and concatenated JSON messages.
func ValidateJSON(data []byte) error {
	bad := errors.New("INVALID_REQUEST")
	if len(data) == 0 || len(data) > 65536 {
		return bad
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if uniqueJSON(d, 0) != nil {
		return bad
	}
	if _, err := d.Token(); err != io.EOF {
		return bad
	}
	return nil
}

func object(data []byte, required, optional []string) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil || m == nil {
		return nil, false
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, k := range required {
		if !has(m, k) {
			return nil, false
		}
		allowed[k] = true
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k, value := range m {
		if !allowed[k] {
			return nil, false
		}
		// encoding/json accepts null for bool/numeric/string fields and leaves
		// their Go zero values. Presence alone therefore cannot validate them.
		// The only nullable wire field in these request objects is the deadline;
		// typed decoding above checks every other scalar's JSON type.
		if k != "validUntilUtc" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, false
		}
	}
	return m, true
}
func profileShape(data []byte) bool {
	m, ok := object(data, []string{"key", "protocol", "entry", "auth", "validUntilUtc", "configVersion", "configFingerprint", "mode"}, []string{"protocolConfig", "expectedExitIpv4", "routingMode"})
	if !ok {
		return false
	}
	if raw, present := m["routingMode"]; present {
		var mode string
		if json.Unmarshal(raw, &mode) != nil || (mode != "global" && mode != "bypass_cn") {
			return false
		}
	}
	if _, ok = object(m["key"], []string{"environment", "dataSource", "accountKey", "kind", "sourceId"}, nil); !ok {
		return false
	}
	if _, ok = object(m["entry"], []string{"host", "port"}, nil); !ok {
		return false
	}
	_, ok = object(m["auth"], []string{"username", "password"}, nil)
	return ok
}

// encoding/json otherwise accepts duplicate keys (last value wins).
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("INVALID_REQUEST")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("INVALID_REQUEST")
			}
			seen[s] = true
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("INVALID_REQUEST")
	}
	_, err = d.Token()
	return err
}
func has(m map[string]json.RawMessage, k string) bool { _, ok := m[k]; return ok }
