package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	value  *state
	fail   bool
	dir    string
	handed bool
}

func (s *memoryStore) Load() (*state, error) {
	if s.value == nil {
		return nil, nil
	}
	b, _ := json.Marshal(s.value)
	var v state
	_ = json.Unmarshal(b, &v)
	return &v, nil
}
func (s *memoryStore) Save(v state) error {
	if s.fail {
		return errors.New("fixture disk full")
	}
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &s.value)
	return nil
}
func (s *memoryStore) Handoff(_ string, _ state) error { s.handed = true; return nil }
func (s *memoryStore) Dir() string                     { return s.dir }

type testVerifier struct {
	*update.Checker
	ready     bool
	downloads int
}

func (v *testVerifier) Download(_ context.Context, c *update.Candidate, dir string, o update.DownloadOptions) (update.Ready, error) {
	v.downloads++
	if o.ConsentPackageID != c.ID() {
		return update.Ready{}, update.ErrWaitingNetwork
	}
	v.ready = true
	return update.Ready{Path: filepath.Join(dir, c.ID()+".exe"), PackageID: c.ID(), Size: c.Package().Size}, nil
}
func (v *testVerifier) Revalidate(context.Context, *update.Candidate, string) error {
	if !v.ready {
		return os.ErrNotExist
	}
	return nil
}

func flowFixture(t *testing.T) (releaseConfig, *memoryStore, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := releaseConfig{Schema: 1, Version: "1.0.0", Build: 10, Channel: "beta", Keys: map[string]string{"test": base64.StdEncoding.EncodeToString(pub)}, ManifestURLs: []string{"https://updates.example.test/manifest.json"}, AllowedHosts: []string{"updates.example.test"}}
	now := time.Now().UTC().Truncate(time.Second)
	payload := update.Manifest{Schema: 1, Sequence: 3, Published: now.Add(-time.Hour).Format(time.RFC3339), Expires: now.Add(time.Hour).Format(time.RFC3339), Releases: []update.Package{{Version: "1.1.0", Build: 11, MinimumVersion: "1.0.0", Channel: "beta", Platform: "windows", Arch: "amd64", MinimumOS: "10.0.17763", URLs: []string{"https://updates.example.test/setup.exe"}, SHA256: strings.Repeat("a", 64), Size: 7, Rollout: 100, Published: now.Add(-time.Hour).Format(time.RFC3339)}}}
	b, _ := json.Marshal(payload)
	canonical, err := update.CanonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte("BlueGrass update manifest v1\ntest\n"), canonical...))
	doc, _ := json.Marshal(map[string]any{"key_id": "test", "payload": payload, "signature": base64.StdEncoding.EncodeToString(sig)})
	return cfg, &memoryStore{dir: t.TempDir()}, doc
}

func testEngine(t *testing.T, cfg releaseConfig, s *memoryStore, raw []byte) (*engine, *testVerifier) {
	t.Helper()
	e, err := newEngine(cfg, runtimeInfo{Publisher: strings.Repeat("1", 64), OSVersion: "10.0.19045", Arch: "amd64"}, s)
	if err != nil {
		t.Fatal(err)
	}
	v := &testVerifier{Checker: e.verifier.(*update.Checker)}
	e.verifier = v
	e.fetch = func(context.Context, string) ([]byte, error) { return raw, nil }
	e.elevate = func(context.Context, string, string) error { return nil }
	return e, v
}

