package tunengine

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"bluegrass/native/core/engine"
	bolt "github.com/sagernet/bbolt"
)

// This opener exists only in tests. Production always verifies the protected
// Windows service directory/file, and has no insecure-development flag.
func fixtureCacheOpen(path string) (*cacheDatabase, error) {
	db, err := bolt.Open(path, 0600, cacheDBOptions(nil))
	if err != nil {
		return nil, err
	}
	return &cacheDatabase{DB: db, release: func() error { return nil }}, nil
}

func cachePrepared(t *testing.T, path string) preparedConfig {
	t.Helper()
	c := testConfig()
	c.CachePath = path
	n, _ := c.Network(context.Background())
	p := testProfile()
	p.Key = engine.ResourceKey{Environment: "test", DataSource: "v2", AccountKey: "account-a", Kind: "node", SourceID: "one"}
	prepared, err := prepareConfig(p, c, n, netip.MustParseAddr(p.Entry.Host))
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func startFixtureCache(t *testing.T, p preparedConfig, now func() time.Time) *persistentFakeIPStore {
	t.Helper()
	s := newPersistentFakeIPStore(context.Background(), p, fixtureCacheOpen, now)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFakeIPPersistenceRestoresDomainAndAllocationCursor(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	s := startFixtureCache(t, p, time.Now)
	a, err := s.Create("WWW.Example.COM.", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = startFixtureCache(t, p, time.Now)
	if got, ok := s.Lookup(a); !ok || got != "www.example.com" {
		t.Fatal("reverse mapping lost after reopen")
	}
	if got, err := s.Create("www.example.com", false); err != nil || got != a {
		t.Fatal("forward mapping lost after reopen")
	}
	b, err := s.Create("second.example.com", false)
	if err != nil || b == a {
		t.Fatal("allocation cursor lost after reopen")
	}
}

func TestFakeIPCrashDurabilityHelper(t *testing.T) {
	path := os.Getenv("BLUEGRASS_FAKEIP_CRASH_FIXTURE")
	if path == "" {
		t.Skip("subprocess-only disk fixture")
	}
	p := cachePrepared(t, path)
	s := startFixtureCache(t, p, time.Now)
	if _, err := s.Create("committed.example", false); err != nil {
		t.Fatal(err)
	}
	// Simulate process exit without Store.Close or any metadata flush.
	os.Exit(0)
}

func TestFakeIPMappingSurvivesProcessExitWithoutClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fakeip.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestFakeIPCrashDurabilityHelper$")
	cmd.Env = append(os.Environ(), "BLUEGRASS_FAKEIP_CRASH_FIXTURE="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture process failed: %v %s", err, output)
	}
	s := startFixtureCache(t, cachePrepared(t, path), time.Now)
	first := netip.MustParseAddr("198.18.0.2")
	if got, ok := s.Lookup(first); !ok || got != "committed.example" {
		t.Fatal("committed mapping lost on process exit")
	}
	if next, err := s.Create("next.example", false); err != nil || next == first {
		t.Fatal("crash reused committed address")
	}
}

func TestFakeIPNamespaceChangesInvalidateWithoutRebindingStaleAddresses(t *testing.T) {
	cases := map[string]func(*preparedConfig){
		"network":     func(p *preparedConfig) { p.network.Generation++ },
		"account":     func(p *preparedConfig) { p.profile.Key.AccountKey = "account-b" },
		"environment": func(p *preparedConfig) { p.profile.Key.Environment = "other" },
		"source":      func(p *preparedConfig) { p.profile.Key.DataSource = "v3" },
		"resource":    func(p *preparedConfig) { p.profile.Key.SourceID = "two" },
		"LAN policy":  func(p *preparedConfig) { p.allowLAN = !p.allowLAN },
		"DNS policy":  func(p *preparedConfig) { p.network.DNS = []LocalDNS{{Domains: []string{"office.example"}}} },
		"pool":        func(p *preparedConfig) { p.fake4 = netip.MustParsePrefix("198.19.0.0/16") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
			s := startFixtureCache(t, p, time.Now)
			old, _ := s.Create("old.example", false)
			_ = s.Close()
			change(&p)
			s = startFixtureCache(t, p, time.Now)
			if _, ok := s.Lookup(old); ok {
				t.Fatal("stale namespace mapping retained")
			}
			fresh, err := s.Create("new.example", false)
			if err != nil || fresh == old {
				t.Fatal("stale address rebound to different domain")
			}
			_ = s.Close()
			// Going back to the original pool/generation also cannot resurrect or
			// overwrite addresses that were already issued in this cache lifetime.
			original := cachePrepared(t, p.cachePath)
			s = startFixtureCache(t, original, time.Now)
			if _, ok := s.Lookup(old); ok {
				t.Fatal("old namespace resurrected")
			}
			latest, err := s.Create("third.example", false)
			if err != nil || latest == old {
				t.Fatal("old pool cursor was discarded")
			}
		})
	}
}

func TestFakeIPCacheBoundedAgeCapacityAndExplicitClear(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	s := startFixtureCache(t, p, clock)
	old, _ := s.Create("old.example", false)
	if clearCache(p.cachePath, fixtureCacheOpen) == nil {
		t.Fatal("cleared an active cache")
	}
	_ = s.Close()
	now = now.Add(fakeIPCacheRetention + time.Minute)
	s = startFixtureCache(t, p, clock)
	if _, ok := s.Lookup(old); ok {
		t.Fatal("expired retained mapping restored")
	}
	fresh, err := s.Create("fresh.example", false)
	if err != nil || fresh == old {
		t.Fatal("expiry rebound stale address")
	}
	s.maxEntries = 1
	if addr, err := s.Create("over-capacity.example", false); err == nil || addr.IsValid() {
		t.Fatal("capacity silently evicted or reassigned mapping")
	}
	if got, ok := s.Lookup(fresh); !ok || got != "fresh.example" {
		t.Fatal("capacity failure damaged old mapping")
	}
	_ = s.Close()
	if err := clearCache(p.cachePath, fixtureCacheOpen); err != nil {
		t.Fatal(err)
	}
	s = startFixtureCache(t, p, clock)
	if _, ok := s.Lookup(fresh); ok {
		t.Fatal("explicit disconnect retained mapping")
	}
}

func TestFakeIPCacheWriteFailureAndLateSessionCannotReturnAddress(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	ctx, cancel := context.WithCancel(context.Background())
	s := newPersistentFakeIPStore(ctx, p, fixtureCacheOpen, time.Now)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cancel()
	if addr, err := s.Create("late.example", false); err == nil || addr.IsValid() {
		t.Fatal("late session wrote mapping")
	}
	if _, ok := s.Lookup(netip.MustParseAddr("198.18.0.2")); ok {
		t.Fatal("late mapping visible")
	}
	_ = s.Close()
	s = startFixtureCache(t, p, time.Now)
	_ = s.database.DB.Close() // emulate a storage failure after successful start
	addr, err := s.Create("never-committed.example", false)
	if err == nil || addr.IsValid() || strings.Contains(err.Error(), "never-committed") || strings.Contains(err.Error(), p.cachePath) {
		t.Fatal("uncommitted address or sensitive backend error escaped")
	}
}

func TestFakeIPCacheLocalNamesIPv6AndCorruptionFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fakeip.db")
	p := cachePrepared(t, path)
	s := startFixtureCache(t, p, time.Now)
	for _, domain := range []string{"", "printer.LOCAL.", "NAS.LAN", "localhost", "router.home.arpa", "bad\x00.example", "a..example"} {
		if addr, err := s.Create(domain, false); err == nil || addr.IsValid() {
			t.Fatalf("invalid/local domain accepted: %q", domain)
		}
	}
	if addr, err := s.Create("public.example", true); err == nil || addr.IsValid() {
		t.Fatal("IPv6 fake mapping allocated")
	}
	_ = s.Close()
	if err := os.WriteFile(path, []byte("not a cache"), 0600); err != nil {
		t.Fatal(err)
	}
	s = newPersistentFakeIPStore(context.Background(), p, fixtureCacheOpen, time.Now)
	if err := s.Start(); err == nil || strings.Contains(err.Error(), path) {
		t.Fatal("corrupt cache accepted or path leaked")
	}
}

