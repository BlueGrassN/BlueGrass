// Package cmdupdate is the fixed-config, one-request update helper boundary.
// Run and Apply accept no release trust configuration. TrustedHosts exposes
// only the built-in service DNS allowlist.
package cmdupdate

import (
	"bluegrass/native/core/update"
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// These bytes are part of the signed executable. Release engineering replaces
// this source file before building/signing, never through UI, env or sidecars.
//
//go:embed release.json
var embeddedRelease []byte

type releaseConfig struct {
	Schema       int               `json:"schema"`
	Version      string            `json:"version"`
	Build        uint64            `json:"build"`
	Channel      string            `json:"channel"`
	ManifestURLs []string          `json:"manifest_urls"`
	AllowedHosts []string          `json:"allowed_hosts"`
	Keys         map[string]string `json:"keys"`
}
type runtimeInfo struct {
	Publisher, OSVersion, Arch string
	ResolveHost                func(context.Context, string) ([]netip.Addr, error)
	Network                    *routePolicy
}

var (
	errUnconfigured    = errors.New("UPDATE_UNCONFIGURED")
	errRelease         = errors.New("UPDATE_RELEASE_INVALID")
	errRequest         = errors.New("UPDATE_INVALID_REQUEST")
	errState           = errors.New("UPDATE_STATE_INVALID")
	errBusy            = errors.New("UPDATE_BUSY")
	errTrust           = errors.New("UPDATE_TRUST_FAILED")
	errCheck           = errors.New("UPDATE_CHECK_FAILED")
	errCandidate       = errors.New("UPDATE_CANDIDATE_INVALID")
	errWaitingNetwork  = errors.New("UPDATE_WAITING_NETWORK")
	errConsent         = errors.New("UPDATE_INSTALL_CONSENT_REQUIRED")
	errDownload        = errors.New("UPDATE_DOWNLOAD_FAILED")
	errPackage         = errors.New("UPDATE_PACKAGE_INVALID")
	errLaunch          = errors.New("UPDATE_LAUNCH_FAILED")
	errLaunchCancelled = errors.New("UPDATE_LAUNCH_CANCELLED")
	errControlDNS      = errors.New("UPDATE_CONTROL_DNS_FAILED")
)

// TrustedHosts is the service's immutable update DNS whitelist, drawn from the
// same embedded release configuration as the helper. No UI/env/sidecar inputs.
// serviceipc must not import cmdupdate; winservice may safely import this API.
func TrustedHosts() []string {
	cfg, err := compiledRelease()
	if err != nil {
		return nil
	}
	return append([]string(nil), cfg.AllowedHosts...)
}

func compiledRelease() (releaseConfig, error) {
	var r releaseConfig
	if err := strictObject(embeddedRelease, &r, "schema", "version", "build", "channel", "manifest_urls", "allowed_hosts", "keys"); err != nil {
		return r, errRelease
	}
	if r.Schema != 1 {
		return r, errRelease
	}
	if r.Version == "" && r.Build == 0 && r.Channel == "" && len(r.Keys) == 0 && len(r.ManifestURLs) == 0 && len(r.AllowedHosts) == 0 {
		return r, errUnconfigured
	}
	if _, err := r.checkerConfig(runtimeInfo{Publisher: "0000000000000000000000000000000000000000000000000000000000000000", OSVersion: "10.0.19045", Arch: "amd64"}, "config-validation"); err != nil {
		return r, errRelease
	}
	return r, nil
}
func (r releaseConfig) checkerConfig(info runtimeInfo, id string) (update.Config, error) {
	c := update.Config{Publisher: info.Publisher, AllowedHosts: r.AllowedHosts, CurrentVersion: r.Version, CurrentBuild: r.Build, Channel: r.Channel, Arch: info.Arch, OSVersion: info.OSVersion, InstallationID: id, Keys: map[string]ed25519.PublicKey{}}
	c.DialContext = controlDialer(r.AllowedHosts, info.ResolveHost, nil)
	if info.Network != nil {
		c.DialContext = info.Network.dialContext
	}
	if r.Schema != 1 || len(r.ManifestURLs) < 1 || len(r.ManifestURLs) > 4 {
		return c, errRelease
	}
	for id, encoded := range r.Keys {
		b, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return c, errRelease
		}
		c.Keys[id] = ed25519.PublicKey(b)
	}
	if _, err := update.NewChecker(c); err != nil {
		return c, errRelease
	}
	for _, host := range r.AllowedHosts {
		if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, ":/\\ ") || strings.HasSuffix(host, ".") {
			return c, errRelease
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return c, errRelease
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return c, errRelease
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
					return c, errRelease
				}
			}
		}
	}
	for _, raw := range r.ManifestURLs {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Path == "" || len(raw) > 2048 {
			return c, errRelease
		}
		found := false
		for _, host := range r.AllowedHosts {
			if u.Host == host {
				found = true
			}
		}
		if !found {
			return c, errRelease
		}
	}
	return c, nil
}

func strictObject(raw []byte, dst any, allowed ...string) error {
	canonical, err := update.CanonicalJSON(raw)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil || fields == nil {
		return errRequest
	}
	for k, v := range fields {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found || string(v) == "null" {
			return errRequest
		}
	}
	return json.Unmarshal(canonical, dst)
}
