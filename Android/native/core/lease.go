package bridge

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var errLease = errors.New("SESSION_EXPIRED")

type sessionLease struct {
	valid       func() bool
	closed      atomic.Bool
	mu          sync.Mutex
	connections map[io.Closer]struct{}
}

func newSessionLease(valid func() bool) *sessionLease {
	return &sessionLease{valid: valid, connections: make(map[io.Closer]struct{})}
}
func (l *sessionLease) allowed() bool {
	return l != nil && !l.closed.Load() && l.valid != nil && l.valid()
}
func (l *sessionLease) track(c io.Closer) bool {
	if !l.allowed() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed.Load() {
		return false
	}
	l.connections[c] = struct{}{}
	return true
}
func (l *sessionLease) forget(c io.Closer) { l.mu.Lock(); delete(l.connections, c); l.mu.Unlock() }
func (l *sessionLease) close() error {
	if l == nil {
		return nil
	}
	l.closed.Store(true)
	l.mu.Lock()
	list := make([]io.Closer, 0, len(l.connections))
	for c := range l.connections {
		list = append(list, c)
	}
	l.mu.Unlock()
	failed := false
	for _, c := range list {
		if err := c.Close(); err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
			l.forget(c)
		} else {
			failed = true
		}
	}
	if failed {
		return errors.New("CORE_CLOSE_FAILED")
	}
	return nil
}

// Android's callback uses elapsedRealtime (including suspend), not the Go wall
// clock. Recheck after blocking reads so wakeup cannot forward expired buffers.
type leaseConn struct {
	net.Conn
	lease *sessionLease
}

func (c *leaseConn) Read(b []byte) (int, error) {
	if !c.lease.allowed() {
		_ = c.Close()
		return 0, errLease
	}
	n, e := c.Conn.Read(b)
	if !c.lease.allowed() {
		_ = c.Close()
		return 0, errLease
	}
	return n, e
}
func (c *leaseConn) Write(b []byte) (int, error) {
	if !c.lease.allowed() {
		_ = c.Close()
		return 0, errLease
	}
	return c.Conn.Write(b)
}
func (c *leaseConn) Close() error {
	err := c.Conn.Close()
	if err == nil || errors.Is(err, net.ErrClosed) {
		c.lease.forget(c.Conn)
	}
	return err
}

type leasePacketConn struct {
	N.PacketConn
	lease *sessionLease
}

func (c *leasePacketConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	if !c.lease.allowed() {
		_ = c.Close()
		return M.Socksaddr{}, errLease
	}
	address, err := c.PacketConn.ReadPacket(b)
	if !c.lease.allowed() {
		b.Reset()
		_ = c.Close()
		return M.Socksaddr{}, errLease
	}
	return address, err
}
func (c *leasePacketConn) WritePacket(b *buf.Buffer, a M.Socksaddr) error {
	if !c.lease.allowed() {
		b.Release()
		_ = c.Close()
		return errLease
	}
	return c.PacketConn.WritePacket(b, a)
}
func (c *leasePacketConn) Close() error {
	err := c.PacketConn.Close()
	if err == nil || errors.Is(err, net.ErrClosed) {
		c.lease.forget(c.PacketConn)
	}
	return err
}

type leaseWriter struct {
	N.PacketWriter
	lease *sessionLease
}

func (w *leaseWriter) WritePacket(b *buf.Buffer, a M.Socksaddr) error {
	if !w.lease.allowed() {
		b.Release()
		return errLease
	}
	return w.PacketWriter.WritePacket(b, a)
}
