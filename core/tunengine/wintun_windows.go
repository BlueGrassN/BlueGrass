//go:build windows

package tunengine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// Official signed Wintun 0.14.1 amd64; keep in sync with toolchain.json.
const wintunSHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"

var wintunLoad struct {
	sync.Once
	err error
}

func loadWintun() error {
	wintunLoad.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			wintunLoad.err = errors.New("WINTUN_PATH_UNAVAILABLE")
			return
		}
		path := filepath.Join(filepath.Dir(executable), "wintun.dll")
		file, err := verifiedWintunFile(path)
		if err != nil {
			wintunLoad.err = err
			return
		}
		defer file.Close()
		// Preload only the validated EXE-adjacent file. The upstream Wintun
		// package reuses this loaded module; CWD and PATH never participate.
		_, err = windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
		if err != nil {
			wintunLoad.err = errors.New("WINTUN_LOAD_FAILED")
		}
	})
	return wintunLoad.err
}

func verifiedWintunFile(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("WINTUN_PATH_INVALID")
	}
	for part := absolute; ; part = filepath.Dir(part) {
		name, err := windows.UTF16PtrFromString(part)
		if err != nil {
			return nil, errors.New("WINTUN_PATH_INVALID")
		}
		attributes, err := windows.GetFileAttributes(name)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, errors.New("WINTUN_PATH_INVALID")
		}
		if next := filepath.Dir(part); next == part {
			break
		}
	}
	name, _ := windows.UTF16PtrFromString(absolute)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("WINTUN_OPEN_FAILED")
	}
	file := os.NewFile(uintptr(handle), absolute)
	hash := sha256.New()
	_, err = io.Copy(hash, io.LimitReader(file, 2<<20))
	if err != nil || hex.EncodeToString(hash.Sum(nil)) != wintunSHA256 {
		_ = file.Close()
		return nil, errors.New("WINTUN_HASH_MISMATCH")
	}
	return file, nil
}
