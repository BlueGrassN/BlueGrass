package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestConfiguredDialerIsUsedWithoutSystemDNSFallback(t *testing.T) {
	c, _, _ := fixture(t)
	cfg := c.config
	calls := 0
	cfg.DialContext = func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errors.New("controlled resolver unavailable")
	}
	checker, err := NewChecker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(context.Background(), "https://updates.example.test/manifest.json", Floor{}); err == nil || calls != 1 {
		t.Fatal("controlled transport bypassed", calls, err)
	}
}

var testNow = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func TestValidEncodingWrongManifestSignatureRejected(t *testing.T) {
	c, _, m := fixture(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(signed(t, other, m), Floor{}); err == nil {
		t.Fatal("wrong signing key accepted")
	}
}

func fixture(t *testing.T) (*Checker, ed25519.PrivateKey, Manifest) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewChecker(Config{
		Keys:         map[string]ed25519.PublicKey{"test": pub},
		Publisher:    strings.Repeat("1", 64),
		AllowedHosts: []string{"updates.example.test"},
		Channel:      "beta", Arch: "amd64", OSVersion: "10.0.19045",
		CurrentVersion: "1.0.0", CurrentBuild: 10, InstallationID: "test-installation",
	})
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return testNow }
	digest := sha256.Sum256([]byte("package"))
	m := Manifest{Schema: 1, Sequence: 5,
		Published: "2026-10-11T00:00:00Z", Expires: "2026-10-18T00:00:00Z",
		Releases: []Package{{Version: "1.1.0-beta.1", Build: 11, MinimumVersion: "1.0.0",
			Channel: "beta", Platform: "windows", Arch: "amd64", MinimumOS: "10.0.17763",
			URLs:   []string{"https://updates.example.test/setup.exe"},
			SHA256: hex.EncodeToString(digest[:]), Size: 7, Rollout: 100,
			Notes: "Beta", Published: "2026-10-11T00:00:00Z"}},
	}
	return c, priv, m
}

func signed(t *testing.T, priv ed25519.PrivateKey, m Manifest) []byte {
	t.Helper()
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return signRaw(t, priv, payload)
}

