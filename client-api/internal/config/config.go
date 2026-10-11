package config

import (
	"bluegrass/client-api/internal/dbpool"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"runtime"
	"time"
)

type Config struct {
	Listen                    string        `json:"listen"`
	RequireHTTPS              bool          `json:"require_https"`
	TrustedProxyCIDRs         []string      `json:"trusted_proxy_cidrs"`
	TokenKey                  string        `json:"token_key"`
	Source                    dbpool.Config `json:"source"`
	Sessions                  dbpool.Config `json:"sessions"`
	SessionPrefix             string        `json:"session_prefix"`
	MaxInflight               int           `json:"max_inflight"`
	HashWorkers               int           `json:"hash_workers"`
	HashWaitSeconds           int           `json:"hash_wait_seconds"`
	RequestTimeoutSeconds     int           `json:"request_timeout_seconds"`
	QueryTimeoutSeconds       int           `json:"query_timeout_seconds"`
	TransactionTimeoutSeconds int           `json:"transaction_timeout_seconds"`
	PhoneRefreshSeconds       int           `json:"phone_refresh_seconds"`
	MaxPhoneEntries           int           `json:"max_phone_entries"`
	LoginPerMinute            int           `json:"login_per_minute"`
	RefreshPerMinute          int           `json:"refresh_per_minute"`
	ReadPerMinute             int           `json:"read_per_minute"`
	AccessTTLSeconds          int           `json:"access_ttl_seconds"`
	RefreshIdleDays           int           `json:"refresh_idle_days"`
	RetrySeconds              int           `json:"retry_seconds"`
}

func Load(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8087", RequireHTTPS: true, TrustedProxyCIDRs: []string{"127.0.0.1/32", "::1/128"}, SessionPrefix: "bg_client_", MaxInflight: 128, HashWorkers: 8, HashWaitSeconds: 12, RequestTimeoutSeconds: 15, QueryTimeoutSeconds: 2, TransactionTimeoutSeconds: 3, PhoneRefreshSeconds: 30, MaxPhoneEntries: 250000, LoginPerMinute: 120, RefreshPerMinute: 240, ReadPerMinute: 120, AccessTTLSeconds: 1800, RefreshIdleDays: 180, RetrySeconds: 120}
	c.Source = dbpool.Config{Port: 3306, MaxOpen: 16, MaxIdle: 8}
	c.Sessions = c.Source
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot open configuration file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 64*1024 {
		return c, errors.New("invalid configuration file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return c, errors.New("configuration file must be private (mode 0600)")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 64*1024))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, errors.New("invalid configuration JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return c, errors.New("configuration must contain one JSON document")
	}
	key, e := base64.StdEncoding.Strict().DecodeString(c.TokenKey)
	if e != nil || len(key) != 32 {
		return c, errors.New("token_key must be 32 random bytes encoded as base64")
	}
	host, port, e := net.SplitHostPort(c.Listen)
	if e != nil || port == "" {
		return c, errors.New("invalid listen address")
	}
	if !c.RequireHTTPS {
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return c, errors.New("plaintext testing listener must be loopback")
		}
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`).MatchString(c.SessionPrefix) {
		return c, errors.New("invalid session table prefix")
	}
	for _, cidr := range c.TrustedProxyCIDRs {
		if _, _, e = net.ParseCIDR(cidr); e != nil {
			return c, errors.New("invalid trusted proxy CIDR")
		}
	}
	if c.HashWorkers < 1 || c.HashWorkers > 32 || c.MaxInflight < c.HashWorkers || c.MaxInflight > 2048 || c.HashWaitSeconds < 1 || c.HashWaitSeconds > 30 || c.RequestTimeoutSeconds < c.HashWaitSeconds || c.RequestTimeoutSeconds > 60 || c.QueryTimeoutSeconds < 1 || c.QueryTimeoutSeconds > 10 || c.TransactionTimeoutSeconds < 1 || c.TransactionTimeoutSeconds > 10 || c.PhoneRefreshSeconds < 10 || c.PhoneRefreshSeconds > 3600 || c.MaxPhoneEntries < 1 || c.MaxPhoneEntries > 2000000 || c.LoginPerMinute < 1 || c.RefreshPerMinute < 1 || c.AccessTTLSeconds < 60 || c.AccessTTLSeconds > 3600 || c.RefreshIdleDays < 1 || c.RefreshIdleDays > 365 || c.RetrySeconds < 10 || c.RetrySeconds > 600 {
		return c, errors.New("invalid concurrency or session budget")
	}
	if c.Source.MaxOpen+c.Sessions.MaxOpen > 128 {
		return c, errors.New("combined database pool exceeds 128-connection per-instance ceiling")
	}
	if c.ReadPerMinute < 1 {
		return c, errors.New("read_per_minute must be positive")
	}
	for _, pool := range []dbpool.Config{c.Source, c.Sessions} {
		db, e := dbpool.Open(pool, true, time.UTC)
		if e != nil {
			return c, e
		}
		db.Close()
	}
	return c, nil
}
