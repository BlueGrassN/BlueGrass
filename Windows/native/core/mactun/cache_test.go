package mactun

import (
	"context"
	"errors"
	"testing"
)

func TestCachePersistsOnPauseAndClearsOnlyAfterClosedRelease(t *testing.T) {
	tun, b, f, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	tun.cachePath = `C:\private\fakeip.db`
	clears := 0
	tun.clearCache = func(path string) error {
		if path != tun.cachePath || !f.closed {
			t.Fatal("cache clear without owned closed engine")
		}
		clears++
		return nil
	}
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if f.config.CachePath != tun.cachePath {
		t.Fatal("cache not passed to Xray")
	}
	if err = tun.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if err = tun.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if clears != 0 {
		t.Fatal("pause cleared logical mappings")
	}
	tun.clearCache = func(string) error { return errors.New("cache locked") }
	if err = tun.Release(); err == nil || b.j == nil || !b.enabled || !b.marker {
		t.Fatal("failed cache clear released protection")
	}
	tun.clearCache = func(string) error { clears++; return nil }
	if err = tun.Release(); err != nil {
		t.Fatal(err)
	}
	if clears != 1 || b.j != nil {
		t.Fatal("explicit release failed cache lifetime")
	}
}

func TestClosePreservesLogicalCacheAndAllocatorLifetime(t *testing.T) {
	tun, b, _, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	tun.cachePath = `C:\private\fakeip.db`
	called := false
	tun.clearCache = func(string) error { called = true; return nil }
	if err = tun.Prepare(context.Background(), profile()); err != nil {
		t.Fatal(err)
	}
	if err = tun.Close(); err != nil {
		t.Fatal(err)
	}
	if called || b.j == nil || !b.enabled {
		t.Fatal("process close discarded logical cache or protection")
	}
}
