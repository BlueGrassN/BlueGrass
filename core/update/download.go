package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var ErrWaitingNetwork = errors.New("waiting for permission to download on this network")

type NetworkCost uint8

const (
	NetworkUnknown NetworkCost = iota
	NetworkUnmetered
	NetworkMetered
	NetworkCellular
)

type Progress struct{ Downloaded, Total, Remaining int64 }
type DownloadOptions struct {
	// Prepare selects/verifies the physical route for this signed mirror before
	// cost is checked or HTTP begins. It must not transfer package bytes.
	Prepare func(context.Context, string) error
	// RouteValid is independent of cost consent: route or interface changes must
	// pause this attempt, including downloads with per-package metered consent.
	RouteValid func(context.Context) error
	// Network must promptly report the underlying route's cost, honor context,
	// and be safe for concurrent calls. A missing/error/unknown result waits.
	Network func(context.Context) (NetworkCost, error)
	// Consent is valid only for this Candidate.ID; checking updates is not consent.
	ConsentPackageID string
	Progress         func(Progress)
}
type Ready struct {
	Path, PackageID string
	Size            int64
}

// Download prepares a package in a caller-owned, ACL-protected absolute cache
// directory. No user tokens belong here. One attempt per signed mirror (<=4),
// 15-minute total deadline, 30-second read-idle limit; resume by calling again.
// Cancellation means user pause/cancel unless ErrWaitingNetwork is returned.
// No scheduler, UI, maintenance, or installation side effects are performed.
func (c *Checker) Download(ctx context.Context, candidate *Candidate, cacheDir string, opts DownloadOptions) (Ready, error) {
	if err := ctx.Err(); err != nil {
		return Ready{}, err
	}
	if err := c.validateCandidate(candidate); err != nil {
		return Ready{}, err
	}
	if !filepath.IsAbs(cacheDir) {
		return Ready{}, errors.New("absolute protected cache directory required")
	}
	info, err := os.Lstat(cacheDir)
	if err != nil {
		return Ready{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Ready{}, errors.New("cache directory must not be a link")
	}
	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return Ready{}, err
	}
	defer root.Close()
	id := candidate.ID()
	partial := id + ".part"
	final := id + ".exe"
	for _, name := range []string{partial, final, id + ".lock"} {
		if err := regularOrAbsent(root, name); err != nil {
			return Ready{}, err
		}
	}
	lock, err := lockCache(filepath.Join(cacheDir, id+".lock"))
	if err != nil {
		return Ready{}, errors.New("update cache is busy")
	}
	defer lock()
	ready := Ready{Path: filepath.Join(cacheDir, final), PackageID: id, Size: candidate.pkg.Size}
	if _, err := root.Stat(final); err == nil {
		if err := c.Revalidate(ctx, candidate, ready.Path); err == nil {
			return ready, nil
		}
		if err := root.Remove(final); err != nil {
			return Ready{}, err
		}
	}
	ctx, deadline := context.WithTimeout(ctx, 15*time.Minute)
	defer deadline()
	var lastErr error
	for _, mirror := range candidate.pkg.URLs {
		if err := ctx.Err(); err != nil {
			return Ready{}, context.Cause(ctx)
		}
		if err := c.validateCandidate(candidate); err != nil {
			return Ready{}, err
		}
		lastErr = c.transferGuarded(ctx, candidate, root, partial, mirror, opts)
		if ctx.Err() != nil {
			return Ready{}, context.Cause(ctx)
		}
		if errors.Is(lastErr, ErrWaitingNetwork) {
			return Ready{}, lastErr
		}
		if lastErr != nil {
			continue
		}
		lastErr = c.Revalidate(ctx, candidate, filepath.Join(cacheDir, partial))
		if lastErr != nil {
			if err := root.Remove(partial); err != nil {
				return Ready{}, err
			}
			continue
		}
		if err := root.Rename(partial, final); err != nil {
			return Ready{}, err
		}
		if err := c.validateCandidate(candidate); err != nil {
			return Ready{}, err
		}
		return ready, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no update download mirrors")
	}
	return Ready{}, lastErr
}

