package routingupdate

import (
	"archive/zip"
	"bluegrass/native/core/routingpolicy"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Version   string `json:"version"`
	Source    string `json:"source"`
	CheckedAt string `json:"checkedAt"`
	UpdatedAt string `json:"updatedAt"`
	Updating  bool   `json:"updating"`
	LastError string `json:"lastError"`
}

type Store struct {
	directory string
	mu        sync.RWMutex
	updateMu  sync.Mutex
	rules     *routingpolicy.Rules
	status    Status
	cancel    context.CancelFunc
	now       func() time.Time
}

// Android owns each cache directory in one application process. Serialize its
// staging-file lifecycle even if another Store is opened in that process.
var stagingMu sync.Mutex

func cleanStaging(directory string) error {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !(strings.HasPrefix(name, ".rules-") || strings.HasPrefix(name, ".status-")) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func bundledTag() string {
	return strings.TrimSuffix(strings.TrimPrefix(routingpolicy.Version, "loyalsoldier-"), "_v1")
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validTag(tag string) bool {
	if len(tag) != 12 {
		return false
	}
	_, err := time.Parse("200601021504", tag)
	return err == nil
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && strings.ToLower(value) == value
}

func New(directory string) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("UPDATE_FAILED")
	}
	base, err := routingpolicy.Load()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, errors.New("UPDATE_FAILED")
	}
	if err = cleanStaging(directory); err != nil {
		return nil, errors.New("UPDATE_FAILED")
	}
	s := &Store{directory: directory, rules: base, status: Status{Version: bundledTag(), Source: "bundled"}, now: time.Now}
	cacheInvalid := false
	for _, name := range []string{"current.zip", "previous.zip"} {
		record, site, ip, err := readArchive(filepath.Join(directory, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			cacheInvalid = true
			continue
		}
		rules, err := routingpolicy.ImportSourceData(site, ip)
		if err != nil {
			cacheInvalid = true
			continue
		}
		s.rules = rules
		if name == "previous.zip" {
			cacheInvalid = true
		}
		s.status.Version = record.Version
		s.status.Source = "downloaded"
		s.status.UpdatedAt = record.UpdatedAt
		break
	}
	if data, err := readBounded(filepath.Join(directory, "status.json"), 4096); err == nil {
		var state Status
		if json.Unmarshal(data, &state) == nil {
			if checked, e := time.Parse(time.RFC3339, state.CheckedAt); e == nil && !checked.After(s.now().Add(5*time.Minute)) {
				s.status.CheckedAt = state.CheckedAt
			}
			switch state.LastError {
			case "UPDATE_FAILED", "UPDATE_CANCELLED", "CACHE_INVALID":
				s.status.LastError = state.LastError
			}
		}
	}
	if cacheInvalid {
		s.status.LastError = "CACHE_INVALID"
		s.status.CheckedAt = ""
	}
	return s, nil
}

func (s *Store) Status() Status { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }

// Snapshot pins a complete immutable pair; existing sessions keep their pointer.
func (s *Store) Snapshot() (*routingpolicy.Rules, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rules, s.status.Version
}
func (s *Store) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Store) Update(parent context.Context, client *http.Client, force bool) Status {
	if !s.updateMu.TryLock() {
		return s.Status()
	}
	defer s.updateMu.Unlock()
	now := s.now().UTC()
	state := s.Status()
	interval := 24 * time.Hour
	if state.LastError != "" {
		interval = time.Hour
	}
	if checked, err := time.Parse(time.RFC3339, state.CheckedAt); !force && err == nil && now.Sub(checked) >= 0 && now.Sub(checked) < interval {
		return state
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Minute)
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.status.Updating = true
	s.status.LastError = ""
	s.status.CheckedAt = now.Format(time.RFC3339)
	s.mu.Unlock()
	if client == nil {
		client = NewHTTPClient(nil)
	}
	err := s.update(ctx, client, now)
	s.mu.Lock()
	s.cancel = nil
	s.status.Updating = false
	if err != nil {
		s.status.LastError = "UPDATE_FAILED"
		if errors.Is(ctx.Err(), context.Canceled) {
			s.status.LastError = "UPDATE_CANCELLED"
		}
	}
	result := s.status
	s.mu.Unlock()
	data, _ := json.Marshal(result)
	if e := writeAtomic(s.directory, "status.json", data); e != nil {
		s.mu.Lock()
		s.status.LastError = "UPDATE_FAILED"
		result = s.status
		s.mu.Unlock()
	}
	return result
}

type asset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

