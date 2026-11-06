//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartupOfflineTrustRetainsRevocationAndRejectsUnsignedFile(t *testing.T) {
	online, offline := wintrustProviderFlags(false), wintrustProviderFlags(true)
	if online != 0x2080 || offline != online|0x1000 {
		t.Fatal("offline policy altered revocation or enabled online URL retrieval")
	}
	path := filepath.Join(t.TempDir(), "unsigned.exe")
	if err := os.WriteFile(path, []byte("temporary unsigned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if signer, err := TrustedPublisherOffline(path); err == nil || signer != "" {
		t.Fatal("unsigned startup helper trusted", err)
	}
}
