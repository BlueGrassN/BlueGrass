package tunengine

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var errGateClosed = errors.New("BUSINESS_GATE_CLOSED")

type sessionGate struct {
	mu               sync.Mutex
	allowed, retired bool
	connections      map[io.Closer]struct{}
	upload, download atomic.Uint64
}

func newSessionGate() *sessionGate     { return &sessionGate{connections: make(map[io.Closer]struct{})} }
func (g *sessionGate) isOpen() bool    { g.mu.Lock(); defer g.mu.Unlock(); return g.allowed && !g.retired }
func (g *sessionGate) isRetired() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.retired }
func (g *sessionGate) open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired {
		return false
	}
	g.allowed = true
	return true
}
func (g *sessionGate) track(c io.Closer) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.allowed || g.retired {
		return false
	}
	g.connections[c] = struct{}{}
	return true
}
func (g *sessionGate) forget(c io.Closer) { g.mu.Lock(); delete(g.connections, c); g.mu.Unlock() }
func (g *sessionGate) retire() {
	g.mu.Lock()
	g.allowed, g.retired = false, true
	connections := g.connections
	g.connections = make(map[io.Closer]struct{})
	g.mu.Unlock()
	for c := range connections {
		_ = c.Close()
	}
}

type guardedRouter struct {
	Router packetRouter
	gate   *sessionGate
}

func (r *guardedRouter) HijackDNSPacket(ctx context.Context, data []byte, writer N.PacketWriter, m inboundContext) {
	if r.gate.isOpen() {
		r.Router.HijackDNSPacket(ctx, data, &guardedWriter{writer, r.gate}, m)
	}
}
func (r *guardedRouter) RouteConnection(ctx context.Context, conn net.Conn, m inboundContext) error {
	if !r.gate.track(conn) {
		_ = conn.Close()
		return errGateClosed
	}
	defer r.gate.forget(conn)
	return r.Router.RouteConnection(ctx, &countedConn{Conn: conn, gate: r.gate}, m)
}
func (r *guardedRouter) RoutePacketConnection(ctx context.Context, conn N.PacketConn, m inboundContext) error {
	if !r.gate.track(conn) {
		_ = conn.Close()
		return errGateClosed
	}
	defer r.gate.forget(conn)
	return r.Router.RoutePacketConnection(ctx, &countedPacketConn{PacketConn: conn, gate: r.gate}, m)
}
func (r *guardedRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, m inboundContext, onClose N.CloseHandlerFunc) {
	if !r.gate.track(conn) {
		N.CloseOnHandshakeFailure(conn, onClose, errGateClosed)
		return
	}
	r.Router.RouteConnectionEx(ctx, &countedConn{Conn: conn, gate: r.gate}, m, N.AppendClose(onClose, func(error) { r.gate.forget(conn) }))
}
func (r *guardedRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, m inboundContext, onClose N.CloseHandlerFunc) {
	if !r.gate.track(conn) {
		N.CloseOnHandshakeFailure(conn, onClose, errGateClosed)
		return
	}
	r.Router.RoutePacketConnectionEx(ctx, &countedPacketConn{PacketConn: conn, gate: r.gate}, m, N.AppendClose(onClose, func(error) { r.gate.forget(conn) }))
}

// These wrap the inbound/application side: Read is upload, Write is download.
// Probe sockets use the outbound directly and never increment these counters.
type countedConn struct {
	net.Conn
	gate *sessionGate
}

func (c *countedConn) Read(p []byte) (int, error) {
	if !c.gate.isOpen() {
		return 0, errGateClosed
	}
	n, err := c.Conn.Read(p)
	c.gate.upload.Add(uint64(n))
	return n, err
}
func (c *countedConn) Write(p []byte) (int, error) {
	if !c.gate.isOpen() {
		return 0, errGateClosed
	}
	n, err := c.Conn.Write(p)
	c.gate.download.Add(uint64(n))
	return n, err
}
func (c *countedConn) Close() error { c.gate.forget(c.Conn); return c.Conn.Close() }

type countedPacketConn struct {
	N.PacketConn
	gate *sessionGate
}

func (c *countedPacketConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	if !c.gate.isOpen() {
		return M.Socksaddr{}, errGateClosed
	}
	d, err := c.PacketConn.ReadPacket(b)
	if err == nil {
		c.gate.upload.Add(uint64(b.Len()))
	}
	return d, err
}
func (c *countedPacketConn) WritePacket(b *buf.Buffer, d M.Socksaddr) error {
	if !c.gate.isOpen() {
		b.Release()
		return errGateClosed
	}
	n := b.Len()
	err := c.PacketConn.WritePacket(b, d)
	if err == nil {
		c.gate.download.Add(uint64(n))
	}
	return err
}
func (c *countedPacketConn) Close() error { c.gate.forget(c.PacketConn); return c.PacketConn.Close() }

type guardedWriter struct {
	N.PacketWriter
	gate *sessionGate
}

func (w *guardedWriter) WritePacket(b *buf.Buffer, d M.Socksaddr) error {
	if !w.gate.isOpen() {
		b.Release()
		return errGateClosed
	}
	return w.PacketWriter.WritePacket(b, d)
}

type packetRouter interface {
	HijackDNSPacket(context.Context, []byte, N.PacketWriter, inboundContext)
	RouteConnection(context.Context, net.Conn, inboundContext) error
	RoutePacketConnection(context.Context, N.PacketConn, inboundContext) error
	RouteConnectionEx(context.Context, net.Conn, inboundContext, N.CloseHandlerFunc)
	RoutePacketConnectionEx(context.Context, N.PacketConn, inboundContext, N.CloseHandlerFunc)
}