func signRaw(t *testing.T, priv ed25519.PrivateKey, raw []byte) []byte {
	t.Helper()
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte("BlueGrass update manifest v1\ntest\n"), canonical...))
	result, err := json.Marshal(map[string]any{"key_id": "test", "payload": json.RawMessage(raw), "signature": base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCanonicalRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	got, err := CanonicalJSON([]byte(` { "z": [2,true], "a": "<x>" } `))
	if err != nil || string(got) != `{"a":"\u003cx\u003e","z":[2,true]}` {
		t.Fatalf("canonical %s: %v", got, err)
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"x":{"a":0,"a":1}}`, `{"n":1.0}`, `{"n":1e0}`, `{"n":-0}`, `{"n":9007199254740992}`, `{} {}`, `"\ud800"`, string([]byte{'"', 0xff, '"'}), strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40), strings.Repeat(" ", MaxManifestBytes+1) + "{}"} {
		if _, err := CanonicalJSON([]byte(raw)); err == nil {
			t.Errorf("accepted invalid JSON: %.100s", raw)
		}
	}
}

func TestStrictSchemaRejectsCaseAliasesAndOptionalNull(t *testing.T) {
	for _, extra := range []string{`"SCHEMA":1,`, `"VERSION":"1.1.0-beta.1",`, `"PACKAGE_ID":"",`, `"package_id":null,`} {
		t.Run(extra, func(t *testing.T) {
			c, key, m := fixture(t)
			payload, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			target := `"version":`
			if strings.Contains(extra, "SCHEMA") {
				target = `"schema":`
			}
			raw := strings.Replace(string(payload), target, extra+target, 1)
			if _, err := c.Verify(signRaw(t, key, []byte(raw)), Floor{}); err == nil {
				t.Fatal("noncanonical schema field accepted")
			}
		})
	}
	c, key, m := fixture(t)
	raw := strings.Replace(string(signed(t, key, m)), `"key_id":`, `"KEY_ID":"test","key_id":`, 1)
	if _, err := c.Verify([]byte(raw), Floor{}); err == nil {
		t.Fatal("envelope alias accepted")
	}
}

func TestVerifySignedManifestAndRollback(t *testing.T) {
	c, key, m := fixture(t)
	raw := signed(t, key, m)
	r, err := c.Verify(raw, Floor{})
	if err != nil || r.Candidate == nil || r.Sequence != 5 || r.Unsupported {
		t.Fatalf("result %+v: %v", r, err)
	}
	if r.Candidate.Package().Version != "1.1.0-beta.1" {
		t.Fatal("wrong version")
	}
	if _, err := c.Verify(raw, Floor{Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256}); err != nil {
		t.Fatal("same manifest must permit resume", err)
	}
	for _, floor := range []Floor{{Sequence: 6, ManifestSHA256: strings.Repeat("a", 64)}, {Sequence: 5, ManifestSHA256: strings.Repeat("a", 64)}, {Sequence: 5}, {Build: 12, Version: "1.2.0"}} {
		result, err := c.Verify(raw, floor)
		if err == nil && result.Candidate != nil {
			t.Errorf("rollback accepted: %+v", floor)
		}
	}
	raw[len(raw)-8] ^= 1
	if _, err := c.Verify(raw, Floor{}); err == nil {
		t.Fatal("tampered envelope accepted")
	}
}

func TestRejectInvalidSignedFields(t *testing.T) {
	c, key, original := fixture(t)
	cases := map[string]func(*Manifest){
		"schema":            func(m *Manifest) { m.Schema = 2 },
		"zero sequence":     func(m *Manifest) { m.Sequence = 0 },
		"expired":           func(m *Manifest) { m.Expires = "2026-10-11T12:00:00Z" },
		"future":            func(m *Manifest) { m.Published = "2026-10-13T00:00:00Z" },
		"long validity":     func(m *Manifest) { m.Expires = "2027-01-01T00:00:00Z" },
		"size zero":         func(m *Manifest) { m.Releases[0].Size = 0 },
		"size cap":          func(m *Manifest) { m.Releases[0].Size = MaxPackageBytes + 1 },
		"digest":            func(m *Manifest) { m.Releases[0].SHA256 = strings.Repeat("A", 64) },
		"url http":          func(m *Manifest) { m.Releases[0].URLs = []string{"http://updates.example.test/setup.exe"} },
		"url other host":    func(m *Manifest) { m.Releases[0].URLs = []string{"https://updates.example.test.evil.test/setup.exe"} },
		"url credentials":   func(m *Manifest) { m.Releases[0].URLs = []string{"https://user:secret@updates.example.test/setup.exe"} },
		"url fragment":      func(m *Manifest) { m.Releases[0].URLs = []string{"https://updates.example.test/setup.exe#x"} },
		"rollout":           func(m *Manifest) { m.Releases[0].Rollout = 101 },
		"version":           func(m *Manifest) { m.Releases[0].Version = "01.1.0" },
		"min version":       func(m *Manifest) { m.Releases[0].MinimumVersion = "3.0.0" },
		"os":                func(m *Manifest) { m.Releases[0].MinimumOS = "10.foo.0" },
		"platform":          func(m *Manifest) { m.Releases[0].Platform = "shell" },
		"duplicate release": func(m *Manifest) { m.Releases = append(m.Releases, m.Releases[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := original
			m.Releases = append([]Package(nil), original.Releases...)
			mutate(&m)
			if _, err := c.Verify(signed(t, key, m), Floor{}); err == nil {
				t.Fatal("invalid signed field accepted")
			}
		})
	}
	payload, _ := json.Marshal(original)
	for _, raw := range []string{strings.Replace(string(payload), `"schema":1`, `"schema":1,"command":"execute"`, 1), strings.Replace(string(payload), `"force":false,`, "", 1)} {
		if _, err := c.Verify(signRaw(t, key, []byte(raw)), Floor{}); err == nil {
			t.Fatal("unknown/missing field accepted")
		}
	}
}

func TestApplicabilityAndMinimumVersion(t *testing.T) {
	for _, name := range []string{"channel", "arch", "os", "rollout", "old build", "old version"} {
		t.Run(name, func(t *testing.T) {
			c, key, m := fixture(t)
			switch name {
			case "channel":
				m.Releases[0].Channel = "stable"
			case "arch":
				m.Releases[0].Arch = "arm64"
			case "os":
				m.Releases[0].MinimumOS = "10.0.99999"
			case "rollout":
				m.Releases[0].Rollout = 0
			case "old build":
				m.Releases[0].Build = 10
			case "old version":
				m.Releases[0].Version = "0.9.0"
				m.Releases[0].MinimumVersion = "0.9.0"
			}
			r, err := c.Verify(signed(t, key, m), Floor{})
			if err != nil || r.Candidate != nil {
				t.Fatalf("applicability: %+v %v", r, err)
			}
		})
	}
	c, key, m := fixture(t)
	m.Releases[0].MinimumVersion = "1.1.0-beta.1"
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil || !r.Unsupported || r.Candidate == nil {
		t.Fatalf("minimum version: %+v %v", r, err)
	}
}

func TestCandidateCannotBeForgedOrMutated(t *testing.T) {
	c, key, m := fixture(t)
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil {
		t.Fatal(err)
	}
	p := r.Candidate.Package()
	p.URLs[0] = "https://evil.test/a"
	if r.Candidate.Package().URLs[0] != "https://updates.example.test/setup.exe" {
		t.Fatal("candidate exposed mutable state")
	}
	if r.Candidate.ID() == "" {
		t.Fatal("missing cache/consent identity")
	}
}

func TestInstalledFloorCannotBeForgottenInProcess(t *testing.T) {
	c, key, m := fixture(t)
	if _, err := c.Verify(signed(t, key, m), Floor{Build: 20, Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	if _, err := c.Verify(signed(t, key, m), Floor{}); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil || r.Candidate != nil {
		t.Fatalf("forgot installed rollback floor: %+v %v", r, err)
	}
}

func TestManifestMinimumVersionIsSignedMinimum(t *testing.T) {
	c, key, m := fixture(t)
	m.Releases[0].MinimumVersion = "0.9.0"
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil || r.MinimumVersion != "0.9.0" || r.Unsupported {
		t.Fatalf("wrong minimum: %+v %v", r, err)
	}
}

func TestManifestRejectsConflictingEntriesForSameTarget(t *testing.T) {
	c, key, m := fixture(t)
	p := m.Releases[0]
	p.Build++
	p.Version = "1.2.0"
	m.Releases = append(m.Releases, p)
	if _, err := c.Verify(signed(t, key, m), Floor{}); err == nil {
		t.Fatal("ambiguous per-target minimum/version accepted")
	}
}

func TestVersionFloorCannotFallWhenBuildFloorRises(t *testing.T) {
	c, key, m := fixture(t)
	if _, err := c.Verify(signed(t, key, m), Floor{Build: 20, Version: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	if _, err := c.Verify(signed(t, key, m), Floor{Build: 21, Version: "1.5.0"}); err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	m.Releases[0].Build = 22
	m.Releases[0].Version = "1.6.0"
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil || r.Candidate != nil {
		t.Fatalf("version high-water mark was lowered: %+v %v", r, err)
	}
}

func TestInvalidLaterReleaseNeverReturnsCandidate(t *testing.T) {
	c, key, m := fixture(t)
	bad := m.Releases[0]
	bad.Channel = "invalid"
	m.Releases = append(m.Releases, bad)
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err == nil || r.Candidate != nil {
		t.Fatal("failed verification returned a candidate")
	}
}
