//go:build windows

package tunengine

import (
	"golang.zx2c4.com/wintun"
	"os"
	"path/filepath"
	"testing"
)

func TestWintunRejectsMissingAndModifiedAppLocalDriver(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "wintun.dll")
	if _, err := verifiedWintunFile(missing); err == nil {
		t.Fatal("missing driver accepted")
	}
	if err := os.WriteFile(missing, []byte("unapproved driver"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedWintunFile(missing); err == nil {
		t.Fatal("modified driver accepted")
	}
}

// Run the compiled test executable from a foreign CWD, once with and once
// without the approved DLL beside it. This loads DLL code but opens no adapter.
func TestWintunLoadFromExecutableDirectory(t *testing.T) {
	mode := os.Getenv("BLUEGRASS_TEST_WINTUN_LOAD")
	if mode == "" {
		t.Skip("isolated packaged-loader check")
	}
	err := loadWintun()
	if mode == "absent" {
		if err == nil {
			t.Fatal("loaded Wintun from CWD or system fallback")
		}
		return
	}
	if mode != "present" {
		t.Fatal("unknown loader mode")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := wintun.Version(); got != "0.14.1" {
		t.Fatal("wrong loaded Wintun version", got)
	}
}

func TestWintunApprovedDriverMatchesRuntimePin(t *testing.T) {
	path := filepath.Join("..", "..", "..", "scripts", "assets", "wintun", "amd64", "wintun.dll")
	f, err := verifiedWintunFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// The verified handle denies writes until the exact DLL has been loaded.
	if altered, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		altered.Close()
		t.Fatal("verified driver permits concurrent replacement")
	}
}
