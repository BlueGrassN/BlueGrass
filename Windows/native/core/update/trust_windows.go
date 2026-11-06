//go:build windows

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

func openArtifact(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute artifact path required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact must be a regular file")
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// No WRITE/DELETE sharing: hash and signer verification see the same bytes.
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

type trustGUID struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}
type trustFileInfo struct {
	Size         uint32
	Path         *uint16
	File         syscall.Handle
	KnownSubject *trustGUID
}
type trustData struct {
	Size                                    uint32
	PolicyCallback, SIPClient               uintptr
	UIChoice, RevocationChecks, UnionChoice uint32
	FileInfo                                *trustFileInfo
	StateAction                             uint32
	StateData                               uintptr
	URLReference                            *uint16
	ProviderFlags, UIContext                uint32
	SignatureSettings                       uintptr
}
type providerCert struct {
	Size        uint32
	Certificate uintptr
}
type certContext struct {
	Encoding    uint32
	Encoded     uintptr
	Length      uint32
	Info, Store uintptr
}

func systemWintrust() (*syscall.DLL, error) {
	// kernel32 is a KnownDLL; all other DLLs load from the native system directory.
	kernel, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return nil, err
	}
	defer kernel.Release()
	getdir, err := kernel.FindProc("GetSystemDirectoryW")
	if err != nil {
		return nil, err
	}
	var dir [32768]uint16
	n, _, _ := getdir.Call(uintptr(unsafe.Pointer(&dir[0])), uintptr(len(dir)))
	if n == 0 || n >= uintptr(len(dir)) {
		return nil, errors.New("cannot resolve system DLL directory")
	}
	return syscall.LoadDLL(filepath.Join(syscall.UTF16ToString(dir[:n]), "wintrust.dll"))
}

func trustedFilePublisher(f *os.File) (string, error) {
	return trustedFilePublisherMode(f, false)
}

func wintrustProviderFlags(offline bool) uint32 {
	// Windows SDK WinTrust.h: check the chain except the root, and disable MD2/4.
	flags := uint32(0x80 | 0x2000)
	if offline {
		// WTD_CACHE_ONLY_URL_RETRIEVAL affects both CRL and AIA retrieval.
		flags |= 0x1000
	}
	return flags
}

func trustedFilePublisherMode(f *os.File, offline bool) (string, error) {
	dll, err := systemWintrust()
	if err != nil {
		return "", err
	}
	defer dll.Release()
	verify, err := dll.FindProc("WinVerifyTrust")
	if err != nil {
		return "", err
	}
	provider, err := dll.FindProc("WTHelperProvDataFromStateData")
	if err != nil {
		return "", err
	}
	signer, err := dll.FindProc("WTHelperGetProvSignerFromChain")
	if err != nil {
		return "", err
	}
	certificate, err := dll.FindProc("WTHelperGetProvCertFromChain")
	if err != nil {
		return "", err
	}
	path, err := syscall.UTF16PtrFromString(f.Name())
	if err != nil {
		return "", err
	}
	file := trustFileInfo{Path: path, File: syscall.Handle(f.Fd())}
	file.Size = uint32(unsafe.Sizeof(file))
	action := trustGUID{0x00aac56b, 0xcd44, 0x11d0, [8]byte{0x8c, 0xc2, 0x00, 0xc0, 0x4f, 0xc2, 0x95, 0xee}}
	data := trustData{UIChoice: 2, RevocationChecks: 1, UnionChoice: 1, FileInfo: &file, StateAction: 1, ProviderFlags: wintrustProviderFlags(offline)}
	data.Size = uint32(unsafe.Sizeof(data))
	defer func() {
		data.StateAction = 2
		verify.Call(^uintptr(0), uintptr(unsafe.Pointer(&action)), uintptr(unsafe.Pointer(&data)))
		runtime.KeepAlive(f)
		runtime.KeepAlive(path)
		runtime.KeepAlive(file)
	}()
	status, _, _ := verify.Call(^uintptr(0), uintptr(unsafe.Pointer(&action)), uintptr(unsafe.Pointer(&data)))
	if uint32(status) != 0 {
		return "", fmt.Errorf("Authenticode trust rejected (0x%08x)", uint32(status))
	}
	prov, _, _ := provider.Call(data.StateData)
	if prov == 0 {
		return "", errors.New("missing signature provider")
	}
	sgn, _, _ := signer.Call(prov, 0, 0, 0)
	if sgn == 0 {
		return "", errors.New("missing verified signer")
	}
	pc, _, _ := certificate.Call(sgn, 0)
	if pc == 0 {
		return "", errors.New("missing signer certificate")
	}
	var providerCertificate providerCert
	if err := readNativeMemory(pc, unsafe.Pointer(&providerCertificate), unsafe.Sizeof(providerCertificate)); err != nil {
		return "", err
	}
	var cert certContext
	if err := readNativeMemory(providerCertificate.Certificate, unsafe.Pointer(&cert), unsafe.Sizeof(cert)); err != nil {
		return "", err
	}
	if cert.Encoded == 0 || cert.Length == 0 || cert.Length > 1024*1024 {
		return "", errors.New("invalid signer certificate")
	}
	der := make([]byte, int(cert.Length))
	if err := readNativeMemory(cert.Encoded, unsafe.Pointer(&der[0]), uintptr(len(der))); err != nil {
		return "", err
	}
	digest := sha256.Sum256(der)
	return hex.EncodeToString(digest[:]), nil
}

// Windows owns these addresses until WTD_STATEACTION_CLOSE. Copy into bounded Go
// storage instead of constructing Go pointers from foreign integer addresses.
func readNativeMemory(address uintptr, destination unsafe.Pointer, size uintptr) error {
	kernel, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return err
	}
	defer kernel.Release()
	read, err := kernel.FindProc("ReadProcessMemory")
	if err != nil {
		return err
	}
	var copied uintptr
	ok, _, _ := read.Call(^uintptr(0), address, uintptr(destination), size, uintptr(unsafe.Pointer(&copied)))
	if ok == 0 || copied != size {
		return errors.New("cannot read verified signer state")
	}
	return nil
}
