package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

type state struct {
	Schema         int             `json:"schema"`
	InstallationID string          `json:"installation_id"`
	Floor          update.Floor    `json:"floor"`
	Manifest       json.RawMessage `json:"manifest"`
}
type stateStore interface {
	Load() (*state, error)
	Save(state) error
	Handoff(string, state) error
	Dir() string
}
type verifier interface {
	Verify([]byte, update.Floor) (update.Result, error)
	VerifyStartupPolicy([]byte, update.Floor) (update.StartupPolicy, error)
	Download(context.Context, *update.Candidate, string, update.DownloadOptions) (update.Ready, error)
	Revalidate(context.Context, *update.Candidate, string) error
}
type engine struct {
	cfg      releaseConfig
	store    stateStore
	state    state
	verifier verifier
	fetch    func(context.Context, string) ([]byte, error)
	elevate  func(context.Context, string, string) error
	network  *routePolicy
}

// prepareApply commits the independent machine sequence floor before any
// package is staged or launched. User cache state is only signed input here.
func prepareApply(cfg releaseConfig, info runtimeInfo, store stateStore, incoming state, id string) (*update.Checker, *update.Candidate, error) {
	if incoming.Schema != 1 || !hexID(incoming.InstallationID, 64) || !hexID(id, 64) {
		return nil, nil, errState
	}
	prior, err := store.Load()
	if err != nil {
		return nil, nil, errState
	}
	floor := update.Floor{Version: cfg.Version, Build: cfg.Build}
	if prior != nil {
		if prior.Schema != 1 {
			return nil, nil, errState
		}
		floor = prior.Floor
	}
	config, err := cfg.checkerConfig(info, incoming.InstallationID)
	if err != nil {
		return nil, nil, errRelease
	}
	c, err := update.NewChecker(config)
	if err != nil {
		return nil, nil, errRelease
	}
	r, err := c.Verify(incoming.Manifest, floor)
	if err != nil || r.Candidate == nil || r.Candidate.ID() != id || r.Sequence != incoming.Floor.Sequence || r.ManifestSHA256 != incoming.Floor.ManifestSHA256 {
		return nil, nil, errCandidate
	}
	floor.Sequence = r.Sequence
	floor.ManifestSHA256 = r.ManifestSHA256
	if err := store.Save(state{Schema: 1, InstallationID: incoming.InstallationID, Floor: floor, Manifest: incoming.Manifest}); err != nil {
		return nil, nil, errState
	}
	return c, r.Candidate, nil
}

func newEngine(cfg releaseConfig, info runtimeInfo, store stateStore) (*engine, error) {
	s, err := store.Load()
	if err != nil {
		return nil, errState
	}
	if s != nil && (len(s.Manifest) == 0 || s.Floor.Sequence == 0 || !hexID(s.Floor.ManifestSHA256, 64)) {
		return nil, errState
	}
	if s == nil {
		var id [32]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, errState
		}
		s = &state{Schema: 1, InstallationID: hex.EncodeToString(id[:]), Floor: update.Floor{Version: cfg.Version, Build: cfg.Build}}
	}
	if s.Schema != 1 || !hexID(s.InstallationID, 64) || (len(s.Manifest) == 0) != (s.Floor.Sequence == 0) {
		return nil, errState
	}
	config, err := cfg.checkerConfig(info, s.InstallationID)
	if err != nil {
		return nil, errRelease
	}
	c, err := update.NewChecker(config)
	if err != nil {
		return nil, errRelease
	}
	return &engine{cfg: cfg, store: store, state: *s, verifier: c, network: info.Network, fetch: func(ctx context.Context, url string) ([]byte, error) {
		return fetchManifest(ctx, url, config.DialContext)
	}}, nil
}

