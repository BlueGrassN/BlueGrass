package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustedPublisherRejectsUnsignedAndMissingFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "unsigned.exe")
	if err := os.WriteFile(file, []byte("not a signed PE"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, file + ".missing"} {
		if publisher, err := TrustedPublisher(p); err == nil || publisher != "" {
			t.Fatalf("unsigned file trusted: %s %v", publisher, err)
		}
		if err := VerifyPublisher(p, strings.Repeat("1", 64)); err == nil {
			t.Fatal("unsigned publisher accepted")
		}
	}
	if err := VerifyPublisher(file, ""); err == nil {
		t.Fatal("empty expected identity accepted")
	}
}

// Opt-in, read-only integration probe. The source is copied to a temporary
// fixture; no signing keys, certificates or trust-store entries are created.
func TestTrustedPublisherSignedFixture(t *testing.T) {
	source := os.Getenv("BLUEGRASS_SIGNED_FIXTURE")
	expected := os.Getenv("BLUEGRASS_SIGNED_FIXTURE_CERT_SHA256")
	if source == "" || expected == "" {
		t.Skip("no external trusted signed fixture configured")
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	path := filepath.Join(t.TempDir(), "signed.exe")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("fixture copy: %v %v", copyErr, closeErr)
	}
	actual, err := TrustedPublisher(path)
	if err != nil || actual != expected {
		t.Fatalf("signer extraction: got %s want %s: %v", actual, expected, err)
	}
	if err := VerifyPublisher(path, expected); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPublisher(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong certificate accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	c, key, m := fixture(t)
	c.config.Publisher = expected
	m.Releases[0].Size = int64(len(data))
	m.Releases[0].SHA256 = hex.EncodeToString(digest[:])
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Revalidate(context.Background(), r.Candidate, path); err != nil {
		t.Fatalf("combined hash + signer check: %v", err)
	}
}
