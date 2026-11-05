package routingupdate

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func upstreamFixture(t *testing.T, tag string, direct string) (map[string][]byte, []byte, []byte) {
	t.Helper()
	site, err := proto.Marshal(&geodata.GeoSiteList{Entry: []*geodata.GeoSite{
		{Code: "CN", Domain: []*geodata.Domain{{Type: geodata.Domain_Domain, Value: direct}}},
		{Code: "GEOLOCATION-!CN", Domain: []*geodata.Domain{{Type: geodata.Domain_Domain, Value: "foreign.invalid"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ip, err := proto.Marshal(&geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: "CN", Cidr: []*geodata.CIDR{{Ip: []byte{223, 5, 5, 0}, Prefix: 24}}}}})
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"tag_name": tag, "draft": false, "prerelease": false, "assets": []map[string]any{
		{"name": "geosite.dat", "size": len(site), "digest": "sha256:" + hash(site), "state": "uploaded"},
		{"name": "geoip.dat", "size": len(ip), "digest": "sha256:" + hash(ip), "state": "uploaded"},
	}})
	return map[string][]byte{releaseURL: meta, assetURL(tag, "geosite.dat"): site, assetURL(tag, "geoip.dat"): ip}, site, ip
}
func fixtureClient(files map[string][]byte, calls *atomic.Int32) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if calls != nil {
			calls.Add(1)
		}
		data, ok := files[r.URL.String()]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header), Request: r}, nil
	})}
}
func openTestStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestSuccessfulUpdatePersistsAndExistingSnapshotRemainsImmutable(t *testing.T) {
	dir := t.TempDir()
	s := openTestStore(t, dir)
	old, oldTag := s.Snapshot()
	files, _, _ := upstreamFixture(t, "203001010000", "new-domestic.invalid")
	result := s.Update(context.Background(), fixtureClient(files, nil), true)
	if result.LastError != "" || result.Source != "downloaded" || result.Version != "203001010000" {
		t.Fatalf("update: %+v", result)
	}
	current, tag := s.Snapshot()
	if tag == oldTag || current.Domain("new-domestic.invalid") != routingpolicy.Direct || old.Domain("new-domestic.invalid") != routingpolicy.Unknown {
		t.Fatal("snapshot changed or update not used")
	}
	reopened := openTestStore(t, dir)
	restored, _ := reopened.Snapshot()
	if restored.Domain("new-domestic.invalid") != routingpolicy.Direct || !restored.IsCN(netip.MustParseAddr("223.5.5.5")) {
		t.Fatal("cache did not survive process restart")
	}
}

func TestBrokenReleaseNeverReplacesWorkingRules(t *testing.T) {
	for _, kind := range []string{"hash", "truncated", "malformed", "missing", "rollback", "duplicate", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t, t.TempDir())
			good, _, _ := upstreamFixture(t, "203001010000", "old-domestic.invalid")
			if s.Update(context.Background(), fixtureClient(good, nil), true).LastError != "" {
				t.Fatal("setup")
			}
			bad, _, _ := upstreamFixture(t, "203001020000", "new-domestic.invalid")
			switch kind {
			case "hash":
				bad[assetURL("203001020000", "geosite.dat")][0] ^= 1
			case "truncated":
				bad[assetURL("203001020000", "geoip.dat")] = []byte{1}
			case "malformed":
				var m map[string]any
				json.Unmarshal(bad[releaseURL], &m)
				m["assets"].([]any)[0].(map[string]any)["digest"] = "sha256:" + hash([]byte{255})
				m["assets"].([]any)[0].(map[string]any)["size"] = 1
				bad[releaseURL], _ = json.Marshal(m)
				bad[assetURL("203001020000", "geosite.dat")] = []byte{255}
			case "missing":
				bad[releaseURL] = []byte(`{"tag_name":"203001020000","assets":[]}`)
			case "rollback":
				bad, _, _ = upstreamFixture(t, "202901010000", "new-domestic.invalid")
			case "duplicate":
				var m map[string]any
				json.Unmarshal(bad[releaseURL], &m)
				a := m["assets"].([]any)
				m["assets"] = append(a, a[0])
				bad[releaseURL], _ = json.Marshal(m)
			case "oversized":
				var m map[string]any
				json.Unmarshal(bad[releaseURL], &m)
				m["assets"].([]any)[0].(map[string]any)["size"] = 100000000
				bad[releaseURL], _ = json.Marshal(m)
			}
			if s.Update(context.Background(), fixtureClient(bad, nil), true).LastError == "" {
				t.Fatal("bad release accepted")
			}
			for _, store := range []*Store{s, openTestStore(t, s.directory)} {
				rules, tag := store.Snapshot()
				if tag != "203001010000" || rules.Domain("old-domestic.invalid") != routingpolicy.Direct {
					t.Fatal("working rules lost")
				}
			}
		})
	}
}

