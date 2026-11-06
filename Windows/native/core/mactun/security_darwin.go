//go:build darwin

package mactun

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func noExtendedACL(fd int) bool {
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || fs.Flags&unix.MNT_UNKNOWNPERMISSIONS != 0 {
		return false
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&buffer)
	return errno == 0 && noACLBuffer(buffer[:])
}
