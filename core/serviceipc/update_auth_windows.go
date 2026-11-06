//go:build windows

package serviceipc

import (
	"net"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// AuthenticateUpdateHelper reads the real pipe peer and its parent using the
// same OS boundary as Authenticate. Callers MUST dispatch only the read-only
// fixed-host resolver protocol after this succeeds, never a Native Host.
func AuthenticateUpdateHelper(conn net.Conn, installedDir, trustedSigner string, signer Signer) (Identity, error) {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok || signer == nil {
		return Identity{}, ErrPeer
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid) != nil {
		return Identity{}, ErrPeer
	}
	peer, parentPID, err := processIdentity(pid, signer)
	if err != nil {
		return Identity{}, err
	}
	parent, _, err := processIdentity(parentPID, signer)
	if err != nil {
		return Identity{}, err
	}
	peer.ParentImage, peer.ParentSigner, peer.ParentSID, peer.ParentSession = parent.Image, parent.Signer, parent.SID, parent.Session
	root, err := filepath.EvalSymlinks(installedDir)
	if err != nil || ValidateUpdatePeer(peer, root, trustedSigner) != nil {
		return Identity{}, ErrPeer
	}
	return peer.Identity, nil
}
