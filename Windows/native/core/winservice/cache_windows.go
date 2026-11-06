//go:build windows

package winservice

import (
	"os"
	"path/filepath"

	"bluegrass/native/core/tunengine"
	"golang.org/x/sys/windows"
)

func ownedCachePath() (string, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "BlueGrass", "fakeip.db"), nil
}

// ClearOwnedCache is an explicit logical-release step, after the service and
// owned adapter are stopped and before WFP protection is removed. It accepts no
// caller path, creates no directory and never recursively removes anything.
// An existing cache still goes through tunengine's strict ACL/path/lock checks.
func ClearOwnedCache() error {
	path, err := ownedCachePath()
	if err != nil {
		return err
	}
	return clearCacheIfPresent(path, os.Lstat, tunengine.ClearCache)
}

func clearCacheIfPresent(path string, lstat func(string) (os.FileInfo, error), clear func(string) error) error {
	_, err := lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return clear(path)
}

// Both installed-service and absent-service uninstall paths share this order.
// Each function owns its exact objects; any error prevents later release steps.
func repairOwnedNetwork(repairAdapter, clearCache, repairProtection func() error) error {
	if err := repairAdapter(); err != nil {
		return err
	}
	if err := clearCache(); err != nil {
		return err
	}
	return repairProtection()
}
