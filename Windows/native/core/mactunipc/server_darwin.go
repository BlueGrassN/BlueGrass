//go:build darwin

package mactunipc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Serve serves one authorized broker for one on-demand process lifetime. It
// installs no daemon and never changes the network or removes an intent marker.
func Serve(owner Identity, run func(io.Reader, io.Writer, uint32) error) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || !owner.valid() {
		return ErrHelperInvalid
	}
	if !ownerAlive(owner) {
		return ErrUnavailable
	}
	if err := validateInstalledWorker(); err != nil {
		return err
	}
	if checkRootDirectory(canonicalRuntime) != nil || checkRootDirectory(StateDirectory) != nil {
		return ErrHelperInvalid
	}
	lock, err := rootFile(canonicalRuntime+"/worker.lock", unix.O_CREAT|unix.O_RDWR)
	if err != nil {
		return ErrHelperInvalid
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrBusy
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	intent, err := ReadIntent()
	if err != nil {
		return err
	}
	if intent != nil && intent.OwnerUID != owner.UID {
		return ErrRecovery
	}
	var stat unix.Stat_t
	if err = unix.Lstat(canonicalRuntime+"/worker.sock", &stat); err == nil {
		if stat.Mode&unix.S_IFMT != unix.S_IFSOCK || stat.Uid != 0 || !socketNoACL(canonicalRuntime+"/worker.sock") {
			return ErrHelperInvalid
		}
		if err = unix.Unlink(canonicalRuntime + "/worker.sock"); err != nil {
			return ErrHelperInvalid
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrHelperInvalid
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: canonicalRuntime + "/worker.sock", Net: "unix"})
	if err != nil {
		return ErrUnavailable
	}
	defer listener.Close()
	// Root owns both parent and socket. Connect permission alone confers no
	// authority: every accepted peer must match all kernel identity fields.
	if os.Chmod(canonicalRuntime+"/worker.sock", 0666) != nil {
		return ErrHelperInvalid
	}
	if !socketNoACL(canonicalRuntime + "/worker.sock") {
		return ErrHelperInvalid
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ownerAlive(owner) {
		if listener.SetDeadline(time.Now().Add(100*time.Millisecond)) != nil {
			return ErrUnavailable
		}
		c, err := listener.AcceptUnix()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return ErrUnavailable
		}
		if authenticateSocket(c, owner) != nil {
			_ = c.Close()
			continue
		}
		if c.SetWriteDeadline(time.Now().Add(time.Second)) != nil {
			_ = c.Close()
			return ErrUnavailable
		}
		if WriteFrame(c, []byte(readyFrame)) != nil {
			_ = c.Close()
			return ErrUnavailable
		}
		return serveOwned(context.Background(), c, owner, ownerAlive, run)
	}
	return ErrUnavailable
}
