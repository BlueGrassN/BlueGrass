package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStartupPolicyRetainsExpiredSignedMinimumWithoutCandidateAuthority(t *testing.T) {
	c, key, m := fixture(t)
	m.Releases[0].MinimumVersion = m.Releases[0].Version
	raw := signed(t, key, m)
	accepted, err := c.Verify(raw, Floor{})
	if err != nil {
		t.Fatal(err)
	}
	floor := Floor{Sequence: accepted.Sequence, ManifestSHA256: accepted.ManifestSHA256, Version: "1.0.0", Build: 10}
	c.now = func() time.Time { return testNow.Add(60 * 24 * time.Hour) }
	p, err := c.VerifyStartupPolicy(raw, floor)
	if err != nil || !p.Unsupported || !p.ManifestExpired || p.MinimumVersion != m.Releases[0].MinimumVersion {
		t.Fatal("expired restriction disappeared", p, err)
	}
	if _, err := c.Verify(raw, floor); err == nil {
		t.Fatal("startup read authorized expired candidate")
	}
	if c.validateCandidate(accepted.Candidate) == nil {
		t.Fatal("startup read revived expired package")
	}
}

func TestStartupPolicyDoesNotRelaxSchemaURLsOrPublicationWindow(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"schema": func(m *Manifest) { m.Schema = 2 },
		"future": func(m *Manifest) {
			m.Published = testNow.Add(time.Hour).Format(time.RFC3339)
			m.Expires = testNow.Add(2 * time.Hour).Format(time.RFC3339)
		},
		"long-validity": func(m *Manifest) { m.Expires = testNow.Add(60 * 24 * time.Hour).Format(time.RFC3339) },
		"http":          func(m *Manifest) { m.Releases[0].URLs = []string{"http://updates.example.test/setup.exe"} },
		"foreign-host":  func(m *Manifest) { m.Releases[0].URLs = []string{"https://foreign.example.test/setup.exe"} },
		"invalid-size":  func(m *Manifest) { m.Releases[0].Size = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c, key, m := fixture(t)
			mutate(&m)
			raw := signed(t, key, m)
			var env struct {
				Payload json.RawMessage `json:"payload"`
			}
			_ = json.Unmarshal(raw, &env)
			payload, err := CanonicalJSON(env.Payload)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(payload)
			if _, err := c.VerifyStartupPolicy(raw, Floor{Sequence: m.Sequence, ManifestSHA256: hex.EncodeToString(sum[:])}); err == nil {
				t.Fatal("invalid signed history accepted")
			}
		})
	}
}

func TestStartupPolicyRequiresExactAcceptedFloorAndSignature(t *testing.T) {
	c, key, m := fixture(t)
	raw := signed(t, key, m)
	r, err := c.Verify(raw, Floor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Floor{{}, {Sequence: r.Sequence - 1, ManifestSHA256: r.ManifestSHA256}, {Sequence: r.Sequence + 1, ManifestSHA256: r.ManifestSHA256}, {Sequence: r.Sequence, ManifestSHA256: strings.Repeat("a", 64)}} {
		if _, err := c.VerifyStartupPolicy(raw, f); err == nil {
			t.Fatal("unaccepted history became startup policy", f)
		}
	}
	f := Floor{Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256, Version: "1.0.0", Build: 10}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := c.VerifyStartupPolicy(signed(t, other, m), f); err == nil {
		t.Fatal("wrong signature accepted")
	}
	if _, err := c.VerifyStartupPolicy(raw, f); err != nil {
		t.Fatal(err)
	}
}

func TestStartupPolicyIsNotLimitedByRolloutOrInstallableOS(t *testing.T) {
	c, key, m := fixture(t)
	m.Releases[0].MinimumVersion = m.Releases[0].Version
	m.Releases[0].Rollout = 0
	m.Releases[0].MinimumOS = "10.0.99999"
	raw := signed(t, key, m)
	r, err := c.Verify(raw, Floor{})
	if err != nil || r.Candidate != nil {
		t.Fatal(r, err)
	}
	p, err := c.VerifyStartupPolicy(raw, Floor{Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256})
	if err != nil || !p.Unsupported || p.ManifestExpired {
		t.Fatal("rollout/OS bypassed minimum", p, err)
	}
}
