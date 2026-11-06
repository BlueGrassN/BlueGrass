package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUserCleanupDeletesOnlyOwnedTokensAndPreservesSettings(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"session_v1.dpapi", "session_v1.dpapi.tmp", "preferences.dpapi", "device_id.dpapi", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("temporary fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupRecords(dir, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session_v1.dpapi", "session_v1.dpapi.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("token remains: %s", name)
		}
	}
	for _, name := range []string{"preferences.dpapi", "device_id.dpapi", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("non-token removed", name, err)
		}
	}
	if err := cleanupRecords(dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "preferences.dpapi")); !os.IsNotExist(err) {
		t.Fatal("explicit settings removal failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Fatal("unowned extension deleted")
	}
}

func TestUserCleanupRejectsDirectoriesInsteadOfRecursing(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "session_v1.dpapi")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupRecords(dir, false); err == nil {
		t.Fatal("recursive token deletion permitted")
	}
	if _, err := os.Stat(filepath.Join(nested, "keep")); err != nil {
		t.Fatal("nested data removed")
	}
}

type fakeProxy struct {
	recoverErr, errorState error
	applied                bool
	relinquished           bool
}

func (f *fakeProxy) Recover() error { return f.recoverErr }
func (f *fakeProxy) Relinquish() error {
	f.relinquished = true
	f.applied = false
	f.recoverErr = nil
	return nil
}
func (f *fakeProxy) State() (bool, error) { return f.applied, f.errorState }

func TestProxyCleanupFailsClosedOnIncompleteRecovery(t *testing.T) {
	for _, p := range []*fakeProxy{{recoverErr: errors.New("recovery failure")}, {applied: true}, {errorState: errors.New("read failure")}} {
		if err := recoverProxy(p); err == nil {
			t.Fatal("unconfirmed proxy restoration accepted")
		}
	}
	if err := recoverProxy(&fakeProxy{}); err != nil {
		t.Fatal(err)
	}
}
