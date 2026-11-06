package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const MaxManifestBytes = 256 * 1024
const MaxPackageBytes int64 = 512 * 1024 * 1024

// CanonicalJSON implements the BlueGrass v1 JSON profile, not general RFC 8785.
// It rejects duplicate keys, non-integer numbers, invalid Unicode and excessive
// nesting. encoding/json sorts keys and defines the profile's string escaping.
func CanonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes || !utf8.Valid(raw) {
		return nil, errors.New("invalid manifest size or UTF-8")
	}
	if err := checkSurrogates(raw); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	v, err := parseJSON(d, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return json.Marshal(v)
}

func parseJSON(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > 24 || *nodes > 16384 {
		return nil, errors.New("JSON complexity limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case json.Delim:
		switch v {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok {
					return nil, errors.New("invalid JSON key")
				}
				if _, exists := m[k]; exists {
					return nil, errors.New("duplicate JSON key")
				}
				item, err := parseJSON(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				m[k] = item
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("invalid object")
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				item, err := parseJSON(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				a = append(a, item)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("invalid array")
			}
			return a, nil
		default:
			return nil, errors.New("unexpected delimiter")
		}
	case json.Number:
		s := string(v)
		for _, c := range s {
			if c < '0' || c > '9' {
				return nil, errors.New("only nonnegative integers permitted")
			}
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n > 9007199254740991 {
			return nil, errors.New("integer outside safe range")
		}
		return v, nil
	default:
		return t, nil
	}
}

// encoding/json replaces lone UTF-16 surrogates; reject them before decoding so
// different malformed inputs cannot acquire the same signed representation.
func checkSurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return errors.New("truncated escape")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return errors.New("truncated Unicode escape")
		}
		v, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return err
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return errors.New("unpaired low surrogate")
		}
		if v < 0xd800 || v > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return errors.New("unpaired high surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return errors.New("invalid surrogate pair")
		}
		i += 6
	}
	return nil
}

func decodeObject(raw []byte, dst any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("expected JSON object")
	}
	allowed := make(map[string]bool, len(required)+1)
	for _, k := range required {
		allowed[k] = true
		v, ok := fields[k]
		if !ok || bytes.Equal(v, []byte("null")) {
			return fmt.Errorf("missing required field %s", k)
		}
	}
	if _, ok := dst.(*Package); ok {
		allowed["package_id"] = true
	}
	// DisallowUnknownFields alone accepts case-insensitive matches. The signed
	// schema has exact field names and forbids null even for optional fields.
	for k, v := range fields {
		if !allowed[k] || bytes.Equal(v, []byte("null")) {
			return errors.New("unknown or null schema field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(dst)
}
