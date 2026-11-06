package winproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testFileStore(t *testing.T) *fileStore {
	t.Helper()
	// Fake encryption only; test deliberately never invokes DPAPI.
	seal := func(b []byte) ([]byte, error) {
		out := []byte("FAKE-CIPHER:")
		for _, c := range b {
			out = append(out, c^0xa5)
		}
		return out, nil
	}
	unseal := func(b []byte) ([]byte, error) {
		if !bytes.HasPrefix(b, []byte("FAKE-CIPHER:")) {
			return nil, errInjected
		}
		out := append([]byte(nil), b[len("FAKE-CIPHER:"):]...)
		for i := range out {
			out[i] ^= 0xa5
		}
		return out, nil
	}
	return &fileStore{path: filepath.Join(t.TempDir(), "backup"), protect: seal, unprotect: unseal, replace: os.Rename}
}
func testRecord() Record {
	return Record{Version: 1, Original: originalSettings(), Applied: targetSettings(), Phase: "prepared"}
}

func TestFileStoreRoundTripAndNoPlaintext(t *testing.T) {
	s := testFileStore(t)
	if r, err := s.Load(); r != nil || err != nil {
		t.Fatalf("missing record: %v", err)
	}
	r := testRecord()
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("old.invalid")) || !bytes.HasPrefix(raw, []byte("FAKE-CIPHER:")) {
		t.Fatal("plaintext written")
	}
	got, err := s.Load()
	if err != nil || got == nil || *got != r {
		t.Fatalf("roundtrip: %v, %v", got, err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("record not deleted")
	}
}

func TestFileStoreProtectAndReplaceFailuresPreserveOldRecord(t *testing.T) {
	for _, stage := range []string{"encrypt", "replace"} {
		t.Run(stage, func(t *testing.T) {
			s := testFileStore(t)
			r := testRecord()
			if err := s.Save(r); err != nil {
				t.Fatal(err)
			}
			old, _ := os.ReadFile(s.path)
			if stage == "encrypt" {
				s.protect = func([]byte) ([]byte, error) { return nil, errInjected }
			} else {
				s.replace = func(from, to string) error {
					if filepath.Dir(from) != filepath.Dir(to) {
						t.Fatal("replacement not on same filesystem")
					}
					current, _ := os.ReadFile(to)
					if !bytes.Equal(current, old) {
						t.Fatal("old record overwritten before atomic replacement")
					}
					if _, err := os.ReadFile(from); err != nil {
						t.Fatal(err)
					}
					return errInjected
				}
			}
			r.Phase = "applied"
			if err := s.Save(r); !errors.Is(err, errInjected) {
				t.Fatalf("want injected error, got %v", err)
			}
			current, _ := os.ReadFile(s.path)
			if !bytes.Equal(current, old) {
				t.Fatal("old recovery data lost")
			}
			files, _ := os.ReadDir(filepath.Dir(s.path))
			if len(files) != 1 {
				t.Fatal("temporary ciphertext leaked after failure")
			}
		})
	}
}

func TestFileStoreRejectsCorruptionWithoutDeleting(t *testing.T) {
	for _, data := range [][]byte{[]byte("not encrypted"), []byte("FAKE-CIPHER:bad"), nil} {
		t.Run(string(data), func(t *testing.T) {
			s := testFileStore(t)
			if err := os.WriteFile(s.path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if r, err := s.Load(); err == nil || r != nil {
				t.Fatal("corrupt data accepted")
			}
			if _, err := os.Stat(s.path); err != nil {
				t.Fatal("corrupt evidence removed")
			}
		})
	}
}

func TestFileStoreRejectsInvalidRecordAndOversize(t *testing.T) {
	s := testFileStore(t)
	r := testRecord()
	r.Version = 0
	if err := s.Save(r); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("got %v", err)
	}
	r = testRecord()
	r.Original.Server = string(bytes.Repeat([]byte("x"), 2<<20))
	if err := s.Save(r); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(s.path, bytes.Repeat([]byte("x"), 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Load(); r != nil || !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("got %v", err)
	}
}

func TestFileStoreAtomicCommitProducesNewCompleteRecord(t *testing.T) {
	s := testFileStore(t)
	r := testRecord()
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	r.Phase = "applied"
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got == nil || *got != r {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestFileStoreRejectsPartialAndDuplicateSnapshots(t *testing.T) {
	canonical, _ := json.Marshal(testRecord())
	for _, plain := range [][]byte{
		bytes.Replace(canonical, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1),
		bytes.Replace(canonical, []byte(`"Flags":15,`), nil, 1),
		bytes.Replace(canonical, []byte(`"Version":1`), []byte(`"Version":1,"Unexpected":true`), 1),
		append(append([]byte(nil), canonical...), []byte(` {}`)...),
	} {
		s := testFileStore(t)
		sealed, err := s.protect(plain)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.path, sealed, 0600); err != nil {
			t.Fatal(err)
		}
		if r, err := s.Load(); r != nil || !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("lossy record accepted: %v", err)
		}
	}
}
