//go:build darwin

package mactunipc

import (
	"golang.org/x/sys/unix"
	"net"
)

func processIdentity(pid uint32) (Identity, error) {
	if pid <= 1 || pid > 0x7fffffff {
		return Identity{}, ErrUnavailable
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil || p.Proc.P_pid != int32(pid) || p.Proc.P_stat == 5 /* XNU sys/proc.h: SZOMB */ || p.Eproc.Pcred.P_ruid != p.Eproc.Ucred.Uid {
		return Identity{}, ErrUnavailable
	}
	i := Identity{UID: p.Eproc.Ucred.Uid, PID: pid, StartSeconds: p.Proc.P_starttime.Sec, StartMicroseconds: int64(p.Proc.P_starttime.Usec)}
	if i.StartSeconds <= 0 {
		return Identity{}, ErrUnavailable
	}
	return i, nil
}

// SO credentials and PID are delivered by the kernel, not a handshake body.
func socketPeer(c *net.UnixConn) (uint32, uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, 0, ErrUnavailable
	}
	var uid, pid uint32
	var inner error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil || cred.Version != 0 /* XNU sys/ucred.h: XUCRED_VERSION */ {
			inner = ErrUnavailable
			return
		}
		p, e := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if e != nil || p <= 1 {
			inner = ErrUnavailable
			return
		}
		uid, pid = cred.Uid, uint32(p)
	})
	if err != nil || inner != nil {
		return 0, 0, ErrUnavailable
	}
	return uid, pid, nil
}

func ownerAlive(owner Identity) bool {
	live, err := processIdentity(owner.PID)
	return err == nil && live == owner
}

func authenticateSocket(c *net.UnixConn, owner Identity) error {
	uid, pid, err := socketPeer(c)
	if err != nil {
		return err
	}
	peer, err := processIdentity(pid)
	if err != nil {
		return err
	}
	// Always retain socket credential UID even if the process changed UID.
	peer.UID = uid
	live, err := processIdentity(owner.PID)
	if err != nil {
		return err
	}
	return Authenticate(owner, peer, live)
}
