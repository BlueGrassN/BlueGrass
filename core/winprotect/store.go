package winprotect

import (
	"bytes"
	"encoding/json"
	"errors"
)

const maxIntentBytes = 8192

// Preserve the exact v1 canonical representation for strict migration reads.
type intentV1 struct {
	Version       int    `json:"version"`
	ProtectIntent bool   `json:"protectIntent"`
	OwnerSID      string `json:"ownerSid"`
	AccountKey    string `json:"accountKey"`
	ResourceID    string `json:"resourceId"`
	Mode          string `json:"mode"`
	Since         string `json:"since"`
	Recovery      bool   `json:"recoveryRequired"`
}

func encodeIntent(r intent) ([]byte, error) {
	if !validIntent(r) {
		return nil, errors.New("invalid protection intent")
	}
	var value any = r
	if r.Version == 1 {
		value = intentV1{r.Version, r.ProtectIntent, r.OwnerSID, r.AccountKey, r.ResourceID, r.Mode, r.Since, r.Recovery}
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(b) > maxIntentBytes {
		return nil, errors.New("protection intent too large")
	}
	return b, nil
}
func decodeIntent(b []byte) (*intent, error) {
	var r intent
	if len(b) == 0 || len(b) > maxIntentBytes || json.Unmarshal(b, &r) != nil {
		return nil, errors.New("invalid protection journal")
	}
	canonical, err := encodeIntent(r)
	// Internal canonical records reject unknown, duplicate and missing fields.
	if err != nil || !bytes.Equal(b, canonical) {
		return nil, errors.New("invalid protection journal")
	}
	return &r, nil
}
