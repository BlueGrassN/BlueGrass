// Package update verifies and prepares Windows updates. It never installs them.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Keys map[string]ed25519.PublicKey
	// DialContext optionally supplies the trusted native control-plane dialer.
	// URL allowlisting, redirects, TLS hostname/certificate validation and all
	// update signatures remain enforced here. Never accept this from UI input.
	DialContext func(context.Context, string, string) (net.Conn, error)
	// Publisher is the certificate SHA-256 returned by TrustedPublisher on the
	// installed, trusted product executable. Never derive it from a manifest.
	Publisher                                                string
	AllowedHosts                                             []string
	Channel, Arch, OSVersion, CurrentVersion, InstallationID string
	CurrentBuild                                             uint64
}

type Manifest struct {
	Schema    uint64    `json:"schema"`
	Sequence  uint64    `json:"sequence"`
	Published string    `json:"published"`
	Expires   string    `json:"expires"`
	Releases  []Package `json:"releases"`
}

type Package struct {
	Version        string   `json:"version"`
	Build          uint64   `json:"build"`
	MinimumVersion string   `json:"minimum_version"`
	Channel        string   `json:"channel"`
	Platform       string   `json:"platform"`
	Arch           string   `json:"arch"`
	MinimumOS      string   `json:"minimum_os"`
	PackageID      string   `json:"package_id,omitempty"`
	URLs           []string `json:"urls"`
	SHA256         string   `json:"sha256"`
	Size           int64    `json:"size"`
	Force          bool     `json:"force"`
	Rollout        uint64   `json:"rollout"`
	Notes          string   `json:"notes"`
	Published      string   `json:"published"`
}

