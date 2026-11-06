//go:build darwin

package mactun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"bluegrass/native/core/mactunipc"
	"golang.org/x/sys/unix"
)

const privateRuntime = mactunipc.RuntimeDirectory
const journalPath = mactunipc.StateDirectory + "/network-v1.json"

func secureDir(path string, mode uint32) error {
	if path == "/" {
		return nil
	}
	if err := secureDir(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var st unix.Stat_t
	err := unix.Lstat(path, &st)
	if errors.Is(err, unix.ENOENT) {
		if err = unix.Mkdir(path, mode); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
		err = unix.Lstat(path, &st)
	}
	if err != nil {
		return err
	}
	if st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 {
		return ErrRecovery
	}
	if path == mactunipc.StateDirectory && st.Mode&0077 != 0 {
		return ErrRecovery
	}
	if path == filepath.Dir(mactunipc.IntentPath) && st.Mode&0777 != 0755 {
		return ErrRecovery
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if !noExtendedACL(fd) {
		return ErrRecovery
	}
	return nil
}
func secureOpen(path string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Mode&0022 != 0 || !noExtendedACL(fd) {
		_ = unix.Close(fd)
		return nil, ErrRecovery
	}
	return os.NewFile(uintptr(fd), path), nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func durableWrite(path string, data []byte, mode os.FileMode) error {
	// The parent is root-owned and non-writable to every unprivileged process.
	if f, err := secureOpen(path, unix.O_RDONLY, 0); err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".network-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		_, err = unix.FcntlInt(f.Fd(), unix.F_FULLFSYNC, 0)
	}
	// F_FULLFSYNC is supported by macOS regular files. Failure must retain the
	// old journal rather than advertising an unconfirmed durable mutation.
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func (b *darwinBackend) LoadJournal() (*Journal, error) {
	f, err := secureOpen(journalPath, unix.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		return nil, ErrRecovery
	}
	raw, err := io.ReadAll(io.LimitReader(f, 512*1024+1))
	if err != nil || len(raw) > 512*1024 {
		return nil, ErrRecovery
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var j Journal
	if err = decoder.Decode(&j); err != nil {
		return nil, ErrRecovery
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrRecovery
	}
	if err = validateJournal(j); err != nil {
		return nil, err
	}
	return &j, nil
}
func (b *darwinBackend) SaveJournal(j Journal) error {
	if err := validateJournal(j); err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return durableWrite(journalPath, raw, 0600)
}
func (b *darwinBackend) RemoveJournal() error {
	f, err := secureOpen(journalPath, unix.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	f.Close()
	if err = os.Remove(journalPath); err != nil {
		return err
	}
	return syncDirectory(mactunipc.StateDirectory)
}
func (b *darwinBackend) Marker(owner uint32, active bool) error {
	if err := secureDir(filepath.Dir(mactunipc.IntentPath), 0755); err != nil {
		return err
	}
	return persistIntent(owner, active, protectedIntentFiles{})
}

type protectedIntentFiles struct{}

func (protectedIntentFiles) Read(path string) ([]byte, error) {
	f, err := secureOpen(path, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1025))
}
func (protectedIntentFiles) Write(path string, raw []byte) error {
	return durableWrite(path, raw, 0644)
}
func (protectedIntentFiles) Remove(path string) error        { return os.Remove(path) }
func (protectedIntentFiles) SyncDirectory(path string) error { return syncDirectory(path) }
func (b *darwinBackend) Close() error {
	if b.lease == nil {
		return nil
	}
	err := b.lease.Close()
	b.lease = nil
	return err
}
