package update

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func downloadFixture(t *testing.T, handler http.HandlerFunc) (*Checker, *Candidate, *httptest.Server) {
	t.Helper()
	c, key, m := fixture(t)
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c.hosts[u.Host] = true
	c.client.Transport = srv.Client().Transport
	m.Releases[0].URLs = []string{srv.URL + "/setup.exe"}
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil {
		t.Fatal(err)
	}
	// The only substituted boundary is the OS signature service; hash, HTTP,
	// policy, file IO, cache binding and resumption are exercised for real.
	c.verifyPublisher = func(context.Context, *artifactFile, string) error { return nil }
	return c, r.Candidate, srv
}

func unmetered(context.Context) (NetworkCost, error) { return NetworkUnmetered, nil }

func TestDownloadResumesAndRevalidatesCache(t *testing.T) {
	var calls atomic.Int32
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", "7")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("pac"))
			return
		}
		if r.Header.Get("Range") != "bytes=3-" {
			t.Errorf("bad resume range %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 3-6/7")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("kage"))
	})
	dir := t.TempDir()
	opts := DownloadOptions{Network: unmetered}
	if _, err := c.Download(context.Background(), candidate, dir, opts); err == nil {
		t.Fatal("truncated download accepted")
	}
	ready, err := c.Download(context.Background(), candidate, dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if ready.PackageID != candidate.ID() || ready.Size != 7 {
		t.Fatalf("bad ready metadata: %+v", ready)
	}
	data, err := os.ReadFile(ready.Path)
	if err != nil || string(data) != "package" {
		t.Fatalf("bad download %q %v", data, err)
	}
	if err := c.Revalidate(context.Background(), candidate, ready.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), candidate, dir, opts); err != nil || calls.Load() != 2 {
		t.Fatalf("cache reused without network: %d %v", calls.Load(), err)
	}
	if err := os.WriteFile(ready.Path, []byte("tamper!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Revalidate(context.Background(), candidate, ready.Path); err == nil {
		t.Fatal("tampered ready cache accepted")
	}
}

func TestNetworkConsentIsPackageScoped(t *testing.T) {
	var calls atomic.Int32
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte("package")) })
	for _, cost := range []NetworkCost{NetworkUnknown, NetworkMetered, NetworkCellular} {
		for _, consent := range []string{"", "other-package"} {
			_, err := c.Download(context.Background(), candidate, t.TempDir(), DownloadOptions{Network: func(context.Context) (NetworkCost, error) { return cost, nil }, ConsentPackageID: consent})
			if !errors.Is(err, ErrWaitingNetwork) {
				t.Fatalf("cost %v: %v", cost, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("network request without consent")
	}
	_, err := c.Download(context.Background(), candidate, t.TempDir(), DownloadOptions{Network: func(context.Context) (NetworkCost, error) { return NetworkMetered, nil }, ConsentPackageID: candidate.ID()})
	if err != nil || calls.Load() != 1 {
		t.Fatalf("explicit scoped consent failed: %v", err)
	}
	_, err = c.Download(context.Background(), candidate, t.TempDir(), DownloadOptions{})
	if !errors.Is(err, ErrWaitingNetwork) {
		t.Fatalf("missing network capability treated as free: %v", err)
	}
}

func TestMidRequestNetworkChangeCancelsAndPreservesPartial(t *testing.T) {
	var cost atomic.Int32
	cost.Store(int32(NetworkUnmetered))
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "7")
		_, _ = w.Write([]byte("pac"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dir := t.TempDir()
	_, err := c.Download(ctx, candidate, dir, DownloadOptions{
		Network: func(context.Context) (NetworkCost, error) { return NetworkCost(cost.Load()), nil },
		Progress: func(p Progress) {
			if p.Downloaded >= 3 {
				cost.Store(int32(NetworkMetered))
			}
		},
	})
	if !errors.Is(err, ErrWaitingNetwork) {
		t.Fatalf("did not stop in-flight read on metered switch: %v", err)
	}
	partial, err := os.ReadFile(filepath.Join(dir, candidate.ID()+".part"))
	if err != nil || string(partial) != "pac" {
		t.Fatalf("partial lost %q %v", partial, err)
	}
}

func TestPhysicalRouteChangePausesEvenWithPackageConsent(t *testing.T) {
	var changed atomic.Bool
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "7")
		_, _ = w.Write([]byte("pac"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := c.Download(ctx, candidate, dir, DownloadOptions{
		ConsentPackageID: candidate.ID(), Network: unmetered,
		RouteValid: func(context.Context) error {
			if changed.Load() {
				return errors.New("physical route changed")
			}
			return nil
		},
		Progress: func(p Progress) {
			if p.Downloaded >= 3 {
				changed.Store(true)
			}
		},
	})
	if !errors.Is(err, ErrWaitingNetwork) {
		t.Fatal("consent bypassed route change", err)
	}
	partial, err := os.ReadFile(filepath.Join(dir, candidate.ID()+".part"))
	if err != nil || string(partial) != "pac" {
		t.Fatal("route pause lost resumable bytes", err)
	}
}

func TestRoutePreparationPrecedesCostAndStopsBeforePackageRequest(t *testing.T) {
	var requests atomic.Int32
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("package")) })
	prepared := false
	_, err := c.Download(context.Background(), candidate, t.TempDir(), DownloadOptions{
		Prepare: func(_ context.Context, mirror string) error { prepared = true; return nil },
		Network: func(context.Context) (NetworkCost, error) {
			if !prepared {
				t.Error("cost checked before route was selected")
			}
			return NetworkUnknown, nil
		},
	})
	if !prepared || !errors.Is(err, ErrWaitingNetwork) || requests.Load() != 0 {
		t.Fatal("package started without verified route cost", err)
	}
}

func TestPolicyPauseAtSocketCreationIsNotRetriedAsTransferFailure(t *testing.T) {
	c, candidate, server := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("socket was opened after policy pause") })
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, ErrWaitingNetwork }
	c.client.Transport = transport
	_, err := c.Download(context.Background(), candidate, t.TempDir(), DownloadOptions{Network: unmetered})
	if !errors.Is(err, ErrWaitingNetwork) {
		t.Fatal("route/cost dial pause lost at HTTP boundary", err)
	}
}

