package mactunipc

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "github.com/sagernet/bbolt"
)

// Used only by the smoke fixture after both manager leases and the protected
// opener have established ownership. Read-only validation must never repair,
// empty, create or overwrite a leftover database just to make cleanup pass.
func smokeEmptyReleasedCache(path string, open func(string, int, os.FileMode) (*os.File, error)) error {
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: 100 * time.Millisecond, MaxSize: 32 << 20, OpenFile: open,
		Logger: &bolt.DefaultLogger{Logger: log.New(io.Discard, "", 0)}})
	if err != nil {
		return errors.New("released cache is corrupt, unavailable or still locked")
	}
	err = db.View(func(tx *bolt.Tx) error {
		if k, _ := tx.Cursor().First(); k != nil {
			return errors.New("released cache retains mappings or metadata")
		}
		return nil
	})
	return errors.Join(err, db.Close())
}

func TestSmokeReleasedCacheAcceptsOnlyEmptyDatabase(t *testing.T) {
	for _, bucket := range []string{"", "names", "addresses", "meta", "unexpected"} {
		t.Run("bucket-"+bucket, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fakeip.db")
			db, err := bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			if bucket != "" {
				err = db.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucket([]byte(bucket)); return err })
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			err = smokeEmptyReleasedCache(path, os.OpenFile)
			if (err == nil) != (bucket == "") {
				t.Fatalf("bucket %q: %v", bucket, err)
			}
			// Reopen independently to ensure validation did not delete contents.
			db, err = bolt.Open(path, 0600, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if bucket != "" {
				err = db.View(func(tx *bolt.Tx) error {
					if tx.Bucket([]byte(bucket)) == nil {
						return errors.New("validation erased retained cache")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSmokeReleasedCacheRejectsCorruptionAndLiveWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fakeip.db")
	if err := os.WriteFile(path, []byte("invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := smokeEmptyReleasedCache(path, os.OpenFile); err == nil {
		t.Fatal("corrupt cache accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = smokeEmptyReleasedCache(path, os.OpenFile); err == nil {
		t.Fatal("live writer accepted")
	}
}
