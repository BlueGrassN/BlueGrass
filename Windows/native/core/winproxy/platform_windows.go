//go:build windows

package winproxy

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	Size uint32
	Data *byte
}
type cryptCall func(in *dataBlob, flags uint32, out *dataBlob) error
type moveCall func(from, to *uint16, flags uint32) error

func cryptData(data []byte, call cryptCall, free func(*byte)) ([]byte, error) {
	if len(data) == 0 || len(data) > maxRecordBytes {
		return nil, ErrInvalidRecord
	}
	in := dataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out dataBlob
	err := call(&in, 1, &out) // CRYPTPROTECT_UI_FORBIDDEN, never LOCAL_MACHINE (4)
	runtime.KeepAlive(data)
	if out.Data != nil {
		defer free(out.Data)
	}
	if err != nil {
		return nil, err
	}
	if out.Size == 0 || out.Size > maxRecordBytes || out.Data == nil {
		return nil, ErrInvalidRecord
	}
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func moveFile(from, to string, call moveCall) error {
	f, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// Same-directory temp, MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH.
	return call(f, t, 1|8)
}

// New constructs a current-user manager without querying or changing proxies.
// statePath must be a stable, local path in a directory secured for that user.
func New(statePath string) (*Manager, error) {
	if strings.TrimSpace(statePath) == "" || strings.ContainsRune(statePath, 0) {
		return nil, errors.New("winproxy: invalid state path")
	}
	path, err := filepath.Abs(statePath)
	if err != nil {
		return nil, err
	}
	// kernel32 is in Go's system-DLL allowlist. Other DLLs are loaded via an
	// absolute system directory, never via the current working directory.
	kernel := syscall.NewLazyDLL("kernel32.dll")
	getDir := kernel.NewProc("GetSystemDirectoryW")
	if err := getDir.Find(); err != nil {
		return nil, err
	}
	buf := make([]uint16, 32768)
	n, _, callErr := getDir.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return nil, nativeError("GetSystemDirectoryW", callErr)
	}
	if n >= uintptr(len(buf)) {
		return nil, errors.New("winproxy: system directory too long")
	}
	dir := syscall.UTF16ToString(buf[:n])
	inet := syscall.NewLazyDLL(filepath.Join(dir, "wininet.dll"))
	crypt := syscall.NewLazyDLL(filepath.Join(dir, "crypt32.dll"))
	query := inet.NewProc("InternetQueryOptionW")
	set := inet.NewProc("InternetSetOptionW")
	globalFree := kernel.NewProc("GlobalFree")
	localFree := kernel.NewProc("LocalFree")
	move := kernel.NewProc("MoveFileExW")
	protect := crypt.NewProc("CryptProtectData")
	unprotect := crypt.NewProc("CryptUnprotectData")
	// Resolve now so unavailable APIs return a construction error, not a panic
	// after a transaction has been persisted. No proxy or DPAPI call is made.
	for _, p := range []*syscall.LazyProc{query, set, globalFree, localFree, move, protect, unprotect} {
		if err := p.Find(); err != nil {
			return nil, err
		}
	}
	backend := &winInetBackend{
		query: func(list *connectionOptions) error {
			size := list.Size
			r, _, e := query.Call(0, 75, uintptr(unsafe.Pointer(list)), uintptr(unsafe.Pointer(&size)))
			if r == 0 {
				return nativeError("InternetQueryOptionW", e)
			}
			return nil
		},
		set: func(list *connectionOptions) error {
			r, _, e := set.Call(0, 75, uintptr(unsafe.Pointer(list)), uintptr(list.Size))
			if r == 0 {
				return nativeError("InternetSetOptionW", e)
			}
			return nil
		},
		notify: func(option uint32) error {
			r, _, e := set.Call(0, uintptr(option), 0, 0)
			if r == 0 {
				return nativeError("InternetSetOptionW notification", e)
			}
			return nil
		},
		free: func(p *uint16) { globalFree.Call(uintptr(unsafe.Pointer(p))) },
	}
	cryptFunc := func(proc *syscall.LazyProc) func([]byte) ([]byte, error) {
		return func(data []byte) ([]byte, error) {
			return cryptData(data, func(in *dataBlob, flags uint32, out *dataBlob) error {
				r, _, e := proc.Call(uintptr(unsafe.Pointer(in)), 0, 0, 0, 0, uintptr(flags), uintptr(unsafe.Pointer(out)))
				if r == 0 {
					return nativeError(proc.Name, e)
				}
				return nil
			}, func(p *byte) { localFree.Call(uintptr(unsafe.Pointer(p))) })
		}
	}
	store := &fileStore{path: path, protect: cryptFunc(protect), unprotect: cryptFunc(unprotect), replace: func(from, to string) error {
		return moveFile(from, to, func(f, t *uint16, flags uint32) error {
			r, _, e := move.Call(uintptr(unsafe.Pointer(f)), uintptr(unsafe.Pointer(t)), uintptr(flags))
			if r == 0 {
				return nativeError("MoveFileExW", e)
			}
			return nil
		})
	}}
	return NewWithDependencies(backend, store)
}

func nativeError(op string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		err = syscall.EINVAL
	}
	return fmt.Errorf("%s: %w", op, err)
}
