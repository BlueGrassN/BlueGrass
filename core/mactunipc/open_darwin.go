//go:build darwin

package mactunipc

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const readyFrame = `{"protocolVersion":1,"status":"OK"}`

// Open always uses a system authorization prompt for a new broker. An existing
// worker is never retargeted to another UID, PID or process birth identity.
func Open(ctx context.Context) (*Client, error) {
	if os.Getuid() == 0 || os.Geteuid() != os.Getuid() {
		return nil, ErrHelperInvalid
	}
	source, err := bundledWorker()
	if err != nil {
		return nil, err
	}
	owner, err := processIdentity(uint32(os.Getpid()))
	if err != nil || !owner.valid() {
		return nil, ErrUnavailable
	}
	command, err := InstallationCommand(source, ExpectedWorkerSHA256, owner)
	if err != nil {
		return nil, err
	}
	ctx, cancel := authorizationContext(ctx)
	defer cancel()
	conn, err := authorizeAndDial(ctx, func(ctx context.Context) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", authorizationScript, "--", command)
		// The fixed AppleScript emits one safe status code. The system handles
		// the password; stdin is nil, and neither stderr nor error text is logged.
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
		cmd.WaitDelay = 100 * time.Millisecond
		out, runErr := cmd.Output()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runErr != nil {
			return ErrUnavailable
		}
		return authorizationResult(strings.TrimSpace(string(out)))
	}, dialWorker)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, ErrAuthorizationTimeout
	}
	if errors.Is(err, context.Canceled) {
		return nil, ErrAuthorization
	}
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, timeout: FrameTimeout}, nil
}

func dialWorker(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, ErrUnavailable
		}
		if err := checkRootDirectory(canonicalRuntime); err != nil {
			return nil, ErrHelperInvalid
		}
		var s unix.Stat_t
		statErr := unix.Lstat(canonicalRuntime+"/worker.sock", &s)
		if statErr == nil && (s.Mode&unix.S_IFMT != unix.S_IFSOCK || s.Uid != 0 || !socketNoACL(canonicalRuntime+"/worker.sock")) {
			return nil, ErrHelperInvalid
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, ErrHelperInvalid
		}
		if statErr == nil {
			d := net.Dialer{Timeout: 200 * time.Millisecond}
			conn, err := d.DialContext(ctx, "unix", canonicalRuntime+"/worker.sock")
			if err == nil {
				c, ok := conn.(*net.UnixConn)
				if !ok {
					_ = conn.Close()
					return nil, ErrUnavailable
				}
				uid, _, peerErr := socketPeer(c)
				if peerErr != nil || uid != 0 {
					_ = conn.Close()
					return nil, ErrHelperInvalid
				}
				deadline := time.Now().Add(time.Second)
				if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
					deadline = dl
				}
				_ = c.SetReadDeadline(deadline)
				stop := context.AfterFunc(ctx, func() { _ = c.Close() })
				ready, readErr := ReadFrame(c)
				stop()
				if readErr != nil {
					_ = c.Close()
					return nil, ErrBusy
				}
				if string(ready) != readyFrame {
					_ = c.Close()
					return nil, ErrUnavailable
				}
				_ = c.SetDeadline(time.Time{})
				return c, nil
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ErrUnavailable
		case <-timer.C:
		}
	}
}
