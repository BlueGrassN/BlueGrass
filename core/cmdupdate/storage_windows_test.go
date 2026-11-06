//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/update"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateStateRoundTripTamperAndExclusiveLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "updates")
	s, err := openDiskStore(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openDiskStore(dir, false); err == nil {
		other.Close()
		t.Fatal("concurrent session accepted")
	}
	cfg, _, raw := flowFixture(t)
	v := state{Schema: 1, InstallationID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Manifest: raw}
	v.Floor.Version = cfg.Version
	v.Floor.Build = cfg.Build
	if err := s.Save(v); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = openDiskStore(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Load()
	wantDocument, _ := update.CanonicalJSON(raw)
	var gotDocument []byte
	if got != nil {
		gotDocument, _ = update.CanonicalJSON(got.Manifest)
	}
	if err != nil || got == nil || got.InstallationID != v.InstallationID || !bytes.Equal(gotDocument, wantDocument) {
		t.Fatal("persisted state mismatch", err)
	}
	path := filepath.Join(dir, "state.dpapi")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0x80
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("tampered DPAPI floor accepted")
	}
}
func TestPathConfinementRejectsUNCRelativeAndAlternateStreams(t *testing.T) {
	for _, p := range []string{`relative`, `C:relative`, `\\server\share`, `C:\path\file:stream`, `C:\path\..\elsewhere`, `C:\path\trailing.`} {
		if localAbsolute(p) {
			t.Fatalf("accepted path %s", p)
		}
	}
	if !localAbsolute(`C:\Program Files\BlueGrass`) {
		t.Fatal("normal absolute path rejected")
	}
}
