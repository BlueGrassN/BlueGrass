//go:build darwin

package nativehost

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"bluegrass/native/core/macproxy"
	"bluegrass/native/core/winproxy"
	"golang.org/x/sys/unix"
)

func privateDarwinDirectory(dir string) error {
	s, err := os.Lstat(dir)
	if err != nil {
		return errors.New("USER_DIRECTORY_UNAVAILABLE")
	}
	stat, ok := s.Sys().(*syscall.Stat_t)
	if !ok || !s.IsDir() || s.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("USER_DIRECTORY_UNAVAILABLE")
	}
	return nil
}
func acquireDarwinProxyLock(dir string) (func(), error) {
	if err := privateDarwinDirectory(dir); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(dir, "proxy.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, winproxy.ErrRecoveryRequired
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Getuid()) || stat.Mode&0077 != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, winproxy.ErrRecoveryRequired
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		_ = unix.Close(fd)
		return nil, errors.New("PROXY_ALREADY_OWNED")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }) }, nil
}
func lockDarwinProxy() (*macproxy.Manager, func(), error) {
	if os.Getuid() == 0 {
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, nil, errors.New("USER_DIRECTORY_UNAVAILABLE")
	}
	base := filepath.Join(home, "Library", "Application Support", "BlueGrass")
	if err = os.MkdirAll(base, 0700); err != nil {
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	if err = privateDarwinDirectory(base); err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(base, "network")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	release, err := acquireDarwinProxyLock(dir)
	if err != nil {
		return nil, nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		release()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	helper := filepath.Join(filepath.Dir(executable), "BlueGrass.ProxySettings")
	s, err := os.Lstat(helper)
	if err != nil || !s.Mode().IsRegular() || s.Mode().Perm()&0022 != 0 || s.Mode().Perm()&0111 == 0 {
		release()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	stat, ok := s.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Getuid())) {
		release()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	m, closeHelper, err := macproxy.Open(helper, dir)
	if err != nil {
		release()
		return nil, nil, err
	}
	return m, func() { closeHelper(); release() }, nil
}

// RepairCurrentUserProxy shares the host lock and never starts a local listener.
func RepairCurrentUserProxy() (bool, error) {
	m, release, err := lockDarwinProxy()
	if err != nil {
		return false, err
	}
	defer release()
	return repairProxy(m)
}

// RunSystemProxy reuses the private JSON-lines host and the pinned local Xray
// engine. There is deliberately no tunnel or persistent-protection capability.
func RunSystemProxy(input io.Reader, output io.Writer) error {
	m, release, err := lockDarwinProxy()
	if err != nil {
		return err
	}
	defer release()
	recoveryErr := m.Recover()
	if recoveryErr != nil && !errors.Is(recoveryErr, winproxy.ErrOwnershipLost) {
		return recoveryErr
	}
	h, err := New(localProxyPort)
	if err != nil {
		return err
	}
	h.proxy = m
	h.proxyRecoveryRequired = recoveryErr != nil
	h.proxyEngine = &proxyEngine{Engine: h.engine, manager: m, address: h.engine.Address(), bypass: proxyBypass(true)}
	return h.run(input, output)
}
