package main

import (
	"context"
	"crypto/x509"
	"io"
	"os"
	"path/filepath"
	"time"
)

const androidCADir = "/system/etc/security/cacerts"

func appendAndroidRoots(ctx context.Context, pool *x509.CertPool, directory string) (int, string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, "android_ca_unavailable"
	}
	if len(entries) > 1024 {
		return 0, "android_ca_limit"
	}
	loaded, code := 0, ""
	for _, entry := range entries {
		if ctx.Err() != nil {
			return loaded, "ca_timeout"
		}
		if entry.IsDir() {
			continue
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			code = "android_ca_partial"
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			_ = file.Close()
			code = "android_ca_partial"
			continue
		}
		pem, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		_ = file.Close()
		if err != nil || len(pem) > 1<<20 || !pool.AppendCertsFromPEM(pem) {
			code = "android_ca_partial"
			continue
		}
		loaded++
	}
	if loaded == 0 && code == "" {
		code = "android_ca_empty"
	}
	return loaded, code
}

func loadTrust(parent context.Context) (*x509.CertPool, caResult) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	type trust struct {
		pool *x509.CertPool
		info caResult
	}
	ready := make(chan trust, 1)
	go func() {
		pool, err := x509.SystemCertPool()
		info := caResult{SystemPoolLoaded: err == nil && pool != nil}
		if pool == nil {
			pool = x509.NewCertPool()
		}
		info.AndroidCAFilesLoaded, info.Error = appendAndroidRoots(ctx, pool, androidCADir)
		if !info.SystemPoolLoaded && info.AndroidCAFilesLoaded == 0 {
			pool = nil
			info.Error = "ca_unavailable"
		}
		ready <- trust{pool, info}
	}()
	select {
	case value := <-ready:
		return value.pool, value.info
	case <-ctx.Done():
		return nil, caResult{Error: "ca_timeout"}
	}
}
