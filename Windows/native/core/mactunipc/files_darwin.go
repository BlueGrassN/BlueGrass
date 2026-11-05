//go:build darwin

package mactunipc

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Use the checked Library parent directly, with no /var symlink or daemon-
// writable /private/var/run ancestor. Socket/lease files may survive reboot.
const canonicalRuntime = RuntimeDirectory

func checkRootDirectory(path string) error {
	fd, err := openRootDirectory(path)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func openRootDirectory(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, ErrHelperInvalid
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	checked := func(fd int) bool {
		var s unix.Stat_t
		return unix.Fstat(fd, &s) == nil && s.Mode&unix.S_IFMT == unix.S_IFDIR && s.Uid == 0 && s.Mode&0022 == 0 && fdNoACL(fd)
	}
	if !checked(fd) {
		_ = unix.Close(fd)
		return -1, ErrHelperInvalid
	}
	if path == "/" {
		return fd, nil
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
		if !checked(fd) {
			_ = unix.Close(fd)
			return -1, ErrHelperInvalid
		}
	}
	return fd, nil
}

func rootFile(path string, flags int) (*os.File, error) {
	parent, err := openRootDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, filepath.Base(path), flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var s unix.Stat_t
	if unix.Fstat(fd, &s) != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || validateRootMetadata(s.Uid, uint32(s.Mode), uint64(s.Nlink), "file") != nil || !fdNoACL(fd) {
		_ = f.Close()
		return nil, ErrHelperInvalid
	}
	return f, nil
}

// ReadIntent returns nil only for a genuinely absent protected marker. Unsafe
// parents, files, permissions, hard links and corrupt data are errors, never an
// indication that it is safe to start a different network mode.
func ReadIntent() (*Intent, error) {
	return readIntent(func(path string) (io.ReadCloser, error) {
		return rootFile(path, unix.O_RDONLY)
	})
}

func fdNoACL(fd int) bool {
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || fs.Flags&unix.MNT_UNKNOWNPERMISSIONS != 0 {
		return false
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var b [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&b)
	return errno == 0 && noACLAttributes(b[:])
}

// Unix sockets cannot be opened as ordinary files for fgetattrlist. Their
// parent has already been walked through checked descriptors; query the node
// without following links and reject replacement during the metadata read.
func socketNoACL(path string) bool {
	var before, after unix.Stat_t
	if unix.Lstat(path, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFSOCK || before.Uid != 0 {
		return false
	}
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return false
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var b [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_GETATTRLIST, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), unix.FSOPT_NOFOLLOW, 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&b)
	return errno == 0 && noACLAttributes(b[:]) && unix.Lstat(path, &after) == nil && before.Dev == after.Dev && before.Ino == after.Ino
}

func hashFile(f *os.File) (string, error) {
	h := sha256.New()
	// Bundled executable must be finite and regular; refuse an unexpected giant.
	n, err := io.Copy(h, io.LimitReader(f, 512*1024*1024+1))
	if err != nil || n > 512*1024*1024 {
		return "", ErrHelperInvalid
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func bundledWorker() (string, error) {
	if !validHash(ExpectedWorkerSHA256) {
		return "", ErrHelperInvalid
	}
	exe, err := os.Executable()
	if err != nil {
		return "", ErrHelperInvalid
	}
	source := filepath.Join(filepath.Dir(exe), "BlueGrass.Tunnel")
	fd, err := unix.Open(source, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", ErrHelperInvalid
	}
	f := os.NewFile(uintptr(fd), source)
	defer f.Close()
	var s unix.Stat_t
	if unix.Fstat(fd, &s) != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Mode&0022 != 0 || s.Mode&0111 == 0 || s.Nlink != 1 || (s.Uid != 0 && s.Uid != uint32(os.Getuid())) || !fdNoACL(fd) {
		return "", ErrHelperInvalid
	}
	hash, err := hashFile(f)
	if err != nil || hash != ExpectedWorkerSHA256 {
		return "", ErrHelperInvalid
	}
	return source, nil
}

func validateInstalledWorker() error {
	exe, err := os.Executable()
	if err != nil {
		return ErrHelperInvalid
	}
	hash := strings.TrimPrefix(filepath.Base(exe), "BlueGrass.Tunnel-")
	if !validHash(hash) || exe != helperPath(hash) {
		return ErrHelperInvalid
	}
	f, err := rootFile(exe, unix.O_RDONLY)
	if err != nil {
		return ErrHelperInvalid
	}
	defer f.Close()
	actual, err := hashFile(f)
	if err != nil || actual != hash {
		return ErrHelperInvalid
	}
	return nil
}