func (c *Checker) transferGuarded(ctx context.Context, candidate *Candidate, root *os.Root, partial, mirror string, opts DownloadOptions) error {
	if opts.Prepare != nil {
		if err := opts.Prepare(ctx, mirror); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if err := networkAllowed(ctx, candidate, opts); err != nil {
		return err
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := networkAllowed(ctx, candidate, opts); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-watchDone }()
	err := c.transfer(ctx, candidate, root, partial, mirror, opts)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return err
}

func regularOrAbsent(root *os.Root, name string) error {
	i, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !i.Mode().IsRegular() {
		return errors.New("update cache entry must be a regular file")
	}
	return nil
}

func networkAllowed(ctx context.Context, candidate *Candidate, opts DownloadOptions) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if opts.RouteValid != nil && opts.RouteValid(ctx) != nil {
		return ErrWaitingNetwork
	}
	if opts.ConsentPackageID == candidate.ID() {
		return nil
	}
	if opts.Network == nil {
		return ErrWaitingNetwork
	}
	cost, err := opts.Network(ctx)
	if err != nil || cost != NetworkUnmetered {
		return ErrWaitingNetwork
	}
	return nil
}

func (c *Checker) transfer(ctx context.Context, candidate *Candidate, root *os.Root, partial, mirror string, opts DownloadOptions) error {
	if err := regularOrAbsent(root, partial); err != nil {
		return err
	}
	f, err := root.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	size := candidate.pkg.Size
	if offset > size {
		if err := f.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if offset == size {
		return f.Sync()
	}
	if err := networkAllowed(ctx, candidate, opts); err != nil {
		return err
	}
	req, err := c.request(ctx, mirror)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrWaitingNetwork) {
			return ErrWaitingNetwork
		}
		return errors.New("package request failed")
	}
	defer resp.Body.Close()
	if !identityEncoding(resp) {
		return errors.New("encoded package response rejected")
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if resp.Header.Get("Content-Range") != "" {
			return errors.New("unexpected content range")
		}
		if err := f.Truncate(0); err != nil {
			return err
		}
		offset = 0
	case http.StatusPartialContent:
		want := fmt.Sprintf("bytes %d-%d/%d", offset, size-1, size)
		if offset == 0 || resp.Header.Get("Content-Range") != want {
			return errors.New("invalid resume content range")
		}
	default:
		return errors.New("invalid package HTTP status")
	}
	if resp.ContentLength >= 0 && resp.ContentLength != size-offset {
		return errors.New("package response size mismatch")
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	readctx, stop := context.WithCancel(ctx)
	defer stop()
	idle := time.AfterFunc(30*time.Second, func() { stop(); _ = resp.Body.Close() })
	defer idle.Stop()
	reader := io.LimitReader(resp.Body, size-offset+1)
	buf := make([]byte, 32*1024)
	if opts.Progress != nil {
		opts.Progress(Progress{offset, size, size - offset})
	}
	for {
		if err := networkAllowed(readctx, candidate, opts); err != nil {
			return err
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			if err := networkAllowed(readctx, candidate, opts); err != nil {
				return err
			}
			if int64(n) > size-offset {
				return errors.New("package exceeds signed size")
			}
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			offset += int64(n)
			idle.Reset(30 * time.Second)
			if opts.Progress != nil {
				opts.Progress(Progress{offset, size, size - offset})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return errors.New("package download interrupted")
		}
		if readctx.Err() != nil {
			return errors.New("package read idle timeout")
		}
	}
	if offset != size {
		return errors.New("incomplete package")
	}
	return f.Sync()
}

// Revalidate must run after restart and again before explicit installation.
// Windows opens a handle that denies writes/deletion for the complete hash and
// signer verification. Main must protect staging/launch against path replacement
// after this method returns, especially across an elevation boundary.
func (c *Checker) Revalidate(ctx context.Context, candidate *Candidate, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.validateCandidate(candidate); err != nil {
		return err
	}
	f, err := openArtifact(path)
	if err != nil {
		return err
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return err
	}
	if !i.Mode().IsRegular() || i.Size() != candidate.pkg.Size {
		return errors.New("package size mismatch")
	}
	hash := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != candidate.pkg.SHA256 {
		return errors.New("package SHA-256 mismatch")
	}
	if err := c.verifyPublisher(ctx, f, c.config.Publisher); err != nil {
		return err
	}
	return c.validateCandidate(candidate)
}
