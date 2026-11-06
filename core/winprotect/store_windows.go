//go:build windows && (amd64 || arm64)

package winprotect

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The directory and every record/lock are protected from inheritance. Owner BA
// prevents an unelevated user SID from gaining WRITE_DAC through ownership.
const stateSDDL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

type diskStore struct {
	path            string
	directory, lock windows.Handle
	sd              *windows.SECURITY_DESCRIPTOR
}

func openStore() (*diskStore, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "BlueGrass")
	sd, err := windows.SecurityDescriptorFromString(stateSDDL)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	if err = windows.CreateDirectory(p, &sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	s := &diskStore{path: filepath.Join(dir, "state.json"), sd: sd}
	s.directory, err = s.openTrusted(dir, windows.OPEN_EXISTING, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, true)
	if err != nil {
		return nil, err
	}
	s.lock, err = s.openTrusted(filepath.Join(dir, "winprotect.lock"), windows.OPEN_ALWAYS, 0, false)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("exclusive protection state lock: %w", err)
	}
	return s, nil
}
func (s *diskStore) Close() error {
	var err error
	if s.lock != 0 && s.lock != windows.InvalidHandle {
		err = windows.CloseHandle(s.lock)
		s.lock = 0
	}
	if s.directory != 0 && s.directory != windows.InvalidHandle {
		err = errors.Join(err, windows.CloseHandle(s.directory))
		s.directory = 0
	}
	return err
}
func (s *diskStore) openTrusted(path string, creation, share uint32, directory bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: s.sd}
	access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE | windows.READ_CONTROL | windows.WRITE_DAC)
	if directory {
		access = windows.READ_CONTROL | windows.WRITE_DAC
	}
	h, err := windows.CreateFile(p, access, share, &sa, creation, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, err
	}
	fail := func(e error) (windows.Handle, error) { _ = windows.CloseHandle(h); return 0, e }
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return fail(err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || (!directory && info.NumberOfLinks != 1) {
		return fail(errors.New("untrusted protection path: reparse point, hardlink or type mismatch"))
	}
	current, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fail(err)
	}
	owner, _, err := current.Owner()
	if err != nil {
		return fail(err)
	}
	if owner == nil || (!owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
		return fail(errors.New("protection path must be owned by SYSTEM or Administrators"))
	}
	dacl, _, err := s.sd.DACL()
	if err != nil {
		return fail(err)
	}
	if err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fail(err)
	}
	runtime.KeepAlive(s.sd)
	return h, nil
}
func (s *diskStore) Load() (*intent, error) {
	h, err := s.openTrusted(s.path, windows.OPEN_EXISTING, 0, false)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), s.path)
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxIntentBytes+1))
	if err != nil {
		return nil, err
	}
	return decodeIntent(b)
}
func (s *diskStore) Save(v intent) error {
	b, err := encodeIntent(v)
	if err != nil {
		return err
	}
	// Check an existing destination without following links before replacement.
	h, err := s.openTrusted(s.path, windows.OPEN_EXISTING, 0, false)
	if err == nil {
		if err = windows.CloseHandle(h); err != nil {
			return err
		}
	} else if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(s.path), ".state-"+hex.EncodeToString(id[:])+".tmp")
	h, err = s.openTrusted(tmp, windows.CREATE_NEW, 0, false)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(h), tmp)
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	from, _ := windows.UTF16PtrFromString(tmp)
	to, _ := windows.UTF16PtrFromString(s.path)
	if err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	return nil
}
