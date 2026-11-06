//go:build windows

package serviceipc

import (
	"os"
	"path/filepath"
	"strings"

	"bluegrass/native/core/update"
	"golang.org/x/sys/windows"
)

// Installation refuses developer checkouts. Release IPC has no unsigned or
// arbitrary-path switch; an installed, trusted signer is part of the boundary.
func Installation() (root, publisher string, err error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", ErrPeer
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", "", ErrPeer
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", "", ErrPeer
	}
	root, err = filepath.EvalSymlinks(filepath.Join(programs, "BlueGrass"))
	if err != nil {
		return "", "", ErrPeer
	}
	if !strings.EqualFold(filepath.Dir(exe), root) {
		return "", "", ErrPeer
	}
	publisher, err = update.TrustedPublisher(exe)
	if err != nil {
		return "", "", ErrPeer
	}
	return root, publisher, nil
}
