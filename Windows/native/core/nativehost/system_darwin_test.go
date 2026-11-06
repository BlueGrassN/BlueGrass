//go:build darwin

package nativehost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinFlockExcludesAnotherHostAndReleases(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	release, err := acquireDarwinProxyLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := acquireDarwinProxyLock(dir); err == nil {
		other()
		t.Fatal("concurrent host acquired proxy lock")
	}
	release()
	release, err = acquireDarwinProxyLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestDarwinLockRejectsSymlinkAndUnsafeDirectory(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "proxy.lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := acquireDarwinProxyLock(dir); err == nil {
		release()
		t.Fatal("symlink accepted")
	}
	_ = os.Remove(filepath.Join(dir, "proxy.lock"))
	_ = os.Chmod(dir, 0777)
	if release, err := acquireDarwinProxyLock(dir); err == nil {
		release()
		t.Fatal("writable directory accepted")
	}
}
func TestDarwinNeverAdvertisesTunnelOrPersistentProtection(t *testing.T) {
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.proxy = &testProxy{}
	for _, c := range h.capabilities() {
		if c == "tun" || c == "persistent_protection" {
			t.Fatal("unsupported protection advertised")
		}
	}
}
