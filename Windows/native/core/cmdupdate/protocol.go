package cmdupdate

import (
	"context"
	"encoding/json"
	"io"
)

type request struct {
	Version        int    `json:"v"`
	ID             string `json:"id"`
	Op             string `json:"op"`
	CandidateID    string `json:"candidateId,omitempty"`
	AllowMetered   bool   `json:"allowMetered,omitempty"`
	InstallConsent bool   `json:"installConsent,omitempty"`
}
type snapshot struct {
	State          string `json:"state"`
	CandidateID    string `json:"candidateId,omitempty"`
	Version        string `json:"version,omitempty"`
	Build          uint64 `json:"build,omitempty"`
	Size           int64  `json:"size,omitempty"`
	Notes          string `json:"notes,omitempty"`
	MinimumVersion string `json:"minimumVersion,omitempty"`
	Unsupported    bool   `json:"unsupported"`
	Downloaded     int64  `json:"downloaded"`
	Remaining      int64  `json:"remaining"`
	FromCache      bool   `json:"fromCache"`
}
type startupPolicy struct {
	State           string `json:"state"`
	MinimumVersion  string `json:"minimumVersion,omitempty"`
	Unsupported     bool   `json:"unsupported"`
	ManifestExpired bool   `json:"manifestExpired"`
}
type reply struct {
	Version       int            `json:"v"`
	ID            string         `json:"id"`
	Event         string         `json:"event,omitempty"`
	OK            bool           `json:"ok"`
	Error         string         `json:"error,omitempty"`
	Snapshot      *snapshot      `json:"snapshot,omitempty"`
	StartupPolicy *startupPolicy `json:"startupPolicy,omitempty"`
}

func hexID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func decodeRequest(raw []byte) (request, error) {
	var r request
	if len(raw) > 4096 || strictObject(raw, &r, "v", "id", "op", "candidateId", "allowMetered", "installConsent") != nil {
		return r, errRequest
	}
	if r.Version != 1 || !hexID(r.ID, 32) {
		return r, errRequest
	}
	switch r.Op {
	case "startup_policy":
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if len(fields) != 3 {
			return r, errRequest
		}
	case "check":
		if r.CandidateID != "" || r.AllowMetered || r.InstallConsent {
			return r, errRequest
		}
	case "download":
		if !hexID(r.CandidateID, 64) || r.InstallConsent {
			return r, errRequest
		}
	case "launch":
		if !hexID(r.CandidateID, 64) || r.AllowMetered {
			return r, errRequest
		}
	default:
		return r, errRequest
	}
	return r, nil
}

// Run reads one bounded JSON object until EOF and emits progress/final JSON lines.
// Configuration errors return before opening state, checking trust or networking.
func Run(ctx context.Context, input io.Reader, output io.Writer) error {
	encoder := json.NewEncoder(output)
	raw, readErr := io.ReadAll(io.LimitReader(input, 4097))
	r, err := decodeRequest(raw)
	if readErr != nil {
		err = errRequest
	}
	finish := func(s *snapshot, err error) error {
		code := ""
		if err != nil {
			code = safeCode(err)
		}
		return encoder.Encode(reply{Version: 1, ID: r.ID, OK: err == nil, Error: code, Snapshot: s})
	}
	if err != nil {
		return finish(nil, errRequest)
	}
	cfg, err := compiledRelease()
	if err != nil {
		return finish(nil, err)
	}
	e, close, err := openUserEngineMode(cfg, r.Op == "startup_policy")
	if err != nil {
		return finish(nil, err)
	}
	defer close()
	if r.Op == "startup_policy" {
		p, err := e.readStartupPolicy()
		if err != nil {
			return finish(nil, err)
		}
		return encoder.Encode(reply{Version: 1, ID: r.ID, OK: true, StartupPolicy: &p})
	}
	var writeErr error
	s, err := e.execute(ctx, r, func(s snapshot) {
		if writeErr == nil {
			writeErr = encoder.Encode(reply{Version: 1, ID: r.ID, Event: "progress", OK: true, Snapshot: &s})
		}
	})
	if writeErr != nil {
		return writeErr
	}
	if err != nil {
		return finish(nil, err)
	}
	return finish(&s, nil)
}
func safeCode(err error) string {
	for _, known := range []error{errUnconfigured, errRelease, errRequest, errState, errBusy, errTrust, errCheck, errCandidate, errWaitingNetwork, errConsent, errDownload, errPackage, errLaunch, errLaunchCancelled, errControlDNS} {
		if err == known {
			return known.Error()
		}
	}
	return "UPDATE_FAILED"
}