func TestDownloadRejectsBadResponsesAndPublisher(t *testing.T) {
	for _, mode := range []string{"overflow", "hash", "range", "encoding", "redirect", "publisher"} {
		t.Run(mode, func(t *testing.T) {
			c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "overflow":
					_, _ = w.Write([]byte("package-overflow"))
				case "hash":
					_, _ = w.Write([]byte("garbage"))
				case "range":
					w.Header().Set("Content-Range", "bytes 2-6/7")
					w.WriteHeader(206)
					_, _ = w.Write([]byte("ckage"))
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = w.Write([]byte("package"))
				case "redirect":
					http.Redirect(w, r, "https://evil.test/setup.exe", 302)
				default:
					_, _ = w.Write([]byte("package"))
				}
			})
			if mode == "publisher" {
				c.verifyPublisher = func(context.Context, *artifactFile, string) error { return errors.New("wrong publisher") }
			}
			dir := t.TempDir()
			_, err := c.Download(context.Background(), candidate, dir, DownloadOptions{Network: unmetered})
			if err == nil {
				t.Fatal("invalid artifact became ready")
			}
			if _, err := os.Stat(filepath.Join(dir, candidate.ID()+".exe")); !os.IsNotExist(err) {
				t.Fatal("invalid ready file exists")
			}
		})
	}
}

func TestRangeIgnoredRestartsAndCandidateCannotBeForged(t *testing.T) {
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("package")) })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, candidate.ID()+".part"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	ready, err := c.Download(context.Background(), candidate, dir, DownloadOptions{Network: unmetered})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Revalidate(context.Background(), &Candidate{}, ready.Path); err == nil {
		t.Fatal("forged candidate accepted")
	}
	c.now = func() time.Time { return testNow.Add(20 * 24 * time.Hour) }
	if err := c.Revalidate(context.Background(), candidate, ready.Path); err == nil {
		t.Fatal("expired candidate accepted")
	}
}

func TestManifestHTTPBoundAndRedirect(t *testing.T) {
	for _, mode := range []string{"large", "redirect", "gzip"} {
		t.Run(mode, func(t *testing.T) {
			c, _, srv := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "large":
					_, _ = fmt.Fprint(w, strings.Repeat(" ", MaxManifestBytes+1))
				case "redirect":
					http.Redirect(w, r, "https://evil.test/a", 302)
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
					_, _ = fmt.Fprint(w, "{}")
				}
			})
			if _, err := c.Check(context.Background(), srv.URL+"/manifest", Floor{}); err == nil {
				t.Fatal("invalid manifest HTTP accepted")
			}
		})
	}
}

func TestRevalidateChecksPublisherAfterContent(t *testing.T) {
	c, key, m := fixture(t)
	r, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "package.exe")
	_ = os.WriteFile(path, []byte("package"), 0600)
	// Production verification, no fake signer: even a matching hash is insufficient.
	if err := c.Revalidate(context.Background(), r.Candidate, path); err == nil {
		t.Fatal("hash-only verification accepted unsigned package")
	}
}

func TestRevokedCandidateAndCancelledDownload(t *testing.T) {
	c, key, m := fixture(t)
	first, err := c.Verify(signed(t, key, m), Floor{})
	if err != nil {
		t.Fatal(err)
	}
	m.Sequence++
	m.Releases[0].Rollout = 0
	if _, err := c.Verify(signed(t, key, m), Floor{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), first.Candidate, t.TempDir(), DownloadOptions{Network: unmetered}); err == nil {
		t.Fatal("superseded manifest candidate accepted")
	}
	c, candidate, _ := downloadFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled task sent request") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Download(ctx, candidate, t.TempDir(), DownloadOptions{Network: unmetered}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}

func TestVersionComparisonsAndRollout(t *testing.T) {
	versions := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.2", "1.0.0-alpha.10", "1.0.0-beta", "1.0.0", "1.0.1"}
	for i := 0; i < len(versions)-1; i++ {
		a, _ := parseVersion(versions[i], false)
		b, _ := parseVersion(versions[i+1], false)
		if compareVersion(a, b) >= 0 {
			t.Fatalf("version order %s %s", versions[i], versions[i+1])
		}
	}
	// Published algorithm is the full digest interpreted big endian modulo 100.
	if rolloutBucket("test-installation") != 98 {
		t.Fatal("invalid cohort")
	}
}
