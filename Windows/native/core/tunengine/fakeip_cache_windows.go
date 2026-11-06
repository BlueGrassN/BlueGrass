//go:build windows

package tunengine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	bolt "github.com/sagernet/bbolt"
	"golang.org/x/sys/windows"
)

const cacheSDDL = "O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)"
const cacheFileAllAccess = 0x001F01FF // FILE_ALL_ACCESS from Windows winnt.h

// The service/installer owns directory creation and ACL provisioning. This
// function never repairs permissions, follows reparse points or elevates.
// Ancestors are pinned without FILE_SHARE_DELETE; the immediate parent and DB
// must already be SYSTEM/Administrators-owned with a protected allow-only DACL.
func openProtectedCache(path string) (*cacheDatabase, error) {
	if !validCachePath(path) {
		return nil, errFakeIPCache
	}
	var pinned []windows.Handle
	release := func() error {
		var err error
		for i := len(pinned) - 1; i >= 0; i-- {
			err = errors.Join(err, windows.CloseHandle(pinned[i]))
		}
		pinned = nil
		return err
	}
	parent := filepath.Dir(path)
	var ancestors []string
	for dir := parent; ; dir = filepath.Dir(dir) {
		ancestors = append(ancestors, dir)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		name, err := windows.UTF16PtrFromString(ancestors[i])
		if err != nil {
			_ = release()
			return nil, errFakeIPCache
		}
		h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			_ = release()
			return nil, errFakeIPCache
		}
		pinned = append(pinned, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			_ = release()
			return nil, errFakeIPCache
		}
		if i == 0 && !protectedCacheHandle(h) {
			_ = release()
			return nil, errFakeIPCache
		}
	}
	sd, err := windows.SecurityDescriptorFromString(cacheSDDL)
	if err != nil {
		_ = release()
		return nil, errFakeIPCache
	}
	open := func(name string, _ int, _ os.FileMode) (*os.File, error) {
		if name != path {
			return nil, errFakeIPCache
		}
		ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, errFakeIPCache
		}
		sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
		h, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, 0, &sa,
			windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		runtime.KeepAlive(sd)
		if err != nil {
			return nil, errFakeIPCache
		}
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil ||
			info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 ||
			(uint64(info.FileSizeHigh)<<32|uint64(info.FileSizeLow)) > fakeIPMaxBytes || !protectedCacheHandle(h) {
			_ = windows.CloseHandle(h)
			return nil, errFakeIPCache
		}
		return os.NewFile(uintptr(h), path), nil
	}
	db, err := bolt.Open(path, 0600, cacheDBOptions(open))
	if err != nil {
		_ = release()
		return nil, errFakeIPCache
	}
	return &cacheDatabase{DB: db, release: release}, nil
}

func protectedCacheHandle(h windows.Handle) bool {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	return err == nil && protectedCacheDescriptor(sd)
}

func protectedCacheDescriptor(sd *windows.SECURITY_DESCRIPTOR) bool {
	if sd == nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || (!owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		return false
	}
	var system, admins bool
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask != cacheFileAllAccess {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.IsWellKnown(windows.WinLocalSystemSid) {
			system = true
		} else if sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			admins = true
		} else {
			return false
		}
	}
	return system && admins
}
