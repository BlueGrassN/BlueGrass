package main

import (
	"bluegrass/native/core/winproxy"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func cleanupRecords(directory string, removeSettings bool) error {
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("owned client directory is a link or invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	names := []string{"session_v1.dpapi", "session_v1.dpapi.tmp"}
	if removeSettings {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".dpapi") || strings.HasSuffix(entry.Name(), ".dpapi.tmp") {
				names = append(names, entry.Name())
			}
		}
	}
	for _, name := range names {
		if filepath.Base(name) != name {
			return errors.New("invalid owned record name")
		}
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("owned record must be a regular file")
		}
		if err := root.Remove(name); err != nil {
			return err
		}
	}
	return nil
}

type proxyRecovery interface {
	Recover() error
	Relinquish() error
	State() (bool, error)
}

func recoverProxy(proxy proxyRecovery) error {
	if err := proxy.Recover(); err != nil {
		if !errors.Is(err, winproxy.ErrOwnershipLost) {
			return err
		}
		// Another application now owns the setting. Relinquish the obsolete
		// journal without restoring over that application's chosen configuration.
		if err := proxy.Relinquish(); err != nil {
			return err
		}
	}
	applied, err := proxy.State()
	if err != nil {
		return err
	}
	if applied {
		return errors.New("proxy restoration not confirmed")
	}
	return nil
}
