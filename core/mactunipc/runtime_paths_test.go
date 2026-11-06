package mactunipc

import (
	"io"
	"os"
	"path"
	"strings"
	"testing"
)

func TestRuntimeDiscoveryStaysUnderProtectedLibraryParent(t *testing.T) {
	// An absent durable marker must not send startup back through daemon's
	// group-writable /private/var/run, including the unsupported marker probe.
	_, err := readIntent(func(name string) (io.ReadCloser, error) {
		if path.Dir(name) != "/Library/Application Support/BlueGrass" && path.Dir(name) != "/Library/Application Support/BlueGrass/Run" {
			t.Fatalf("untrusted runtime ancestor consulted: %s", name)
		}
		return nil, os.ErrNotExist
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(SocketPath, "/Library/Application Support/BlueGrass/Run/") {
		t.Fatal("socket escaped protected runtime directory")
	}
	if len(SocketPath) >= 104 {
		t.Fatal("production socket path exceeds Darwin sockaddr_un")
	}
}