func (s *Store) update(ctx context.Context, client *http.Client, now time.Time) error {
	data, err := fetch(ctx, client, releaseURL, 1<<20)
	if err != nil {
		return err
	}
	var info release
	if json.Unmarshal(data, &info) != nil || !validTag(info.Tag) || info.Draft || info.Prerelease {
		return errors.New("invalid release")
	}
	current := s.Status()
	if info.Tag < current.Version || info.Tag < bundledTag() {
		return errors.New("older release")
	}
	wanted := map[string]asset{}
	for _, a := range info.Assets {
		if a.Name != "geosite.dat" && a.Name != "geoip.dat" {
			continue
		}
		if _, ok := wanted[a.Name]; ok {
			return errors.New("duplicate asset")
		}
		if a.Size <= 0 || a.Size > maxSourceBytes || a.State != "uploaded" || !strings.HasPrefix(a.Digest, "sha256:") || !validHash(strings.TrimPrefix(a.Digest, "sha256:")) {
			return errors.New("invalid asset")
		}
		wanted[a.Name] = a
	}
	if len(wanted) != 2 {
		return errors.New("missing asset")
	}
	if current.Source == "downloaded" && info.Tag == current.Version {
		return ctx.Err()
	}
	// Even the initial bundled release is downloaded once: a real verified cache
	// is established independently of the APK, then same-version checks are cheap.
	inputs := map[string][]byte{}
	for _, name := range []string{"geosite.dat", "geoip.dat"} {
		a := wanted[name]
		blob, e := fetch(ctx, client, assetURL(info.Tag, name), a.Size)
		if e != nil {
			return e
		}
		if int64(len(blob)) != a.Size || "sha256:"+hash(blob) != a.Digest {
			return errors.New("asset checksum")
		}
		inputs[name] = blob
	}
	rules, err := routingpolicy.ImportSourceData(inputs["geosite.dat"], inputs["geoip.dat"])
	if err != nil {
		return err
	}
	record := cacheRecord{Format: 1, Version: info.Tag, UpdatedAt: now.Format(time.RFC3339), SiteSHA: hash(inputs["geosite.dat"]), IPSHA: hash(inputs["geoip.dat"])}
	if err = s.commit(ctx, record, inputs["geosite.dat"], inputs["geoip.dat"]); err != nil {
		return err
	}
	s.mu.Lock()
	s.rules = rules
	s.status.Version = record.Version
	s.status.Source = "downloaded"
	s.status.UpdatedAt = record.UpdatedAt
	s.mu.Unlock()
	return nil
}

type cacheRecord struct {
	Format    int    `json:"format"`
	Version   string `json:"version"`
	UpdatedAt string `json:"updatedAt"`
	SiteSHA   string `json:"siteSha256"`
	IPSHA     string `json:"ipSha256"`
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > max {
		return nil, errors.New("invalid cache file")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errors.New("invalid cache size")
	}
	return data, nil
}

func readArchive(path string) (cacheRecord, []byte, []byte, error) {
	var record cacheRecord
	f, err := os.Open(path)
	if err != nil {
		return record, nil, nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return record, nil, nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 2*maxSourceBytes+(1<<20) {
		return record, nil, nil, errors.New("invalid cache archive")
	}
	z, err := zip.NewReader(f, stat.Size())
	if err != nil {
		return record, nil, nil, err
	}
	if len(z.File) != 3 {
		return record, nil, nil, errors.New("invalid cache entries")
	}
	files := map[string][]byte{}
	for _, entry := range z.File {
		limit := maxSourceBytes
		if entry.Name == "record.json" {
			limit = 4096
		} else if entry.Name != "geosite.dat" && entry.Name != "geoip.dat" {
			return record, nil, nil, errors.New("invalid cache name")
		}
		if _, found := files[entry.Name]; found || entry.UncompressedSize64 > uint64(limit) {
			return record, nil, nil, errors.New("invalid cache entry")
		}
		r, e := entry.Open()
		if e != nil {
			return record, nil, nil, e
		}
		blob, e := io.ReadAll(io.LimitReader(r, limit+1))
		r.Close()
		if e != nil || int64(len(blob)) > limit {
			return record, nil, nil, errors.New("invalid cache data")
		}
		files[entry.Name] = blob
	}
	if json.Unmarshal(files["record.json"], &record) != nil || record.Format != 1 || !validTag(record.Version) || record.Version < bundledTag() || !validHash(record.SiteSHA) || !validHash(record.IPSHA) {
		return record, nil, nil, errors.New("invalid cache record")
	}
	if _, err := time.Parse(time.RFC3339, record.UpdatedAt); err != nil {
		return record, nil, nil, errors.New("invalid cache time")
	}
	if hash(files["geosite.dat"]) != record.SiteSHA || hash(files["geoip.dat"]) != record.IPSHA {
		return record, nil, nil, errors.New("invalid cache checksum")
	}
	return record, files["geosite.dat"], files["geoip.dat"], nil
}

func (s *Store) commit(ctx context.Context, record cacheRecord, site, ip []byte) error {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	f, err := os.CreateTemp(s.directory, ".rules-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	z := zip.NewWriter(f)
	meta, _ := json.Marshal(record)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"record.json", meta}, {"geosite.dat", site}, {"geoip.dat", ip}} {
		w, e := z.Create(entry.name)
		if e == nil {
			_, e = w.Write(entry.data)
		}
		if e != nil {
			z.Close()
			f.Close()
			return e
		}
	}
	if err = z.Close(); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	current := filepath.Join(s.directory, "current.zip")
	// Preserve only a verified old pair. If current was corrupted and startup
	// recovered previous, leave that previous archive intact.
	if _, oldSite, oldIP, e := readArchive(current); e == nil {
		if _, e = routingpolicy.ImportSourceData(oldSite, oldIP); e == nil {
			if e = os.Rename(current, filepath.Join(s.directory, "previous.zip")); e != nil {
				return e
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(name, current); err != nil {
		return err
	}
	// On systems which support directory fsync, persist the rename as well.
	if dir, e := os.Open(s.directory); e == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func writeAtomic(directory, name string, data []byte) error {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	f, err := os.CreateTemp(directory, ".status-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, filepath.Join(directory, name)); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}