func TestDailyThrottleFailureRetryAndManualCheck(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	now := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	files, _, _ := upstreamFixture(t, "203001010000", "domestic.invalid")
	var calls atomic.Int32
	c := fixtureClient(files, &calls)
	s.Update(context.Background(), c, false)
	if calls.Load() != 3 {
		t.Fatal("initial auto check missing")
	}
	s.Update(context.Background(), c, false)
	if calls.Load() != 3 {
		t.Fatal("duplicate immediate auto check")
	}
	s.Update(context.Background(), c, true)
	if calls.Load() != 4 {
		t.Fatal("manual check did not check or redownloaded same version")
	}
	now = now.Add(24 * time.Hour)
	s.Update(context.Background(), c, false)
	if calls.Load() != 5 {
		t.Fatal("daily check missing")
	}
	bad := fixtureClient(nil, &calls)
	s.Update(context.Background(), bad, true)
	before := calls.Load()
	now = now.Add(59 * time.Minute)
	s.Update(context.Background(), bad, false)
	if calls.Load() != before {
		t.Fatal("failure loop")
	}
	now = now.Add(2 * time.Minute)
	s.Update(context.Background(), c, false)
	if calls.Load() != before+1 {
		t.Fatal("failure not retried")
	}
}

func TestCorruptedCurrentFallsBackToPreviousAndMissingCurrentRecovers(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "interrupted_commit"}[remove], func(t *testing.T) {
			s := openTestStore(t, t.TempDir())
			for _, v := range []string{"203001010000", "203001020000"} {
				files, _, _ := upstreamFixture(t, v, "domestic.invalid")
				if s.Update(context.Background(), fixtureClient(files, nil), true).LastError != "" {
					t.Fatal("setup")
				}
			}
			p := filepath.Join(s.directory, "current.zip")
			if remove {
				os.Remove(p)
			} else {
				os.WriteFile(p, []byte("partial"), 0600)
			}
			recovered := openTestStore(t, s.directory)
			_, tag := recovered.Snapshot()
			if tag != "203001010000" {
				t.Fatal("previous valid snapshot not recovered", tag)
			}
		})
	}
}

func TestCanceledConcurrentUpdateDoesNotPublishOrDuplicate(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	started := make(chan struct{})
	var calls atomic.Int32
	c := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	done := make(chan Status, 1)
	go func() { done <- s.Update(context.Background(), c, true) }()
	<-started
	if !s.Update(context.Background(), c, true).Updating || calls.Load() != 1 {
		t.Fatal("concurrent download")
	}
	s.Cancel()
	result := <-done
	if result.LastError != "UPDATE_CANCELLED" || result.Updating || result.Source != "bundled" {
		t.Fatalf("cancel: %+v", result)
	}
}

func TestDownloadDestinationsAndRedirectsAreRestricted(t *testing.T) {
	for _, raw := range []string{"http://github.com/a", "https://github.com.evil.invalid/a", "https://evil.invalid/a", "https://github.com:8443/a", "https://user:password@github.com/a", "https://127.0.0.1/a"} {
		if AllowedURL(raw) {
			t.Fatal("accepted", raw)
		}
	}
	for _, host := range []string{"github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		if !AllowedURL("https://" + host + "/a?signature=example") {
			t.Fatal("rejected allowed host")
		}
	}
	c := NewHTTPClient(nil)
	req, _ := http.NewRequest("GET", "https://evil.invalid/a", nil)
	if c.CheckRedirect(req, nil) == nil {
		t.Fatal("unsafe redirect")
	}
}

func TestFailedDiskCommitRetainsCurrentSnapshot(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	files, _, _ := upstreamFixture(t, "203001010000", "old.invalid")
	if s.Update(context.Background(), fixtureClient(files, nil), true).LastError != "" {
		t.Fatal("setup")
	}
	// A directory where the backup file belongs makes the atomic rotation fail
	// on both Windows and Android, without relying on Unix-only permission bits.
	if err := os.Mkdir(filepath.Join(s.directory, "previous.zip"), 0700); err != nil {
		t.Fatal(err)
	}
	files, _, _ = upstreamFixture(t, "203001020000", "new.invalid")
	if s.Update(context.Background(), fixtureClient(files, nil), true).LastError == "" {
		t.Fatal("disk failure ignored")
	}
	for _, store := range []*Store{s, openTestStore(t, s.directory)} {
		rules, version := store.Snapshot()
		if version != "203001010000" || rules.Domain("old.invalid") != routingpolicy.Direct {
			t.Fatal("disk failure replaced working rules")
		}
	}
}

func TestStartupReclaimsInterruptedStagingOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".rules-123.tmp", ".status-456.tmp", "keep.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("interrupted"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".rules-directory.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	openTestStore(t, dir)
	for _, name := range []string{".rules-123.tmp", ".status-456.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("orphan retained", name, err)
		}
	}
	for _, name := range []string{"keep.tmp", ".rules-directory.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("unowned path removed", name, err)
		}
	}
}