func TestFakeIPConcurrentAllocationAndRetiredStoreIsolation(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	s := startFixtureCache(t, p, time.Now)
	var wg sync.WaitGroup
	var expected netip.Addr
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr, err := s.Create("same.example", false)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if expected.IsValid() && expected != addr {
				t.Error("concurrent domain allocated twice")
			}
			expected = addr
		}()
	}
	wg.Wait()
	_ = s.Close()
	next := startFixtureCache(t, p, time.Now)
	if err := s.Reset(); err == nil {
		t.Fatal("retired store reset current generation")
	}
	if got, ok := next.Lookup(expected); !ok || got != "same.example" {
		t.Fatal("retired store damaged reopened cache")
	}
}

func TestFakeIPConfigRequiresTrustedPathAndResourceNamespace(t *testing.T) {
	c := testConfig()
	c.CachePath = "relative.db"
	if _, err := New(c); err == nil {
		t.Fatal("relative cache path accepted")
	}
	c.CachePath = filepath.Join(t.TempDir(), "fakeip.db")
	n, _ := c.Network(context.Background())
	if _, err := prepareConfig(testProfile(), c, n, netip.MustParseAddr("8.8.8.8")); err == nil {
		t.Fatal("cache missing account/resource namespace accepted")
	}
}

// The pinned original store silently returns an address after storage errors.
// Ensure our registered transport actually uses the strict replacement store.
func TestFakeIPRegisteredTransportUsesPersistentStore(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	g := newSessionGate()
	r, err := newRuntime(context.Background(), p, g)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	transport, ok := r.(*xrayRuntime).fake, r.(*xrayRuntime).fake != nil
	if !ok {
		t.Fatal("missing transport")
	}
	store := transport.store
	s, ok := store.(*persistentFakeIPStore)
	if !ok {
		t.Fatal("wrong persistent store")
	}
	s.open = fixtureCacheOpen
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("public.example", false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("after-close.example", false); !errors.Is(err, errFakeIPCache) {
		t.Fatal("closed store did not fail closed")
	}
}

type cacheRuntimeFixture struct {
	*fakeRuntime
	cache *persistentFakeIPStore
}

func (r *cacheRuntimeFixture) Start() error {
	return errors.Join(r.fakeRuntime.Start(), r.cache.Start())
}
func (r *cacheRuntimeFixture) Close() error {
	return errors.Join(r.fakeRuntime.Close(), r.cache.Close())
}

func TestFakeIPPrepareAndEngineRecreationRestoreMappingNeverObservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fakeip.db")
	p := cachePrepared(t, path)
	c := testConfig()
	c.CachePath = path
	var current *cacheRuntimeFixture
	makeEngine := func() *Tun {
		e, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		e.factory = func(ctx context.Context, prepared preparedConfig, g *sessionGate) (runtimeEngine, error) {
			current = &cacheRuntimeFixture{fakeRuntime: &fakeRuntime{gate: g, probe: func(context.Context) (string, error) { return "9.9.9.9", nil }},
				cache: newPersistentFakeIPStore(ctx, prepared, fixtureCacheOpen, time.Now)}
			return current, nil
		}
		t.Cleanup(func() { _ = e.Close() })
		return e
	}
	e := makeEngine()
	if err := e.Prepare(context.Background(), p.profile); err != nil {
		t.Fatal(err)
	}
	address, err := current.cache.Create("survives.example", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if err = e.Prepare(context.Background(), p.profile); err != nil {
		t.Fatal(err)
	}
	if got, ok := current.cache.Lookup(address); !ok || got != "survives.example" {
		t.Fatal("Prepare lost cache")
	}
	if e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("cache restored observation or gate requires detector")
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	e = makeEngine()
	if err = e.Prepare(context.Background(), p.profile); err != nil {
		t.Fatal(err)
	}
	if got, ok := current.cache.Lookup(address); !ok || got != "survives.example" {
		t.Fatal("engine recreation lost mapping")
	}
	if e.VerifiedIPv4() != "" || e.SetBusinessGate(true) != nil {
		t.Fatal("new engine retained observation or requires detector")
	}
}

func TestFakeIPCacheRejectsCorruptAllocationCursor(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	s := startFixtureCache(t, p, time.Now)
	_, _ = s.Create("existing.example", false)
	if err := s.database.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(cacheMeta).Put([]byte(p.fake4.String()), []byte{0, 0, 0, 1})
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s = newPersistentFakeIPStore(context.Background(), p, fixtureCacheOpen, time.Now)
	if err := s.Start(); err == nil {
		_ = s.Close()
		t.Fatal("cursor would reassign an existing FakeIP")
	}
}

func TestFakeIPCacheCursorExhaustionDoesNotWrap(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	s := startFixtureCache(t, p, time.Now)
	old, _ := s.Create("existing.example", false)
	if err := s.database.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(cacheMeta).Put([]byte(p.fake4.String()), []byte{0, 0, 255, 254})
	}); err != nil {
		t.Fatal(err)
	}
	if addr, err := s.Create("cannot-wrap.example", false); err == nil || addr.IsValid() {
		t.Fatal("exhausted pool reassigned a stale address")
	}
	if got, ok := s.Lookup(old); !ok || got != "existing.example" {
		t.Fatal("exhaustion damaged existing mapping")
	}
}

func TestFakeIPCacheDoesNotAdoptUnrelatedDatabase(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	db, err := fixtureCacheOpen(p.cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucket([]byte("other-application")); return err }); err != nil {
		t.Fatal(err)
	}
	_ = db.close()
	s := newPersistentFakeIPStore(context.Background(), p, fixtureCacheOpen, time.Now)
	if err = s.Start(); err == nil {
		_ = s.Close()
		t.Fatal("unrelated database adopted")
	}
	if err = clearCache(p.cachePath, fixtureCacheOpen); err == nil {
		t.Fatal("unrelated database cleared")
	}
}
