package tunengine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bluegrass/native/core/engine"
	mdns "github.com/miekg/dns"
	bolt "github.com/sagernet/bbolt"
)

const (
	fakeIPCacheRetention = 24 * time.Hour // maximum idle retention before reopen
	fakeIPMaxEntries     = 16384
	fakeIPMaxBytes       = 32 << 20
)

var (
	errFakeIPCache = errors.New("FAKEIP_CACHE_UNAVAILABLE")
	cacheMeta      = []byte("meta")
	cacheNames     = []byte("names")
	cacheAddresses = []byte("addresses")
	cacheStateKey  = []byte("state")
)

type cacheDatabase struct {
	*bolt.DB
	release func() error
}
type cacheOpener func(string) (*cacheDatabase, error)

func (d *cacheDatabase) close() error { return errors.Join(d.DB.Close(), d.release()) }

func cacheDBOptions(open func(string, int, os.FileMode) (*os.File, error)) *bolt.Options {
	return &bolt.Options{Timeout: 100 * time.Millisecond, MaxSize: fakeIPMaxBytes,
		OpenFile: open, Logger: &bolt.DefaultLogger{Logger: log.New(io.Discard, "", 0)}}
}

func validCachePath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n*?\"") || filepath.Ext(path) != ".db" {
		return false
	}
	volume := filepath.VolumeName(path)
	// No UNC, device namespace, alternate stream, root file or dot traversal.
	if volume != "" && (len(volume) != 2 || volume[1] != ':' || strings.Contains(path[len(volume):], ":")) {
		return false
	}
	return filepath.Dir(path) != volume+string(filepath.Separator)
}

// ClearCache is an explicit logical-disconnect operation, NOT a runtime Stop.
// Pass the same trusted service path used by Config.CachePath after all engines
// are closed. Exclusive database ownership rejects clearing a live engine. It
// empties the owned DB transactionally; it does not delete directories or WFP.
func ClearCache(path string) error { return clearCache(path, openProtectedCache) }
func clearCache(path string, open cacheOpener) error {
	if path == "" {
		return nil
	}
	if !validCachePath(path) {
		return errFakeIPCache
	}
	db, err := open(path)
	if err != nil {
		return errFakeIPCache
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if err := cacheLayout(tx); err != nil {
			return err
		}
		for _, name := range [][]byte{cacheNames, cacheAddresses, cacheMeta} {
			if tx.Bucket(name) != nil {
				if err := tx.DeleteBucket(name); err != nil {
					return err
				}
			}
		}
		return nil
	})
	closeErr := db.close()
	if err != nil || closeErr != nil {
		return errFakeIPCache
	}
	return nil
}

type cacheState struct {
	Version   int          `json:"version"`
	Namespace string       `json:"namespace"`
	Pool      netip.Prefix `json:"pool"`
	Touched   int64        `json:"touched"`
}

