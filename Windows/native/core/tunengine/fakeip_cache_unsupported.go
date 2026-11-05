//go:build !windows

package tunengine

func openProtectedCache(string) (*cacheDatabase, error) { return nil, errFakeIPCache }
