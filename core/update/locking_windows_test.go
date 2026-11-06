//go:build windows

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactHandleBlocksMutationAndCacheLockIsReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.exe")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err == nil {
		writer.Close()
		f.Close()
		t.Fatal("verification permits concurrent writes")
	}
	if err := os.Remove(path); err == nil {
		f.Close()
		t.Fatal("verification permits path deletion")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(t.TempDir(), "cache.lock")
	unlock, err := lockCache(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockCache(lockPath); err == nil {
		second()
		unlock()
		t.Fatal("concurrent downloader acquired same cache")
	}
	unlock()
	unlock, err = lockCache(lockPath)
	if err != nil {
		t.Fatal("stale cache lock", err)
	}
	unlock()
}
