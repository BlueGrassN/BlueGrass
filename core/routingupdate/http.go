// Package routingupdate manages optional downloaded rule snapshots. The bundled
// routingpolicy.Load contract remains offline and unchanged for other clients.
package routingupdate

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const releaseURL = "https://api.github.com/repos/Loyalsoldier/v2ray-rules-dat/releases/latest"
const maxSourceBytes int64 = 32 << 20

func assetURL(tag, name string) string {
	return "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/" + tag + "/" + name
}

// AllowedURL is shared with the Android proxy dialer. Signed redirect queries
// are accepted but are never exposed in status, logs or persisted metadata.
func AllowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Fragment != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func NewHTTPClient(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	if dial == nil {
		dial = (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout: 30 * time.Second, MaxIdleConns: 4,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !AllowedURL(req.URL.String()) {
				return errors.New("UPDATE_FAILED")
			}
			return nil
		},
	}
}

func fetch(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, error) {
	if !AllowedURL(raw) {
		return nil, errors.New("UPDATE_FAILED")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "BlueGrass-Routing-Updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	if raw != releaseURL {
		req.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > limit {
		return nil, errors.New("UPDATE_FAILED")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("UPDATE_FAILED")
	}
	return data, nil
}
