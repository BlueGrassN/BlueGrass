//go:build windows

package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

type installation struct {
	root, exe, publisher string
	handles              []windows.Handle
}

func (i *installation) Close() {
	for _, h := range i.handles {
		windows.CloseHandle(h)
	}
	i.handles = nil
}
func trustedInstallation() (*installation, error) {
	return trustedInstallationMode(false)
}

func trustedInstallationMode(offline bool) (*installation, error) {
	programs, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return nil, errTrust
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, errTrust
	}
	i := &installation{root: filepath.Join(programs, "BlueGrass")}
	i.exe = filepath.Join(i.root, "BlueGrass.Update.exe")
	if !strings.EqualFold(filepath.Clean(exe), i.exe) {
		return nil, errTrust
	}
	for _, path := range []string{i.root, i.exe, filepath.Join(i.root, "BlueGrass.exe")} {
		h, err := openPinned(path, path == i.root, windows.GENERIC_READ|windows.READ_CONTROL)
		if err != nil {
			i.Close()
			return nil, errTrust
		}
		i.handles = append(i.handles, h)
		if validateACL(h, true) != nil {
			i.Close()
			return nil, errTrust
		}
	}
	trust := update.TrustedPublisher
	if offline {
		trust = update.TrustedPublisherOffline
	}
	i.publisher, err = trust(i.exe)
	if err != nil {
		i.Close()
		return nil, errTrust
	}
	guiPublisher, err := trust(filepath.Join(i.root, "BlueGrass.exe"))
	if err != nil || guiPublisher != i.publisher {
		i.Close()
		return nil, errTrust
	}
	return i, nil
}
func runtimeIdentity(i *installation) runtimeInfo {
	v := windows.RtlGetVersion()
	return runtimeInfo{Publisher: i.publisher, OSVersion: fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber), Arch: runtime.GOARCH}
}
func updateDirectory(machine bool, cfg releaseConfig) (string, error) {
	folder := windows.FOLDERID_LocalAppData
	if machine {
		folder = windows.FOLDERID_ProgramData
	}
	base, err := windows.KnownFolderPath(folder, 0)
	if err != nil {
		return "", errState
	}
	// Never recurse into a caller-supplied directory. Only these fixed children
	// are created; an existing shared BlueGrass parent is not re-ACL'd.
	base = filepath.Join(base, "BlueGrass")
	if _, err := os.Lstat(base); os.IsNotExist(err) {
		if err := createProtectedDirectory(base, machine); err != nil {
			return "", errState
		}
	}
	h, err := openPinned(base, true, windows.READ_CONTROL)
	if err != nil {
		return "", errState
	}
	defer windows.CloseHandle(h)
	if machine && validateACL(h, true) != nil {
		return "", errState
	}
	base = filepath.Join(base, "updates")
	if createProtectedDirectory(base, machine) != nil {
		return "", errState
	}
	return filepath.Join(base, cfg.Channel+"-"+runtime.GOARCH), nil
}
func openUserEngineMode(cfg releaseConfig, offline bool) (*engine, func(), error) {
	i, err := trustedInstallationMode(offline)
	if err != nil {
		return nil, nil, err
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		i.Close()
		return nil, nil, errTrust
	}
	dir, err := updateDirectory(false, cfg)
	if err != nil {
		i.Close()
		return nil, nil, err
	}
	s, err := openDiskStore(dir, false)
	if err != nil {
		i.Close()
		return nil, nil, err
	}
	close := func() { s.Close(); i.Close() }
	info := runtimeIdentity(i)
	if !offline {
		costs := newNLMReader()
		close = func() { costs.Close(); s.Close(); i.Close() }
		info.ResolveHost = func(ctx context.Context, host string) ([]netip.Addr, error) { return resolveViaService(ctx, i, host) }
		info.Network = newRoutePolicy(cfg.AllowedHosts, info.ResolveHost, selectPhysicalBinding, costs.Read)
	}
	e, err := newEngine(cfg, info, s)
	if err != nil {
		close()
		return nil, nil, err
	}
	if !offline {
		e.elevate = func(ctx context.Context, id, source string) error { return requestElevation(ctx, i.exe, id, source) }
	}
	return e, close, nil
}