func TestCheckPersistsFloorBeforeExposingCandidateAndRestoresAcrossProcess(t *testing.T) {
	cfg, s, raw := flowFixture(t)
	e, _ := testEngine(t, cfg, s, raw)
	r, err := e.execute(context.Background(), request{Op: "check"}, nil)
	if err != nil || r.CandidateID == "" || s.value.Floor.Sequence != 3 {
		t.Fatalf("check: %+v %v", r, err)
	}
	e2, v := testEngine(t, cfg, s, raw)
	launches := 0
	e2.elevate = func(context.Context, string, string) error { launches++; return nil }
	if _, err := e2.execute(context.Background(), request{Op: "download", CandidateID: r.CandidateID}, nil); err != errWaitingNetwork || v.downloads != 0 {
		t.Fatal("download without consent", err)
	}
	if _, err := e2.execute(context.Background(), request{Op: "download", CandidateID: strings.Repeat("b", 64), AllowMetered: true}, nil); err != errCandidate {
		t.Fatal("forged candidate accepted", err)
	}
	ready, err := e2.execute(context.Background(), request{Op: "download", CandidateID: r.CandidateID, AllowMetered: true}, nil)
	if err != nil || ready.State != "ready" {
		t.Fatal(ready, err)
	}
	if _, err := e2.execute(context.Background(), request{Op: "launch", CandidateID: r.CandidateID}, nil); err != errConsent || launches != 0 {
		t.Fatal("launch without consent", err)
	}
	if _, err := e2.execute(context.Background(), request{Op: "launch", CandidateID: r.CandidateID, InstallConsent: true}, nil); err != nil || launches != 1 || !s.handed {
		t.Fatal("explicit launch failed", err)
	}
	v.ready = false
	if _, err := e2.execute(context.Background(), request{Op: "launch", CandidateID: r.CandidateID, InstallConsent: true}, nil); err != errPackage || launches != 1 {
		t.Fatal("changed cache launched", err)
	}
}

func TestStateWriteFailureNeverExposesNewCandidate(t *testing.T) {
	cfg, s, raw := flowFixture(t)
	s.fail = true
	e, v := testEngine(t, cfg, s, raw)
	r, err := e.execute(context.Background(), request{Op: "check"}, nil)
	if err != errState || r.CandidateID != "" || v.downloads != 0 {
		t.Fatal("failed durable floor accepted", r, err)
	}
}

func TestRestartRejectsTamperedManifestAndSequenceFloor(t *testing.T) {
	for _, tamper := range []string{"signature", "floor"} {
		t.Run(tamper, func(t *testing.T) {
			cfg, s, raw := flowFixture(t)
			e, _ := testEngine(t, cfg, s, raw)
			r, err := e.execute(context.Background(), request{Op: "check"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tamper == "signature" {
				s.value.Manifest = []byte(`{}`)
			} else {
				s.value.Floor.Sequence++
			}
			e2, v := testEngine(t, cfg, s, raw)
			if _, err := e2.execute(context.Background(), request{Op: "download", CandidateID: r.CandidateID, AllowMetered: true}, nil); err == nil || v.downloads != 0 {
				t.Fatal("tampered state accepted")
			}
		})
	}
}

func TestOfflineCheckOnlyUsesStillValidSignedCache(t *testing.T) {
	cfg, s, raw := flowFixture(t)
	e, _ := testEngine(t, cfg, s, raw)
	first, err := e.execute(context.Background(), request{Op: "check"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	r, err := e.execute(context.Background(), request{Op: "check"}, nil)
	if err != nil || !r.FromCache || r.CandidateID != first.CandidateID {
		t.Fatal(r, err)
	}
}

func TestElevatedPreparationEnforcesIndependentMachineFloorBeforeStaging(t *testing.T) {
	cfg, user, raw := flowFixture(t)
	e, _ := testEngine(t, cfg, user, raw)
	r, err := e.execute(context.Background(), request{Op: "check"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	info := runtimeInfo{Publisher: strings.Repeat("1", 64), OSVersion: "10.0.19045", Arch: "amd64"}
	machine := &memoryStore{dir: t.TempDir(), fail: true}
	if _, _, err := prepareApply(cfg, info, machine, *user.value, r.CandidateID); err != errState {
		t.Fatal("failed machine floor write accepted", err)
	}
	machine.fail = false
	if _, c, err := prepareApply(cfg, info, machine, *user.value, r.CandidateID); err != nil || c == nil || machine.value.Floor.Sequence != 3 {
		t.Fatal("machine floor not committed", err)
	}
	machine.value.Floor.Sequence = 4
	if _, _, err := prepareApply(cfg, info, machine, *user.value, r.CandidateID); err != errCandidate {
		t.Fatal("machine rollback floor bypassed", err)
	}
}
