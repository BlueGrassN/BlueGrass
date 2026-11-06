//go:build windows

package nativehost

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"bluegrass/native/core/winproxy"
	"golang.org/x/sys/windows"
)

// lockCurrentUserProxy shares the recovery journal and lock between normal host
// startup and repair. OS file locks also disappear on forced termination.
func lockCurrentUserProxy() (*winproxy.Manager, func(), error) {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil || dir == "" || !filepath.IsAbs(dir) {
		return nil, nil, errors.New("USER_DIRECTORY_UNAVAILABLE")
	}
	dir = filepath.Join(dir, "BlueGrass", "network")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "proxy.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = lock.Close()
		return nil, nil, errors.New("PROXY_ALREADY_OWNED")
	}
	release := func() {
		_ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &overlapped)
		_ = lock.Close()
	}
	manager, err := winproxy.New(filepath.Join(dir, "proxy-state.dpapi"))
	if err != nil {
		release()
		return nil, nil, err
	}
	return manager, release, nil
}

// RepairCurrentUserProxy restores only this user's journaled proxy settings.
// preserved is true when external settings were left intact and obsolete
// ownership was relinquished. It never discovers a service or starts a listener.
func RepairCurrentUserProxy() (preserved bool, err error) {
	manager, release, err := lockCurrentUserProxy()
	if err != nil {
		return false, err
	}
	defer release()
	return repairProxy(manager)
}

// RunSystemProxy holds the per-user lock through recovery and EOF cleanup.
func RunSystemProxy(input io.Reader, output io.Writer) error {
	manager, release, err := lockCurrentUserProxy()
	if err != nil {
		return err
	}
	defer release()
	recoveryErr := manager.Recover()
	if recoveryErr != nil && !errors.Is(recoveryErr, winproxy.ErrOwnershipLost) {
		return recoveryErr
	}
	if handled, err := recoverTunnelIfPresent(input, output); handled || err != nil {
		return err
	}
	h, err := New(localProxyPort)
	if err != nil {
		return err
	}
	h.proxy = manager
	h.proxyRecoveryRequired = recoveryErr != nil
	h.proxyEngine = &proxyEngine{Engine: h.engine, manager: manager, address: h.engine.Address(), bypass: proxyBypass(true)}
	return h.run(input, output)
}
