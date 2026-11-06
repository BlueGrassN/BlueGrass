//go:build darwin

package mactun

import (
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Read-only DIOCGETSTARTERS ABI from Apple's bsd/net/pfvar.h. Both supported
// architectures use a 16-byte pfioc_tokens and packed 84-byte pfioc_token.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/pfvar.h
// A binary read avoids guessing pfctl's human-readable reference table format.
// Mutations still use only pfctl -E and -X with the journal's owned token.
func (b *darwinBackend) TokenExists(ctx context.Context, token string) (bool, error) {
	wanted, err := strconv.ParseUint(token, 10, 64)
	if err != nil || wanted == 0 {
		return false, ErrRecovery
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	fd, err := unix.Open("/dev/pf", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	const recordSize = 84
	data := make([]byte, recordSize*1024)
	request := struct {
		Size    int32
		Padding uint32
		Buffer  unsafe.Pointer
	}{Size: int32(len(data)), Buffer: unsafe.Pointer(&data[0])}
	if unsafe.Sizeof(request) != 16 {
		return false, ErrRecovery
	}
	const diocGetStarters = 0xc0104405 // _IOWR('D',5,struct pfioc_tokens)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), diocGetStarters, uintptr(unsafe.Pointer(&request)))
	runtime.KeepAlive(data)
	if errors.Is(errno, unix.ENOENT) {
		return false, nil
	}
	if errno != 0 {
		return false, errno
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if request.Size < 0 || int(request.Size) >= len(data) || request.Size%recordSize != 0 {
		return false, ErrRecovery
	}
	for i := 0; i < int(request.Size); i += recordSize {
		if binary.LittleEndian.Uint64(data[i:i+8]) == wanted {
			return true, nil
		}
	}
	return false, nil
}
