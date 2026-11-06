package mactun

import (
	"errors"
	"os"
	"path"
	"strings"
	"testing"

	"bluegrass/native/core/mactunipc"
)

type memoryIntentFiles struct {
	files           map[string][]byte
	writes, removes int
	synced          string
	syncErr         error
}

func (f *memoryIntentFiles) Read(p string) ([]byte, error) {
	b, ok := f.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}
func (f *memoryIntentFiles) Write(p string, b []byte) error {
	f.writes++
	f.files[p] = append([]byte(nil), b...)
	return nil
}
func (f *memoryIntentFiles) Remove(p string) error        { f.removes++; delete(f.files, p); return nil }
func (f *memoryIntentFiles) SyncDirectory(p string) error { f.synced = p; return f.syncErr }

func TestIntentSurvivesRuntimeLossAndSyncsPersistentParent(t *testing.T) {
	f := &memoryIntentFiles{files: map[string][]byte{}}
	if err := persistIntent(501, true, f); err != nil {
		t.Fatal(err)
	}
	for name := range f.files {
		if strings.HasPrefix(name, mactunipc.RuntimeDirectory+"/") {
			delete(f.files, name)
		}
	}
	raw, err := f.Read(mactunipc.IntentPath)
	if err != nil {
		t.Fatal("reboot removed protection discovery", err)
	}
	marker, err := mactunipc.DecodeIntent(raw)
	if err != nil || marker.OwnerUID != 501 || !marker.Active {
		t.Fatal(marker, err)
	}
	if path.Dir(mactunipc.IntentPath) != path.Dir(mactunipc.StateDirectory) {
		t.Fatal("marker must be public sibling of private journal directory")
	}
	if err = persistIntent(501, false, f); err != nil {
		t.Fatal(err)
	}
	if f.synced != path.Dir(mactunipc.IntentPath) || f.removes != 1 {
		t.Fatal("wrong removal durability parent", f.synced)
	}
}

func TestIntentOwnershipAndDeleteDurabilityErrors(t *testing.T) {
	f := &memoryIntentFiles{files: map[string][]byte{}}
	if err := persistIntent(501, true, f); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		if err := persistIntent(502, active, f); !errors.Is(err, ErrOwner) {
			t.Fatal(err)
		}
	}
	if f.removes != 0 || f.writes != 1 {
		t.Fatal("foreign owner changed discovery")
	}
	f.syncErr = errors.New("sync failed")
	if err := persistIntent(501, false, f); !errors.Is(err, f.syncErr) {
		t.Fatal("acknowledged unconfirmed directory sync", err)
	}
}
