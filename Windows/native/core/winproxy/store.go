package winproxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxRecordBytes = 1 << 20

type fileStore struct {
	path               string
	protect, unprotect func([]byte) ([]byte, error)
	replace            func(string, string) error
}

func (s *fileStore) Load() (*Record, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sealed, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(sealed) == 0 || len(sealed) > maxRecordBytes {
		return nil, ErrInvalidRecord
	}
	plain, err := s.unprotect(sealed)
	if err != nil {
		return nil, fmt.Errorf("decrypt record: %w", err)
	}
	defer clear(plain)
	if len(plain) > maxRecordBytes {
		return nil, ErrInvalidRecord
	}
	var r Record
	if json.Unmarshal(plain, &r) != nil || !validRecord(r) {
		return nil, ErrInvalidRecord
	}
	// Records are internal, versioned, canonical JSON. Reject missing/duplicate/
	// unknown fields rather than silently restoring a lossy partial snapshot.
	canonical, err := json.Marshal(r)
	if err != nil {
		return nil, ErrInvalidRecord
	}
	defer clear(canonical)
	if !bytes.Equal(canonical, plain) {
		return nil, ErrInvalidRecord
	}
	return &r, nil
}

func (s *fileStore) Save(r Record) error {
	if !validRecord(r) {
		return ErrInvalidRecord
	}
	plain, err := json.Marshal(r)
	if err != nil {
		return ErrInvalidRecord
	}
	defer clear(plain)
	if len(plain) > maxRecordBytes {
		return ErrInvalidRecord
	}
	sealed, err := s.protect(plain)
	if err != nil {
		return fmt.Errorf("encrypt record: %w", err)
	}
	if len(sealed) == 0 || len(sealed) > maxRecordBytes {
		return ErrInvalidRecord
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".winproxy-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if _, err := f.Write(sealed); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Never delete the destination first. Failure leaves the old backup intact.
	return s.replace(name, s.path)
}

func (s *fileStore) Delete() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