// Floor must be persisted in protected storage by the caller, partitioned by
// channel/platform/architecture. Sequence/digest advance after verification;
// Version/Build advance only after a successful installation. Never reset on error.
type Floor struct {
	Sequence       uint64 `json:"sequence"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Version        string `json:"version"`
	Build          uint64 `json:"build"`
}

type Result struct {
	Sequence       uint64
	ManifestSHA256 string
	MinimumVersion string
	Unsupported    bool
	Candidate      *Candidate
}

// StartupPolicy restores only a previously accepted signed minimum version.
// It deliberately carries no candidate or package/install authority.
type StartupPolicy struct {
	MinimumVersion  string
	Unsupported     bool
	ManifestExpired bool
}

type verifiedManifest struct {
	Result
	expired bool
}

type Candidate struct {
	owner        *Checker
	pkg          Package
	id           string
	expires      time.Time
	sequence     uint64
	manifestHash string
}

func (c *Candidate) Package() Package {
	p := c.pkg
	p.URLs = append([]string(nil), p.URLs...)
	return p
}
func (c *Candidate) ID() string { return c.id }

type Checker struct {
	config          Config
	hosts           map[string]bool
	client          *http.Client
	now             func() time.Time
	verifyPublisher func(context.Context, *artifactFile, string) error
	mu              sync.Mutex
	floor           Floor
}

func NewChecker(config Config) (*Checker, error) {
	if len(config.Keys) < 1 || len(config.Keys) > 2 {
		return nil, errors.New("one or two embedded manifest keys required")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, key := range config.Keys {
		if len(id) == 0 || len(id) > 32 || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("invalid manifest key")
		}
		for _, r := range id {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return nil, errors.New("invalid key ID")
			}
		}
		keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	config.Keys = keys
	if !validHash(config.Publisher) {
		return nil, errors.New("trusted publisher certificate fingerprint required")
	}
	if config.Channel != "stable" && config.Channel != "beta" {
		return nil, errors.New("invalid update channel")
	}
	if config.Arch != "amd64" && config.Arch != "arm64" {
		return nil, errors.New("unsupported Windows architecture")
	}
	if _, err := parseVersion(config.CurrentVersion, false); err != nil {
		return nil, err
	}
	osv, err := parseVersion(config.OSVersion, true)
	if err != nil || osv.numbers[0] < 10 {
		return nil, errors.New("unsupported OS version")
	}
	if config.CurrentBuild == 0 || config.CurrentBuild > 9007199254740991 || len(config.InstallationID) == 0 || len(config.InstallationID) > 256 {
		return nil, errors.New("invalid installed build or installation ID")
	}
	hosts := map[string]bool{}
	for _, host := range config.AllowedHosts {
		if host != strings.ToLower(host) || strings.ContainsAny(host, "/@?#\\ \t\r\n") || host == "" {
			return nil, errors.New("invalid HTTPS allowlist")
		}
		u, err := url.Parse("https://" + host + "/")
		if err != nil || u.Host != host || u.Hostname() == "" {
			return nil, errors.New("invalid HTTPS allowlist")
		}
		for _, r := range host {
			if r > 127 {
				return nil, errors.New("allowlist must use ASCII host names")
			}
		}
		hosts[host] = true
	}
	if len(hosts) == 0 || len(hosts) > 16 {
		return nil, errors.New("HTTPS allowlist required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 15 * time.Second
	if config.DialContext != nil {
		transport.DialContext = config.DialContext
	}
	return &Checker{config: config, hosts: hosts, now: time.Now, verifyPublisher: verifyArtifactPublisher,
		client: &http.Client{Transport: transport, Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("update redirects forbidden") }}}, nil
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Checker) trustedURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || !c.hosts[u.Host] || u.Hostname() == "" || u.Path == "" || strings.Contains(raw, "\\") {
		return errors.New("untrusted update URL")
	}
	return nil
}

func (c *Checker) request(ctx context.Context, raw string) (*http.Request, error) {
	if err := c.trustedURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err == nil {
		req.Header.Set("Accept-Encoding", "identity")
	}
	return req, err
}

func (c *Checker) Check(ctx context.Context, manifestURL string, floor Floor) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := c.request(ctx, manifestURL)
	if err != nil {
		return Result{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return Result{}, errors.New("manifest request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !identityEncoding(resp) || resp.ContentLength > MaxManifestBytes {
		return Result{}, errors.New("invalid manifest response")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestBytes+1))
	if err != nil {
		return Result{}, errors.New("manifest read failed")
	}
	return c.Verify(raw, floor)
}

func identityEncoding(r *http.Response) bool {
	return r.Header.Get("Content-Encoding") == "" || r.Header.Get("Content-Encoding") == "identity"
}

func (c *Checker) Verify(raw []byte, floor Floor) (Result, error) {
	r, err := c.verify(raw, floor, false)
	return r.Result, err
}

// VerifyStartupPolicy is offline and revalidates the complete signed schema.
// Only expiry is relaxed, and only for the exact nonzero persisted sequence and
// digest. It neither advances the checker floor nor creates candidate authority.
func (c *Checker) VerifyStartupPolicy(raw []byte, floor Floor) (StartupPolicy, error) {
	r, err := c.verify(raw, floor, true)
	if err != nil {
		return StartupPolicy{}, err
	}
	return StartupPolicy{MinimumVersion: r.MinimumVersion, Unsupported: r.Unsupported, ManifestExpired: r.expired}, nil
}

func (c *Checker) verify(raw []byte, floor Floor, policyOnly bool) (result verifiedManifest, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if err != nil {
			result = verifiedManifest{}
		}
	}()
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return result, err
	}
	var env struct {
		KeyID     string          `json:"key_id"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = decodeObject(canonical, &env, "key_id", "payload", "signature"); err != nil {
		return result, err
	}
	key, ok := c.config.Keys[env.KeyID]
	if !ok {
		return result, errors.New("unknown manifest signing key")
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(env.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, append([]byte("BlueGrass update manifest v1\n"+env.KeyID+"\n"), env.Payload...), sig) {
		return result, errors.New("invalid manifest signature")
	}
	var m Manifest
	if err = decodeObject(env.Payload, &m, "schema", "sequence", "published", "expires", "releases"); err != nil {
		return result, err
	}
	if m.Schema != 1 || m.Sequence == 0 || len(m.Releases) == 0 || len(m.Releases) > 32 {
		return result, errors.New("unsupported manifest schema or size")
	}
	published, err := parseTime(m.Published)
	if err != nil {
		return result, err
	}
	expires, err := parseTime(m.Expires)
	if err != nil {
		return result, err
	}
	now := c.now()
	result.expired = !expires.After(now)
	if published.After(now.Add(5*time.Minute)) || !policyOnly && result.expired || !expires.After(published) || expires.Sub(published) > 30*24*time.Hour {
		return result, errors.New("manifest outside validity window")
	}
	digest := sha256.Sum256(env.Payload)
	hash := hex.EncodeToString(digest[:])
	if policyOnly && (floor.Sequence == 0 || floor.Sequence != m.Sequence || floor.ManifestSHA256 != hash) {
		return result, errors.New("startup policy requires exact accepted manifest floor")
	}
	for _, f := range []Floor{floor, c.floor} {
		if f.Sequence > 0 && !validHash(f.ManifestSHA256) || f.Sequence > m.Sequence || f.Sequence == m.Sequence && f.ManifestSHA256 != hash {
			return result, errors.New("manifest rollback or sequence conflict")
		}
		if (f.Version == "") != (f.Build == 0) {
			return result, errors.New("incomplete installed version floor")
		}
		if f.Version != "" {
			if _, err := parseVersion(f.Version, false); err != nil {
				return result, err
			}
		}
	}
	var fields struct {
		Releases []json.RawMessage `json:"releases"`
	}
	_ = json.Unmarshal(env.Payload, &fields)
	seen := map[string]bool{}
	current, _ := parseVersion(c.config.CurrentVersion, false)
	osv, _ := parseVersion(c.config.OSVersion, true)
	for i, p := range m.Releases {
		var dummy Package
		if err := decodeObject(fields.Releases[i], &dummy, "version", "build", "minimum_version", "channel", "platform", "arch", "minimum_os", "urls", "sha256", "size", "force", "rollout", "notes", "published"); err != nil {
			return result, err
		}
		pv, err := c.validatePackage(p, published)
		if err != nil {
			return result, err
		}
		identity := fmt.Sprintf("%s/%s/%s", p.Platform, p.Arch, p.Channel)
		if seen[identity] {
			return result, errors.New("duplicate release")
		}
		seen[identity] = true
		if p.Platform != "windows" || p.Arch != c.config.Arch || p.Channel != c.config.Channel {
			continue
		}
		min, _ := parseVersion(p.MinimumVersion, false)
		result.MinimumVersion = p.MinimumVersion
		result.Unsupported = compareVersion(min, current) > 0
		if policyOnly {
			continue
		}
		minOS, _ := parseVersion(p.MinimumOS, true)
		if compareVersion(osv, minOS) < 0 || p.Build <= c.config.CurrentBuild || compareVersion(pv, current) <= 0 {
			continue
		}
		old := false
		for _, f := range []Floor{floor, c.floor} {
			if p.Build <= f.Build {
				old = true
			}
			if f.Version != "" {
				fv, _ := parseVersion(f.Version, false)
				if compareVersion(pv, fv) <= 0 {
					old = true
				}
			}
		}
		if old {
			continue
		}
		if rolloutBucket(c.config.InstallationID) >= p.Rollout {
			continue
		}
		encoded, _ := json.Marshal(p)
		id := sha256.Sum256(encoded)
		result.Candidate = &Candidate{owner: c, pkg: p, id: hex.EncodeToString(id[:]), expires: expires, sequence: m.Sequence, manifestHash: hash}
	}
	result.Sequence = m.Sequence
	result.ManifestSHA256 = hash
	if result.MinimumVersion == "" {
		result.MinimumVersion = c.config.CurrentVersion
	}
	if policyOnly {
		return result, nil
	}
	if floor.Build > c.floor.Build {
		c.floor.Build = floor.Build
	}
	if floor.Version != "" {
		incoming, _ := parseVersion(floor.Version, false)
		prior, _ := parseVersion(c.floor.Version, false)
		if c.floor.Version == "" || compareVersion(incoming, prior) > 0 {
			c.floor.Version = floor.Version
		}
	}
	c.floor.Sequence, c.floor.ManifestSHA256 = m.Sequence, hash
	return result, nil
}

func (c *Checker) validatePackage(p Package, published time.Time) (version, error) {
	v, err := parseVersion(p.Version, false)
	if err != nil {
		return v, err
	}
	min, err := parseVersion(p.MinimumVersion, false)
	if err != nil || compareVersion(min, v) > 0 {
		return v, errors.New("invalid minimum version")
	}
	if _, err := parseVersion(p.MinimumOS, true); err != nil {
		return v, err
	}
	if p.Platform != "windows" && p.Platform != "android" && p.Platform != "windows7" {
		return v, errors.New("unknown platform")
	}
	if p.Platform == "android" && p.PackageID != "com.bluegrass.client" || p.Platform != "android" && p.PackageID != "" {
		return v, errors.New("invalid package identity")
	}
	if p.Arch != "amd64" && p.Arch != "arm64" && p.Arch != "arm" && p.Arch != "universal" {
		return v, errors.New("unknown architecture")
	}
	if p.Channel != "stable" && p.Channel != "beta" || p.Build == 0 || p.Rollout > 100 || p.Size <= 0 || p.Size > MaxPackageBytes || !validHash(p.SHA256) || len(p.Notes) > 16384 {
		return v, errors.New("invalid release metadata")
	}
	if len(p.URLs) < 1 || len(p.URLs) > 4 {
		return v, errors.New("one to four mirrors required")
	}
	for _, u := range p.URLs {
		if err := c.trustedURL(u); err != nil {
			return v, err
		}
	}
	t, err := parseTime(p.Published)
	if err != nil || t.After(published) {
		return v, errors.New("invalid release publication time")
	}
	return v, nil
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.Format(time.RFC3339) != s || !strings.HasSuffix(s, "Z") {
		return time.Time{}, errors.New("expected canonical UTC timestamp")
	}
	return t, nil
}

func rolloutBucket(id string) uint64 {
	sum := sha256.Sum256([]byte(id))
	var n uint64
	for _, b := range sum {
		n = (n*256 + uint64(b)) % 100
	}
	return n
}

func (c *Checker) validateCandidate(candidate *Candidate) error {
	if candidate == nil || candidate.owner != c || !candidate.expires.After(c.now()) {
		return errors.New("untrusted or expired update candidate")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.floor.Sequence != candidate.sequence || c.floor.ManifestSHA256 != candidate.manifestHash {
		return errors.New("superseded update candidate")
	}
	return nil
}
