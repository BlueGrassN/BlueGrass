package cmdupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bluegrass/native/core/update"
)

func TestStartupPolicyUsesOnlyAcceptedStateWithoutNetworkOrWrites(t *testing.T) {
	cfg, s, raw := flowFixture(t)
	e, v := testEngine(t, cfg, s, raw)
	e.fetch = func(context.Context, string) ([]byte, error) {
		t.Fatal("offline startup reached feed")
		return nil, nil
	}
	p, err := e.readStartupPolicy()
	if err != nil || p.State != "no_history" || s.value != nil {
		t.Fatal(p, err)
	}
	r, err := v.Verify(raw, e.state.Floor)
	if err != nil {
		t.Fatal(err)
	}
	e.state.Manifest = raw
	e.state.Floor.Sequence = r.Sequence
	e.state.Floor.ManifestSHA256 = r.ManifestSHA256
	s.fail = true // a policy read must not save or hand off anything
	p, err = e.readStartupPolicy()
	if err != nil || p.State != "verified" || p.MinimumVersion != "1.0.0" || p.Unsupported || s.handed {
		t.Fatal(p, err)
	}
	e.state.Floor.Sequence++
	if _, err = e.readStartupPolicy(); !errors.Is(err, errState) {
		t.Fatal("bad history treated as absence", err)
	}
}

func TestStartupPolicyProtocolRejectsCandidateAndConsent(t *testing.T) {
	base := `{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"startup_policy"}`
	if _, err := decodeRequest([]byte(base)); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{`,"candidateId":"` + strings.Repeat("a", 64) + `"`, `,"allowMetered":true`, `,"installConsent":true`, `,"allowMetered":false`, `,"installConsent":false`, `,"candidateId":""`} {
		if _, err := decodeRequest([]byte(strings.TrimSuffix(base, "}") + extra + "}")); err == nil {
			t.Fatal("startup accepted action fields")
		}
	}
}

func TestStartupPolicyUnconfiguredRPCDoesNotOpenInstallationOrNetwork(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"startup_policy"}`), &out); err != nil {
		t.Fatal(err)
	}
	var r reply
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.OK || r.Error != "UPDATE_UNCONFIGURED" || r.StartupPolicy != nil || r.Snapshot != nil {
		t.Fatal(out.String(), err)
	}
}

func TestStartupPolicyCorruptEmptyHistoryFailsInsteadOfNoHistory(t *testing.T) {
	cfg, s, raw := flowFixture(t)
	e, _ := testEngine(t, cfg, s, raw)
	e.state.Floor.ManifestSHA256 = strings.Repeat("a", 64)
	if _, err := e.readStartupPolicy(); err != errState {
		t.Fatal("orphan hash became no history", err)
	}
	e.state.Floor = update.Floor{Version: cfg.Version, Build: cfg.Build}
	b, _ := json.Marshal(e.state)
	_ = json.Unmarshal(b, &s.value)
	if _, err := newEngine(cfg, runtimeInfo{Publisher: strings.Repeat("1", 64), OSVersion: "10.0.19045", Arch: "amd64"}, s); err != errState {
		t.Fatal("persisted empty record accepted", err)
	}
}