// Namespace contains no credentials or raw account identifiers on disk. The
// full classifier policy is included because custom Network providers may keep
// Generation constant while changing trusted local-domain mappings.
func mappingNamespace(p preparedConfig) string {
	input := struct {
		Version     int
		Key         engine.ResourceKey
		Protocol    string
		RoutingMode string
		AllowLAN    bool
		Network     NetworkSnapshot
		Pool        netip.Prefix
	}{2, p.profile.Key, p.profile.Protocol, p.profile.RoutingMode, p.allowLAN, p.network, p.fake4}
	data, _ := json.Marshal(input)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

var _ fakeIPStore = (*persistentFakeIPStore)(nil)

type persistentFakeIPStore struct {
	mu         sync.Mutex
	ctx        context.Context
	prepared   preparedConfig
	open       cacheOpener
	now        func() time.Time
	database   *cacheDatabase
	state      cacheState
	closed     bool
	maxEntries int
}

func newPersistentFakeIPStore(ctx context.Context, p preparedConfig, open cacheOpener, now func() time.Time) *persistentFakeIPStore {
	return &persistentFakeIPStore{ctx: ctx, prepared: p, open: open, now: now, maxEntries: fakeIPMaxEntries}
}

func (s *persistentFakeIPStore) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return errFakeIPCache
	}
	if s.database != nil {
		return nil
	}
	if s.prepared.fake4 != netip.MustParsePrefix("198.18.0.0/16") && s.prepared.fake4 != netip.MustParsePrefix("198.19.0.0/16") {
		return errFakeIPCache
	}
	db, err := s.open(s.prepared.cachePath)
	if err != nil {
		return errFakeIPCache
	}
	now := s.now()
	state := cacheState{1, mappingNamespace(s.prepared), s.prepared.fake4, now.Unix()}
	err = db.Update(func(tx *bolt.Tx) error {
		if err := cacheLayout(tx); err != nil {
			return err
		}
		meta, err := tx.CreateBucketIfNotExists(cacheMeta)
		if err != nil {
			return err
		}
		var previous cacheState
		if raw := meta.Get(cacheStateKey); raw != nil {
			if len(raw) > 1024 || json.Unmarshal(raw, &previous) != nil || previous.Version != 1 || len(previous.Namespace) != 64 || !previous.Pool.IsValid() {
				return errFakeIPCache
			}
		}
		// A namespace reset preserves per-pool allocation high-water marks. Old
		// application FakeIPs then fail lookup rather than point to new domains.
		if previous.Namespace != state.Namespace || previous.Pool != state.Pool ||
			previous.Touched > now.Add(5*time.Minute).Unix() || now.Sub(time.Unix(previous.Touched, 0)) > fakeIPCacheRetention {
			if err = resetCacheMappings(tx); err != nil {
				return err
			}
		}
		for _, name := range [][]byte{cacheNames, cacheAddresses} {
			if _, err = tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		for _, prefix := range []string{"198.18.0.0/16", "198.19.0.0/16"} {
			value := meta.Get([]byte(prefix))
			if value != nil && (len(value) != 4 || binary.BigEndian.Uint32(value) > 65534) {
				return errFakeIPCache
			}
		}
		if err := s.validateMappings(tx); err != nil {
			return err
		}
		return putCacheState(meta, state)
	})
	if err != nil {
		_ = db.close()
		return errFakeIPCache
	}
	s.database, s.state = db, state
	return nil
}

func cacheLayout(tx *bolt.Tx) error {
	count := 0
	err := tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
		count++
		if string(name) != string(cacheMeta) && string(name) != string(cacheNames) && string(name) != string(cacheAddresses) {
			return errFakeIPCache
		}
		return nil
	})
	if err != nil || count != 0 && count != 3 {
		return errFakeIPCache
	}
	if count == 3 && tx.Bucket(cacheMeta).Get(cacheStateKey) == nil {
		return errFakeIPCache
	}
	return nil
}

func resetCacheMappings(tx *bolt.Tx) error {
	for _, name := range [][]byte{cacheNames, cacheAddresses} {
		if tx.Bucket(name) != nil {
			if err := tx.DeleteBucket(name); err != nil {
				return err
			}
		}
		if _, err := tx.CreateBucket(name); err != nil {
			return err
		}
	}
	return nil
}

func putCacheState(meta *bolt.Bucket, state cacheState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return meta.Put(cacheStateKey, data)
}

func (s *persistentFakeIPStore) validateMappings(tx *bolt.Tx) error {
	names, addresses := tx.Bucket(cacheNames), tx.Bucket(cacheAddresses)
	var highWater uint32
	if raw := tx.Bucket(cacheMeta).Get([]byte(s.prepared.fake4.String())); len(raw) == 4 {
		highWater = binary.BigEndian.Uint32(raw)
	}
	count := 0
	err := names.ForEach(func(k, v []byte) error {
		count++
		domain := string(k)
		addr, ok := netip.AddrFromSlice(v)
		if count > s.maxEntries || !ok || !s.prepared.fake4.Contains(addr) || !s.validDomain(domain) || string(addresses.Get(v)) != domain {
			return errFakeIPCache
		}
		bytes := addr.As4()
		offset := uint32(bytes[2])<<8 | uint32(bytes[3])
		if offset < 2 || offset > 65534 || offset > highWater {
			return errFakeIPCache
		}
		return nil
	})
	if err != nil {
		return err
	}
	return addresses.ForEach(func(k, v []byte) error {
		if string(names.Get(v)) != string(k) {
			return errFakeIPCache
		}
		return nil
	})
}

