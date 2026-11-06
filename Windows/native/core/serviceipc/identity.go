package serviceipc

import (
	"errors"
	"path/filepath"
	"strings"
)

var ErrPeer = errors.New("SERVICE_CALLER_REJECTED")

type Peer struct {
	Identity
	Image, Signer                        string
	ParentImage, ParentSigner, ParentSID string
	ParentSession                        uint32
}

func ValidatePeer(p Peer, installedDir, trustedSigner string, server bool) error {
	if trustedSigner == "" || p.Signer != trustedSigner || p.PID == 0 || p.SID == "" || !filepath.IsAbs(installedDir) {
		return ErrPeer
	}
	exact := func(path, name string) bool {
		return strings.EqualFold(filepath.Clean(path), filepath.Join(installedDir, name))
	}
	if server {
		if p.SID != "S-1-5-18" || p.Session != 0 || !exact(p.Image, "BlueGrass.Service.exe") {
			return ErrPeer
		}
		return nil
	}
	if p.Session == 0 || !exact(p.Image, "BlueGrass.Native.exe") || !exact(p.ParentImage, "BlueGrass.exe") || p.ParentSID != p.SID || p.ParentSession != p.Session || p.ParentSigner != trustedSigner {
		return ErrPeer
	}
	return nil
}
