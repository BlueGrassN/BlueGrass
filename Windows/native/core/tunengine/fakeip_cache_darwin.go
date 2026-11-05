//go:build darwin

package tunengine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	bolt "github.com/sagernet/bbolt"
	"golang.org/x/sys/unix"
)

// This function never creates/repairs directory ownership. The trusted root
// manager provisions an absolute, canonical 0700 directory. Every ancestor is
// opened relative to a pinned fd with O_NOFOLLOW and must be root-owned,
// non-writable by group/other and have no extended ACL. Fail closed on a file
// system that cannot report security attributes. No GUI-selected cache paths.
func openProtectedCache(path string) (*cacheDatabase, error) {
	if os.Geteuid() != 0 || !validCachePath(path) {
		return nil, errFakeIPCache
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errFakeIPCache
	}
	pinned := []int{fd}
	release := func() error {
		var err error
		for i := len(pinned) - 1; i >= 0; i-- {
			err = errors.Join(err, unix.Close(pinned[i]))
		}
		pinned = nil
		return err
	}
	fail := func() (*cacheDatabase, error) { _ = release(); return nil, errFakeIPCache }
	if !darwinCacheDirectory(fd, false) {
		return fail()
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/")
	for i, part := range parts {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fail()
		}
		pinned = append(pinned, next)
		fd = next
		if !darwinCacheDirectory(fd, i == len(parts)-1) {
			return fail()
		}
	}
	parentFD := fd
	open := func(name string, _ int, _ os.FileMode) (*os.File, error) {
		if name != path {
			return nil, errFakeIPCache
		}
		fd, err := unix.Openat(parentFD, filepath.Base(path), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
		if err != nil {
			return nil, errFakeIPCache
		}
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Mode&0777 != 0600 || st.Nlink != 1 || st.Size < 0 || st.Size > fakeIPMaxBytes || !darwinCacheNoACL(fd) {
			_ = unix.Close(fd)
			return nil, errFakeIPCache
		}
		return os.NewFile(uintptr(fd), path), nil
	}
	db, err := bolt.Open(path, 0600, cacheDBOptions(open))
	if err != nil {
		return fail()
	}
	return &cacheDatabase{DB: db, release: release}, nil
}

func darwinCacheDirectory(fd int, private bool) bool {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 || st.Mode&0022 != 0 || private && st.Mode&0077 != 0 {
		return false
	}
	return darwinCacheNoACL(fd)
}

func darwinCacheNoACL(fd int) bool {
	// fgetattrlist(2), ATTR_CMN_EXTENDED_SECURITY returns an attrreference_t.
	// Refuse filesystems that ignore on-disk owners/permissions.
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || fs.Flags&unix.MNT_UNKNOWNPERMISSIONS != 0 {
		return false
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&buffer)
	if errno != 0 {
		return false
	}
	return darwinNoACLBuffer(buffer[:])
}