func (s *persistentFakeIPStore) validDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || domain != normalizedName(domain) || localName(domain) || strings.ContainsAny(domain, "\x00\r\n\\ /:") || strings.Contains(domain, "..") {
		return false
	}
	if _, ok := mdns.IsDomainName(domain); !ok {
		return false
	}
	if strings.HasSuffix(domain, ".in-addr.arpa") || strings.HasSuffix(domain, ".ip6.arpa") {
		return false
	}
	for _, resolver := range s.prepared.network.DNS {
		for _, suffix := range resolver.Domains {
			suffix = normalizedName(suffix)
			if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
				return false
			}
		}
	}
	return true
}

func (s *persistentFakeIPStore) Contains(address netip.Addr) bool {
	return s.prepared.fake4.Contains(address)
}

func (s *persistentFakeIPStore) Create(domain string, ipv6 bool) (netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	domain = normalizedName(domain)
	if ipv6 || s.closed || s.database == nil || s.ctx.Err() != nil || !s.validDomain(domain) {
		return netip.Addr{}, errFakeIPCache
	}
	var address netip.Addr
	err := s.database.Update(func(tx *bolt.Tx) error {
		names, addresses, meta := tx.Bucket(cacheNames), tx.Bucket(cacheAddresses), tx.Bucket(cacheMeta)
		if raw := names.Get([]byte(domain)); raw != nil {
			address, _ = netip.AddrFromSlice(raw)
		} else {
			if names.Stats().KeyN >= s.maxEntries {
				return errFakeIPCache
			}
			cursor := uint32(1)
			if raw := meta.Get([]byte(s.prepared.fake4.String())); raw != nil {
				cursor = binary.BigEndian.Uint32(raw)
			}
			if cursor >= 65534 {
				return errFakeIPCache
			} // never wrap/rebind live or stale addresses
			cursor++
			base := s.prepared.fake4.Addr().As4()
			base[2], base[3] = byte(cursor>>8), byte(cursor)
			address = netip.AddrFrom4(base)
			if err := names.Put([]byte(domain), address.AsSlice()); err != nil {
				return err
			}
			if err := addresses.Put(address.AsSlice(), []byte(domain)); err != nil {
				return err
			}
			if err := meta.Put([]byte(s.prepared.fake4.String()), binary.BigEndian.AppendUint32(nil, cursor)); err != nil {
				return err
			}
		}
		s.state.Touched = s.now().Unix()
		return putCacheState(meta, s.state)
	})
	// Mapping AND cursor are synced in one transaction before returning a DNS
	// answer. Upstream Store.Create intentionally swallows these errors; we don't.
	if err != nil || s.ctx.Err() != nil {
		return netip.Addr{}, errFakeIPCache
	}
	return address, nil
}

func (s *persistentFakeIPStore) Lookup(address netip.Addr) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.database == nil || s.ctx.Err() != nil || !s.Contains(address) {
		return "", false
	}
	var domain string
	err := s.database.View(func(tx *bolt.Tx) error { domain = string(tx.Bucket(cacheAddresses).Get(address.AsSlice())); return nil })
	return domain, err == nil && domain != ""
}

func (s *persistentFakeIPStore) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.database == nil || s.ctx.Err() != nil {
		return errFakeIPCache
	}
	err := s.database.Update(func(tx *bolt.Tx) error { return resetCacheMappings(tx) })
	if err != nil {
		return errFakeIPCache
	}
	return nil
}

func (s *persistentFakeIPStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.database == nil {
		return nil
	}
	s.state.Touched = s.now().Unix()
	err := s.database.Update(func(tx *bolt.Tx) error { return putCacheState(tx.Bucket(cacheMeta), s.state) })
	closeErr := s.database.close()
	if err != nil || closeErr != nil {
		return errFakeIPCache
	}
	return nil
}
