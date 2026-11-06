//go:build !windows && !darwin

package tunengine

func openProtectedCache(string) (*cacheDatabase, error) { return nil, errFakeIPCache }