func (e *engine) readStartupPolicy() (startupPolicy, error) {
	if len(e.state.Manifest) == 0 {
		if e.state.Floor.Sequence != 0 || e.state.Floor.ManifestSHA256 != "" || e.state.Floor.Version != e.cfg.Version || e.state.Floor.Build != e.cfg.Build {
			return startupPolicy{}, errState
		}
		return startupPolicy{State: "no_history"}, nil
	}
	p, err := e.verifier.VerifyStartupPolicy(e.state.Manifest, e.state.Floor)
	if err != nil {
		return startupPolicy{}, errState
	}
	return startupPolicy{State: "verified", MinimumVersion: p.MinimumVersion, Unsupported: p.Unsupported, ManifestExpired: p.ManifestExpired}, nil
}
func (e *engine) execute(ctx context.Context, r request, progress func(snapshot)) (snapshot, error) {
	if r.Op == "check" {
		return e.check(ctx)
	}
	result, err := e.verifier.Verify(e.state.Manifest, e.state.Floor)
	if err != nil {
		return snapshot{}, errState
	}
	c := result.Candidate
	if c == nil || c.ID() != r.CandidateID {
		return snapshot{}, errCandidate
	}
	s := describe(result)
	path := filepath.Join(e.store.Dir(), c.ID()+".exe")
	switch r.Op {
	case "download":
		if !r.AllowMetered && e.network == nil {
			return snapshot{}, errWaitingNetwork
		}
		var lastProgress time.Time
		opts := update.DownloadOptions{Progress: func(p update.Progress) {
			if progress != nil {
				if time.Since(lastProgress) < 250*time.Millisecond && p.Downloaded != p.Total {
					return
				}
				lastProgress = time.Now()
				v := s
				v.Notes = ""
				v.State = "downloading"
				v.Downloaded = p.Downloaded
				v.Remaining = p.Remaining
				progress(v)
			}
		}}
		if r.AllowMetered {
			opts.ConsentPackageID = c.ID()
		}
		if e.network != nil {
			e.network.beginDownload(r.AllowMetered)
			opts.Prepare = e.network.prepare
			opts.RouteValid = e.network.routeValid
			opts.Network = e.network.network
		}
		_, err := e.verifier.Download(ctx, c, e.store.Dir(), opts)
		if errors.Is(err, update.ErrWaitingNetwork) {
			return snapshot{}, errWaitingNetwork
		}
		if err != nil {
			return snapshot{}, errDownload
		}
		if e.verifier.Revalidate(ctx, c, path) != nil {
			return snapshot{}, errPackage
		}
		s.State = "ready"
		s.Downloaded = s.Size
		s.Remaining = 0
		return s, nil
	case "launch":
		if !r.InstallConsent {
			return snapshot{}, errConsent
		}
		if e.verifier.Revalidate(ctx, c, path) != nil {
			return snapshot{}, errPackage
		}
		if err := e.store.Handoff(c.ID(), e.state); err != nil {
			return snapshot{}, errState
		}
		if e.elevate == nil {
			return snapshot{}, errLaunch
		}
		if err := e.elevate(ctx, c.ID(), e.store.Dir()); err != nil {
			return snapshot{}, err
		}
		s.State = "launch_requested"
		return s, nil
	default:
		return snapshot{}, errRequest
	}
}
func (e *engine) check(ctx context.Context) (snapshot, error) {
	for _, url := range e.cfg.ManifestURLs {
		raw, err := e.fetch(ctx, url)
		if err != nil {
			continue
		}
		r, err := e.verifier.Verify(raw, e.state.Floor)
		if err != nil {
			continue
		}
		next := e.state
		next.Manifest = append([]byte(nil), raw...)
		next.Floor.Sequence = r.Sequence
		next.Floor.ManifestSHA256 = r.ManifestSHA256
		if err := e.store.Save(next); err != nil {
			return snapshot{}, errState
		}
		e.state = next
		return e.inspect(ctx, r, false), nil
	}
	if len(e.state.Manifest) > 0 {
		if r, err := e.verifier.Verify(e.state.Manifest, e.state.Floor); err == nil {
			return e.inspect(ctx, r, true), nil
		}
	}
	return snapshot{}, errCheck
}
func (e *engine) inspect(ctx context.Context, r update.Result, cached bool) snapshot {
	s := describe(r)
	s.FromCache = cached
	if c := r.Candidate; c != nil && e.verifier.Revalidate(ctx, c, filepath.Join(e.store.Dir(), c.ID()+".exe")) == nil {
		s.State = "ready"
		s.Downloaded = s.Size
		s.Remaining = 0
	}
	return s
}
func describe(r update.Result) snapshot {
	s := snapshot{State: "up_to_date", MinimumVersion: r.MinimumVersion, Unsupported: r.Unsupported}
	if c := r.Candidate; c != nil {
		p := c.Package()
		s.State = "available"
		s.CandidateID = c.ID()
		s.Version = p.Version
		s.Build = p.Build
		s.Size = p.Size
		s.Remaining = p.Size
		s.Notes = p.Notes
	}
	return s
}
func fetchManifest(ctx context.Context, url string, dial func(context.Context, string, string) (net.Conn, error)) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.DialContext = dial
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errCheck }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	encoding := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != 200 || resp.ContentLength > update.MaxManifestBytes || encoding != "" && encoding != "identity" {
		return nil, errCheck
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, update.MaxManifestBytes+1))
	if err != nil || len(b) > update.MaxManifestBytes {
		return nil, errCheck
	}
	return b, nil
}
