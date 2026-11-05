//go:build darwin

package tunengine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	bolt "github.com/sagernet/bbolt"
)

func requireDarwinKernel(t *testing.T) {
	t.Helper()
	if os.Getenv("BLUEGRASS_DARWIN_KERNEL_TEST") != "1" {
		t.Skip("Darwin real-kernel/root acceptance requires BLUEGRASS_DARWIN_KERNEL_TEST=1")
	}
	if os.Geteuid() != 0 {
		t.Fatal("kernel acceptance explicitly requested but not root")
	}
}

func TestDarwinKernelProtectedCache(t *testing.T) {
	requireDarwinKernel(t)
	dir, err := os.MkdirTemp("/private/var/root", "bluegrass-cache-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fakeip.db")
	db, err := openProtectedCache(path)
	if err != nil {
		t.Fatal("protected cache creation", err)
	}
	if err = db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("test"))
		if e != nil {
			return e
		}
		return b.Put([]byte("name"), []byte("example.test"))
	}); err != nil {
		t.Fatal(err)
	}
	if other, err := openProtectedCache(path); err == nil {
		_ = other.close()
		t.Fatal("shared database ownership")
	}
	if err = db.close(); err != nil {
		t.Fatal(err)
	}
	db, err = openProtectedCache(path)
	if err != nil {
		t.Fatal(err)
	}
	err = db.View(func(tx *bolt.Tx) error {
		if string(tx.Bucket([]byte("test")).Get([]byte("name"))) != "example.test" {
			t.Error("persistent mapping lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.close(); err != nil {
		t.Fatal(err)
	}
	reject := func(p string) {
		t.Helper()
		if d, e := openProtectedCache(p); e == nil {
			_ = d.close()
			t.Fatal("unsafe cache accepted", p)
		}
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	reject(path)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err = os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	reject(path)
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	reject(link)
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err = os.Symlink(dir, sub); err != nil {
		t.Fatal(err)
	}
	reject(filepath.Join(sub, "fakeip.db"))
	if err = os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	reject(path)
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Mode bits alone cannot reject a Darwin ACL that grants another user access.
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read,write", path).CombinedOutput(); err != nil {
		t.Fatalf("ACL fixture: %v: %s", err, out)
	}
	reject(path)
}
