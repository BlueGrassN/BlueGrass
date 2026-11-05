package serviceipc

import (
	"path/filepath"
	"strings"
)

// ValidateUpdatePeer is a separate read-only resolver identity. It does not
// grant the Native broker's session/connection permissions.
func ValidateUpdatePeer(p Peer, installedDir, trustedSigner string) error {
	if trustedSigner == "" || p.Signer != trustedSigner || p.PID == 0 || p.SID == "" || p.Session == 0 || !filepath.IsAbs(installedDir) {
		return ErrPeer
	}
	exact := func(path, name string) bool {
		return strings.EqualFold(filepath.Clean(path), filepath.Join(installedDir, name))
	}
	if !exact(p.Image, "BlueGrass.Update.exe") || !exact(p.ParentImage, "BlueGrass.exe") || p.ParentSID != p.SID || p.ParentSession != p.Session || p.ParentSigner != trustedSigner {
		return ErrPeer
	}
	return nil
}
