package mactunipc

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestSecurityAttributesRejectACLAndUncertainMetadata(t *testing.T) {
	absent := make([]byte, 12)
	binary.LittleEndian.PutUint32(absent, 12)
	binary.LittleEndian.PutUint32(absent[4:], 8)
	if !noACLAttributes(absent) {
		t.Fatal("absent ACL rejected")
	}
	sentinel := make([]byte, 56)
	binary.LittleEndian.PutUint32(sentinel, 56)
	binary.LittleEndian.PutUint32(sentinel[4:], 8)
	binary.LittleEndian.PutUint32(sentinel[8:], 44)
	binary.LittleEndian.PutUint32(sentinel[12:], 0x012cc16d)
	binary.LittleEndian.PutUint32(sentinel[48:], 0xffffffff)
	if !noACLAttributes(sentinel) {
		t.Fatal("NOACL sentinel rejected")
	}
	for _, change := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint32(b[48:], 1) }, // actual ACE
		func(b []byte) { binary.LittleEndian.PutUint32(b[48:], 0) }, // explicit ACL
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:], 0xfffffffc) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[8:], 4096) },
		func(b []byte) { b[12] = 0 }, func(b []byte) { b[52] = 1 },
	} {
		b := append([]byte(nil), sentinel...)
		change(b)
		if noACLAttributes(b) {
			t.Fatal("unsafe or malformed ACL accepted")
		}
	}
	for _, b := range [][]byte{nil, {0}, sentinel[:20]} {
		if noACLAttributes(b) {
			t.Fatal("truncated attributes accepted")
		}
	}
}

func TestDurableIntentDiscoveryDoesNotConsultRuntime(t *testing.T) {
	i, err := readIntent(func(path string) (io.ReadCloser, error) {
		if path != "/Library/Application Support/BlueGrass/tun-intent.json" {
			t.Fatalf("consulted runtime instead of durable intent: %s", path)
		}
		return io.NopCloser(strings.NewReader(`{"version":1,"ownerUid":501,"active":true,"recoveryRequired":true}`)), nil
	})
	if err != nil || i == nil || !i.Active || i.OwnerUID != 501 {
		t.Fatalf("%+v %v", i, err)
	}
}

func TestAbsentDurableIntentStillRefusesLegacyAndUnsafeMarkers(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		durableErr, errorLegacy error
		legacy                  bool
		wantErr                 bool
	}{
		{"absent", os.ErrNotExist, os.ErrNotExist, false, false},
		{"unreadable durable", os.ErrPermission, nil, false, true},
		{"unsupported runtime marker", os.ErrNotExist, nil, true, true},
		{"unsafe legacy parent", os.ErrNotExist, ErrHelperInvalid, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			legacyConsulted := false
			i, err := readIntent(func(path string) (io.ReadCloser, error) {
				if path == IntentPath {
					return nil, tt.durableErr
				}
				if path != RuntimeDirectory+"/intent.json" {
					t.Fatal(path)
				}
				legacyConsulted = true
				if tt.legacy {
					return io.NopCloser(strings.NewReader(`{}`)), nil
				}
				return nil, tt.errorLegacy
			})
			if i != nil || (err != nil) != tt.wantErr {
				t.Fatalf("%+v %v", i, err)
			}
			if errors.Is(tt.durableErr, os.ErrPermission) && legacyConsulted {
				t.Fatal("ignored unsafe durable marker")
			}
		})
	}
}

func TestInstallerACLFilterRequiresCleanCompleteListing(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if runtime.GOOS == "darwin" {
		awk = "/usr/bin/awk"
		err = nil
	}
	if err != nil {
		t.Skip("Darwin runner executes the production awk filter")
	}
	for _, tt := range []struct {
		name, input string
		ok          bool
	}{
		{"plain", "drwxr-xr-x 2 0 0 64 Jan 1 00:00 /safe\nBLUEGRASS_NO_ACL_CHECK_OK\n", true},
		{"xattr only", "-rwx------@ 1 0 0 42 Jan 1 00:00 /safe\nBLUEGRASS_NO_ACL_CHECK_OK\n", true},
		{"acl marker", "drwxr-xr-x+ 2 0 0 64 Jan 1 00:00 /unsafe\nBLUEGRASS_NO_ACL_CHECK_OK\n", false},
		{"acl masked by xattr", "-rwx------@ 1 0 0 42 Jan 1 00:00 /unsafe\n 0: user:501 allow write\nBLUEGRASS_NO_ACL_CHECK_OK\n", false},
		{"failed tool", "drwxr-xr-x 2 0 0 64 Jan 1 00:00 /safe\n", false},
		{"oversized line", strings.Repeat("x", 4097) + "\nBLUEGRASS_NO_ACL_CHECK_OK\n", false},
		{"extra data", "drwxr-xr-x 2 0 0 64 Jan 1 00:00 /safe\nBLUEGRASS_NO_ACL_CHECK_OK\nextra\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(awk, noACLListingFilter)
			cmd.Stdin = strings.NewReader(tt.input)
			if err := cmd.Run(); (err == nil) != tt.ok {
				t.Fatalf("filter acceptance=%v", err)
			}
		})
	}
}
