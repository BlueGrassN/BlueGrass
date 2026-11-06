package bridge

import (
	"context"
	"errors"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"sync/atomic"
	"testing"
)

type readNotifyConn struct {
	net.Conn
	entered chan struct{}
}

type leaseTestPacket struct {
	N.PacketConn
	valid  *atomic.Bool
	writes int
}

func (c *leaseTestPacket) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	_, _ = b.Write([]byte("fixture"))
	c.valid.Store(false)
	return M.ParseSocksaddr("8.8.8.8:53"), nil
}
func (c *leaseTestPacket) WritePacket(b *buf.Buffer, _ M.Socksaddr) error {
	c.writes++
	b.Release()
	return nil
}
func (*leaseTestPacket) Close() error { return nil }
func TestExistingUDPDiscardsReadAndBlocksWriteAfterLeaseExpiry(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	lease := newSessionLease(valid.Load)
	raw := &leaseTestPacket{valid: &valid}
	conn := &leasePacketConn{PacketConn: raw, lease: lease}
	b := buf.NewSize(64)
	defer b.Release()
	if _, err := conn.ReadPacket(b); !errors.Is(err, errLease) || b.Len() != 0 {
		t.Fatal("expired UDP read passed")
	}
	packet := buf.NewSize(16)
	_, _ = packet.Write([]byte("fixture"))
	if err := conn.WritePacket(packet, M.ParseSocksaddr("8.8.8.8:53")); !errors.Is(err, errLease) || raw.writes != 0 {
		t.Fatal("expired UDP write passed")
	}
}

func (c *readNotifyConn) Read(b []byte) (int, error) { close(c.entered); return c.Conn.Read(b) }
func TestExistingTCPChecksLeaseAfterBlockedRead(t *testing.T) {
	var valid atomic.Bool
	valid.Store(true)
	lease := newSessionLease(valid.Load)
	app, peer := net.Pipe()
	defer app.Close()
	defer peer.Close()
	started := make(chan struct{})
	conn := &leaseConn{Conn: &readNotifyConn{Conn: app, entered: started}, lease: lease}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { b := make([]byte, 20); n, e := conn.Read(b); done <- result{n, e} }()
	<-started
	valid.Store(false)
	go peer.Write([]byte("fixture"))
	r := <-done
	if r.n != 0 || !errors.Is(r.err, errLease) {
		t.Fatalf("expired read: n=%d err=%v", r.n, r.err)
	}
	go io.Copy(io.Discard, peer)
	if n, err := conn.Write([]byte("fixture")); err == nil || n != 0 {
		t.Fatal("expired existing stream wrote data")
	}
}

type retryCloser struct{ calls int }

func (c *retryCloser) Close() error {
	c.calls++
	if c.calls == 1 {
		return errors.New("first close failure")
	}
	return nil
}
func TestSessionCloseRetriesFailureAndBecomesIdempotent(t *testing.T) {
	closer := &retryCloser{}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{box: closer, ctx: ctx, cancel: cancel, lease: newSessionLease(func() bool { return true })}
	if s.Close() == nil {
		t.Fatal("first failure hidden")
	}
	if ctx.Err() == nil {
		t.Fatal("failed close kept session live")
	}
	if err := s.Close(); err != nil {
		t.Fatal("retry cannot recover")
	}
	if err := s.Close(); err != nil || closer.calls != 2 {
		t.Fatal("completed close is not idempotent")
	}
}
