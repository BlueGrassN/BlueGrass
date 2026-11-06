//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupPolicyReopensExpiredDPAPIHistoryWithoutNetwork(t *testing.T) {
	cfg, mem, raw := flowFixture(t)
	e, _ := testEngine(t, cfg, mem, raw)
	var env struct {
		Payload update.Manifest `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Keys = map[string]string{"test": base64.StdEncoding.EncodeToString(pub)}
	old := time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)
	env.Payload.Published = old.Format(time.RFC3339)
	env.Payload.Expires = old.Add(time.Hour).Format(time.RFC3339)
	env.Payload.Releases[0].Published = env.Payload.Published
	env.Payload.Releases[0].MinimumVersion = "1.1.0"
	payload, _ := json.Marshal(env.Payload)
	payload, err = update.CanonicalJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte("BlueGrass update manifest v1\ntest\n"), payload...))
	raw, _ = json.Marshal(map[string]any{"key_id": "test", "payload": json.RawMessage(payload), "signature": base64.StdEncoding.EncodeToString(sig)})
	sum := sha256.Sum256(payload)
	e.state.Manifest = raw
	e.state.Floor.Sequence = env.Payload.Sequence
	e.state.Floor.ManifestSHA256 = hex.EncodeToString(sum[:])
	dir := filepath.Join(t.TempDir(), "updates")
	s, err := openDiskStore(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(e.state); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	s, err = openDiskStore(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reopened, err := newEngine(cfg, runtimeInfo{Publisher: strings.Repeat("1", 64), OSVersion: "10.0.19045", Arch: "amd64"}, s)
	if err != nil {
		t.Fatal(err)
	}
	reopened.fetch = func(context.Context, string) ([]byte, error) { t.Fatal("startup accessed feed"); return nil, nil }
	p, err := reopened.readStartupPolicy()
	if err != nil || p.State != "verified" || !p.Unsupported || !p.ManifestExpired || p.MinimumVersion != "1.1.0" {
		t.Fatal("expired minimum lost on restart", p, err)
	}
	if _, err := reopened.execute(context.Background(), request{Op: "download", CandidateID: strings.Repeat("a", 64), AllowMetered: true}, nil); err != errState {
		t.Fatal("expired history authorized download", err)
	}
}

func TestStartupStateRejectsUnknownOrMissingNestedFloorFields(t *testing.T) {
	for _, floor := range []string{
		`{"sequence":1,"manifest_sha256":"a","version":"1.0.0","build":10,"extra":true}`,
		`{"sequence":1,"manifest_sha256":"a","version":"1.0.0"}`,
	} {
		s, err := openDiskStore(filepath.Join(t.TempDir(), "updates"), false)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		b, err := cryptState([]byte(`{"schema":1,"installation_id":"`+strings.Repeat("a", 64)+`","floor":`+floor+`,"manifest":{}}`), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Dir(), "state.dpapi"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(); err != errState {
			t.Fatal("corrupt nested schema accepted", err)
		}
	}
}
