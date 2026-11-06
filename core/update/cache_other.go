//go:build !windows

package update

import "os"

func lockCache(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return func() { _ = f.Close(); _ = os.Remove(path) }, nil
}
