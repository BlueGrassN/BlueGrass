//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/update"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const machineSDDL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

type diskStore struct {
	dir             string
	machine         bool
	directory, lock windows.Handle
}

func localAbsolute(path string) bool {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 || strings.Contains(path[2:], ":") || filepath.Clean(path) != path {
		return false
	}
	for _, part := range strings.Split(path[3:], `\`) {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}
func openPinned(path string, directory bool, access uint32) (windows.Handle, error) {
	if !localAbsolute(path) {
		return 0, errState
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, errState
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	share := uint32(windows.FILE_SHARE_READ)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
		share |= windows.FILE_SHARE_WRITE
	}
	h, err := windows.CreateFile(p, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || !directory && info.NumberOfLinks != 1 {
		windows.CloseHandle(h)
		return 0, errState
	}
	return h, nil
}

// The installed machine tree may grant outsiders read/execute only. The user
// cache additionally permits its owning SID. Null/unreadable ACLs fail closed.
func validateACL(h windows.Handle, machine bool) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errTrust
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	privileged := func(sid *windows.SID) bool {
		return sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) || !machine && sid.Equals(u.User.Sid)
	}
	if !privileged(owner) {
		return errTrust
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return errTrust
	}
	const readOnly = windows.GENERIC_READ | windows.GENERIC_EXECUTE | windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errTrust
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return errTrust
		}
		if !privileged(sid) && (!machine || uint32(ace.Mask)&^uint32(readOnly) != 0) {
			return errTrust
		}
	}
	return nil
}
func createProtectedDirectory(path string, machine bool) error {
	sddl := machineSDDL
	if !machine {
		u, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		sid := u.User.Sid.String()
		sddl = "O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(p, &sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	h, err := openPinned(path, true, windows.READ_CONTROL)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return validateACL(h, machine)
}
func openDiskStore(dir string, machine bool) (*diskStore, error) {
	if !localAbsolute(dir) {
		return nil, errState
	}
	if err := createProtectedDirectory(dir, machine); err != nil {
		return nil, errState
	}
	h, err := openPinned(dir, true, windows.READ_CONTROL)
	if err != nil {
		return nil, errState
	}
	s := &diskStore{dir: dir, machine: machine, directory: h}
	p, _ := windows.UTF16PtrFromString(filepath.Join(dir, "session.lock"))
	s.lock, err = windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		s.Close()
		return nil, errBusy
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(s.lock, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.NumberOfLinks != 1 {
		s.Close()
		return nil, errState
	}
	return s, nil
}
func (s *diskStore) Close() {
	if s.lock != 0 && s.lock != windows.InvalidHandle {
		windows.CloseHandle(s.lock)
		s.lock = 0
	}
	if s.directory != 0 {
		windows.CloseHandle(s.directory)
		s.directory = 0
	}
}
func (s *diskStore) Dir() string { return s.dir }
func (s *diskStore) stateName() string {
	if s.machine {
		return "floor.json"
	}
	return "state.dpapi"
}
func readBounded(path string, limit int64) ([]byte, error) {
	h, err := openPinned(path, false, windows.GENERIC_READ)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errState
	}
	return b, nil
}
func (s *diskStore) Load() (*state, error) {
	b, err := readBounded(filepath.Join(s.dir, s.stateName()), 512*1024)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil, nil
	}
	if err != nil {
		return nil, errState
	}
	if !s.machine {
		b, err = cryptState(b, false)
		if err != nil {
			return nil, errState
		}
	}
	var v state
	if strictObject(b, &v, "schema", "installation_id", "floor", "manifest") != nil {
		return nil, errState
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	var floorFields map[string]json.RawMessage
	if len(fields) != 4 || strictObject(fields["floor"], &floorFields, "sequence", "manifest_sha256", "version", "build") != nil || len(floorFields) != 4 {
		return nil, errState
	}
	return &v, nil
}
func (s *diskStore) Save(v state) error {
	b, err := json.Marshal(v)
	if err != nil || len(b) > update.MaxManifestBytes {
		return errState
	}
	if !s.machine {
		b, err = cryptState(b, true)
		if err != nil {
			return errState
		}
	}
	return atomicWrite(s.dir, s.stateName(), b, s.machine)
}
func (s *diskStore) Handoff(id string, v state) error {
	if !hexID(id, 64) {
		return errCandidate
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) > update.MaxManifestBytes {
		return errState
	}
	return atomicWrite(s.dir, "launch-"+id+".json", b, s.machine)
}
func createExclusive(path string, machine bool) (*os.File, error) {
	var sa *windows.SecurityAttributes
	if machine {
		sd, err := windows.SecurityDescriptorFromString(machineSDDL)
		if err != nil {
			return nil, err
		}
		sa = &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
func atomicWrite(dir, name string, b []byte, machine bool) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	path := filepath.Join(dir, ".state-"+hex.EncodeToString(id[:])+".tmp")
	f, err := createExclusive(path, machine)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errState
	}
	dest := filepath.Join(dir, name)
	if h, err := openPinned(dest, false, windows.GENERIC_READ); err == nil {
		windows.CloseHandle(h)
	} else if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return errState
	}
	from, _ := windows.UTF16PtrFromString(path)
	to, _ := windows.UTF16PtrFromString(dest)
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func cryptState(b []byte, protect bool) ([]byte, error) {
	if len(b) == 0 || len(b) > 512*1024 {
		return nil, errState
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	entropyBytes := []byte("BlueGrass update state v1")
	entropy := windows.DataBlob{Size: uint32(len(entropyBytes)), Data: &entropyBytes[0]}
	var err error
	if protect {
		err = windows.CryptProtectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Size > 512*1024 {
		return nil, errState
	}
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
