package tunengine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"maps"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	bolt "github.com/sagernet/bbolt"
)

func cachedDNSAnswer(t *testing.T, store *persistentFakeIPStore, name string) netip.Addr {
	t.Helper()
	transport := &persistentFakeIPTransport{store: store}
	reply, err := transport.Exchange(context.Background(), new(dns.Msg).SetQuestion(dns.Fqdn(name), dns.TypeA))
	if err != nil || reply == nil || len(reply.Answer) != 1 {
		t.Fatal("fake-IP DNS query", reply, err)
	}
	answer, ok := reply.Answer[0].(*dns.A)
	if !ok || answer.Hdr.Ttl <= 2 {
		t.Fatal("DNS answer must remain cacheable across this reconnect", reply)
	}
	address, ok := netip.AddrFromSlice(answer.A.To4())
	if !ok {
		t.Fatal("invalid fake-IP answer")
	}
	return address
}

// Disconnect must forget A's domain but never give an application-cached A
// address to B, even after changing pools and clearing repeatedly within TTL.
func TestClearCacheMappingsDisconnectPreservesBothPoolHighwaters(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	pools := []struct {
		prefix, next string
		issued       int
	}{
		{"198.18.0.0/16", "198.18.0.4", 2},
		{"198.19.0.0/16", "198.19.0.5", 3},
	}
	old := map[string][]netip.Addr{}
	for _, pool := range pools {
		p.fake4 = netip.MustParsePrefix(pool.prefix)
		s := startFixtureCache(t, p, clock)
		for _, name := range []string{"a.example", "a2.example", "a3.example"}[:pool.issued] {
			old[pool.prefix] = append(old[pool.prefix], cachedDNSAnswer(t, s, name))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err != nil {
		t.Fatal(err)
	}
	if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err != nil {
		t.Fatal("idempotent release", err)
	}
	for _, pool := range pools {
		now = now.Add(time.Second)
		p.fake4 = netip.MustParsePrefix(pool.prefix)
		s := startFixtureCache(t, p, clock)
		for _, address := range old[pool.prefix] {
			if name, ok := s.Lookup(address); ok {
				t.Fatal("disconnect retained domain", address, name)
			}
		}
		fresh := cachedDNSAnswer(t, s, "b.example")
		if fresh.String() != pool.next {
			t.Fatalf("pool %s lost highwater: got %s, want %s", pool.prefix, fresh, pool.next)
		}
		for _, address := range old[pool.prefix] {
			if name, ok := s.Lookup(address); ok {
				t.Fatal("cached A address rebound after reconnect", address, name)
			}
		}
		if name, ok := s.Lookup(fresh); !ok || name != "b.example" {
			t.Fatal("new mapping unavailable")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClearCacheMappingsEmptyCacheIsNoop(t *testing.T) {
	if err := ClearCacheMappings(""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fakeip.db")
	for i := 0; i < 2; i++ {
		if err := clearCacheMappings(path, fixtureCacheOpen); err != nil {
			t.Fatal(err)
		}
		db, err := fixtureCacheOpen(path)
		if err != nil {
			t.Fatal(err)
		}
		err = db.View(func(tx *bolt.Tx) error {
			return tx.ForEach(func(_ []byte, _ *bolt.Bucket) error { t.Error("empty database gained a bucket"); return nil })
		})
		if closeErr := db.close(); err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
}

func TestClearCacheMappingsRetainsMetaBytesAndNeverResetsExhaustedPools(t *testing.T) {
	for _, prefix := range []string{"198.18.0.0/16", "198.19.0.0/16"} {
		t.Run(prefix, func(t *testing.T) {
			p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
			p.fake4 = netip.MustParsePrefix(prefix)
			s := startFixtureCache(t, p, time.Now)
			old := cachedDNSAnswer(t, s, "a.example")
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := fixtureCacheOpen(p.cachePath)
			if err != nil {
				t.Fatal(err)
			}
			other := "198.19.0.0/16"
			if prefix == other {
				other = "198.18.0.0/16"
			}
			err = db.Update(func(tx *bolt.Tx) error {
				meta := tx.Bucket(cacheMeta)
				if err := meta.Put([]byte(prefix), binary.BigEndian.AppendUint32(nil, 65534)); err != nil {
					return err
				}
				return meta.Put([]byte(other), binary.BigEndian.AppendUint32(nil, 17))
			})
			if err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			err = db.View(func(tx *bolt.Tx) error {
				return tx.Bucket(cacheMeta).ForEach(func(k, v []byte) error { before[string(k)] = string(v); return nil })
			})
			if closeErr := db.close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err != nil {
				t.Fatal(err)
			}
			db, err = fixtureCacheOpen(p.cachePath)
			if err != nil {
				t.Fatal(err)
			}
			after := map[string]string{}
			err = db.View(func(tx *bolt.Tx) error {
				return tx.Bucket(cacheMeta).ForEach(func(k, v []byte) error { after[string(k)] = string(v); return nil })
			})
			if closeErr := db.close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if !maps.Equal(before, after) {
				t.Fatal("release changed allocator metadata", before, after)
			}
			s = startFixtureCache(t, p, time.Now)
			if address, err := s.Create("b.example", false); err == nil || address.IsValid() {
				t.Fatal("release recycled an exhausted pool")
			}
			if name, ok := s.Lookup(old); ok {
				t.Fatal("released address remains mapped", name)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClearCacheMappingsRejectsLiveStoreAndPreservesLegacyClear(t *testing.T) {
	p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
	s := startFixtureCache(t, p, time.Now)
	old := cachedDNSAnswer(t, s, "a.example")
	if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err == nil {
		t.Fatal("cleared a live database")
	}
	if name, ok := s.Lookup(old); !ok || name != "a.example" {
		t.Fatal("failed release damaged mapping")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := clearCache(p.cachePath, fixtureCacheOpen); err != nil {
		t.Fatal(err)
	}
	s = startFixtureCache(t, p, time.Now)
	if fresh := cachedDNSAnswer(t, s, "b.example"); fresh != old {
		t.Fatal("legacy ClearCache no longer resets allocation")
	}
}

func TestClearCacheMappingsRejectsInvalidMetadataWithoutErasingEvidence(t *testing.T) {
	for name, corrupt := range map[string]func(*bolt.Tx) error{
		"state JSON": func(tx *bolt.Tx) error { return tx.Bucket(cacheMeta).Put(cacheStateKey, []byte("invalid")) },
		"state pool": func(tx *bolt.Tx) error {
			var s cacheState
			_ = json.Unmarshal(tx.Bucket(cacheMeta).Get(cacheStateKey), &s)
			s.Pool = netip.MustParsePrefix("10.0.0.0/16")
			return putCacheState(tx.Bucket(cacheMeta), s)
		},
		"state namespace": func(tx *bolt.Tx) error {
			var s cacheState
			_ = json.Unmarshal(tx.Bucket(cacheMeta).Get(cacheStateKey), &s)
			s.Namespace = "invalid"
			return putCacheState(tx.Bucket(cacheMeta), s)
		},
		"current short cursor": func(tx *bolt.Tx) error { return tx.Bucket(cacheMeta).Put([]byte("198.18.0.0/16"), []byte{2}) },
		"current regressed cursor": func(tx *bolt.Tx) error {
			return tx.Bucket(cacheMeta).Put([]byte("198.18.0.0/16"), binary.BigEndian.AppendUint32(nil, 1))
		},
		"other overflow cursor": func(tx *bolt.Tx) error {
			return tx.Bucket(cacheMeta).Put([]byte("198.19.0.0/16"), binary.BigEndian.AppendUint32(nil, 65535))
		},
		"other zero cursor": func(tx *bolt.Tx) error {
			return tx.Bucket(cacheMeta).Put([]byte("198.19.0.0/16"), binary.BigEndian.AppendUint32(nil, 0))
		},
		"unknown meta": func(tx *bolt.Tx) error { return tx.Bucket(cacheMeta).Put([]byte("other"), []byte("unknown")) },
		"broken reverse mapping": func(tx *bolt.Tx) error {
			return tx.Bucket(cacheAddresses).Put(netip.MustParseAddr("198.18.0.2").AsSlice(), []byte("b.example"))
		},
		"unrelated database": func(tx *bolt.Tx) error { _, err := tx.CreateBucket([]byte("other")); return err },
	} {
		t.Run(name, func(t *testing.T) {
			p := cachePrepared(t, filepath.Join(t.TempDir(), "fakeip.db"))
			s := startFixtureCache(t, p, time.Now)
			address := cachedDNSAnswer(t, s, "a.example")
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := fixtureCacheOpen(p.cachePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Update(corrupt); err != nil {
				t.Fatal(err)
			}
			if err := db.close(); err != nil {
				t.Fatal(err)
			}
			if err := clearCacheMappings(p.cachePath, fixtureCacheOpen); err == nil {
				t.Fatal("invalid allocator evidence accepted")
			}
			db, err = fixtureCacheOpen(p.cachePath)
			if err != nil {
				t.Fatal(err)
			}
			err = db.View(func(tx *bolt.Tx) error {
				if got := tx.Bucket(cacheNames).Get([]byte("a.example")); string(got) != string(address.AsSlice()) {
					t.Error("failed clear erased mapping evidence")
				}
				return nil
			})
			if closeErr := db.close(); err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
		})
	}
}
