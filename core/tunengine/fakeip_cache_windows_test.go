//go:build windows

package tunengine

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFakeIPCacheProtectedDescriptorPolicy(t *testing.T) {
	for _, tc := range []struct {
		sddl  string
		valid bool
	}{
		{cacheSDDL, true},
		{"O:SYG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true},
		{"O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)", false},
		{"O:BUG:BUD:P(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;SY)", false},
		{"O:BAG:BAD:P(A;IO;FA;;;SY)(A;;FA;;;BA)", false},
		{"O:BAG:BAD:P(A;;FR;;;SY)(A;;FA;;;BA)", false},
		{"O:BAG:BAD:P", false},
	} {
		sd, err := windows.SecurityDescriptorFromString(tc.sddl)
		if err != nil {
			t.Fatal(err)
		}
		if got := protectedCacheDescriptor(sd); got != tc.valid {
			t.Errorf("descriptor acceptance=%v want %v", got, tc.valid)
		}
	}
}

func TestFakeIPCacheRejectsUnprotectedUserDirectoryWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fakeip.db")
	if db, err := openProtectedCache(path); err == nil {
		_ = db.close()
		t.Fatal("ordinary user temp directory accepted as protected service cache")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected user directory was mutated")
	}
}

func TestFakeIPCacheRejectsWindowsPathBypasses(t *testing.T) {
	for _, path := range []string{`relative.db`, `C:relative.db`, `C:\fakeip.db`, `\\server\share\fakeip.db`, `\\?\C:\cache\fakeip.db`,
		`C:\cache\..\fakeip.db`, `C:\cache\file:stream.db`, `C:\cache\*.db`, "C:\\cache\\bad\x00.db"} {
		if validCachePath(path) {
			t.Errorf("unsafe cache path accepted: %q", path)
		}
	}
}
