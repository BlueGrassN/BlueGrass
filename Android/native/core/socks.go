package bridge

import (
	"bluegrass/native/core/gate"
	"context"
	"errors"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	xbuf "github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	xsocks "github.com/xtls/xray-core/proxy/socks"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

var (
	errSOCKSFailed    = errors.New("SOCKS_CONNECT_FAILED")
	errUDPRelayDenied = errors.New("UDP_RELAY_DENIED")
)

type socksOptions struct {
	Server             string
	ServerPort         uint16
	Username, Password string
}
type guardedSOCKS struct {
	base               N.Dialer
	session            context.Context
	entry              netip.AddrPort
	username, password string
}

func newGuardedSOCKS(ctx context.Context, o socksOptions) (*guardedSOCKS, error) {
	entry, err := netip.ParseAddr(o.Server)
	if err != nil || !gate.PublicIPv4(o.Server) || o.ServerPort == 0 || (o.Username == "") != (o.Password == "") || len(o.Username) > 255 || len(o.Password) > 255 || ctx.Err() != nil {
		return nil, errSOCKSFailed
	}
	p, _ := ctx.Value(platformKey{}).(*platformBridge)
	return &guardedSOCKS{base: physicalMetadataDialer{p}, session: ctx, entry: netip.AddrPortFrom(entry, o.ServerPort), username: o.Username, password: o.Password}, nil
}

type physicalMetadataDialer struct{ p *platformBridge }

func (d physicalMetadataDialer) DialContext(ctx context.Context, n string, a M.Socksaddr) (net.Conn, error) {
	if d.p == nil {
		return nil, errNetwork
	}
	return d.p.DialContext(ctx, n, a.String())
}
func (d physicalMetadataDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errUDPRelayDenied
}

type socksStreamDialer struct{ out *guardedSOCKS }

func (d socksStreamDialer) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return d.out.DialContext(ctx, n, M.ParseSocksaddr(a))
}
func (h *guardedSOCKS) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	network = N.NetworkName(network)
	udp := network == "udp"
	failure := errSOCKSFailed
	if udp {
		failure = errUDPRelayDenied
	} else if network != "tcp" {
		return nil, failure
	}
	if ctx.Err() != nil || h.session.Err() != nil {
		return nil, failure
	}
	lifetime := newSOCKSSocketLifetime(ctx, h.session, failure)
	fail := func() (net.Conn, error) { lifetime.Close(); return nil, failure }
	raw, err := h.base.DialContext(lifetime.ctx, "tcp", M.SocksaddrFromNetIP(h.entry))
	if err != nil {
		if raw != nil {
			raw.Close()
		}
		return fail()
	}
	if !lifetime.track(raw) {
		return fail()
	}
	socket := &socksLifetimeConn{Conn: raw, lifetime: lifetime}
	headers := []byte{5, 0, 5, 0, 0}
	if h.username != "" {
		headers = []byte{5, 2, 1, 0, 5, 0, 0}
	}
	if udp {
		headers = append(headers, 1)
	}
	handshake := &socksHandshakeConn{Conn: socket, headers: headers, failure: failure}
	deadline := time.Now().Add(12 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if handshake.SetDeadline(deadline) != nil {
		return fail()
	}
	request := &protocol.RequestHeader{Version: 5, Command: protocol.RequestCommandTCP, Address: xnet.ParseAddress(destination.AddrString()), Port: xnet.Port(destination.Port)}
	if udp {
		request.Command = protocol.RequestCommandUDP
	}
	if h.username != "" {
		request.User = &protocol.MemoryUser{Account: &xsocks.Account{Username: h.username, Password: h.password}}
	}
	// Use the pinned Xray protocol implementation. The wrapper additionally
	// validates RFC1929 version, reserved bytes and original wire address type.
	relay, err := xsocks.ClientHandshake(request, handshake, handshake)
	if err != nil || !handshake.complete() {
		return fail()
	}
	if handshake.SetDeadline(time.Time{}) != nil {
		return fail()
	}
	var conn net.Conn = socket
	if udp {
		if relay == nil || !relay.Address.Family().IsIPv4() || !gate.PublicIPv4(relay.Address.String()) || relay.Port == 0 {
			return fail()
		}
		// A fresh RFC1929-authenticated UDP ASSOCIATE may name a different
		// public relay, as supported by the Windows client. Use exactly that
		// wire tuple through the protected physical dialer; never resolve a
		// relay name, substitute the entry, or reuse another flow's reply.
		if relay.Address.String() != h.entry.Addr().String() && h.username == "" {
			return fail()
		}
		relayAddress, err := netip.ParseAddr(relay.Address.String())
		if err != nil {
			return fail()
		}
		raw, err = h.base.DialContext(lifetime.ctx, "udp", M.SocksaddrFrom(relayAddress, uint16(relay.Port)))
		if err != nil {
			if raw != nil {
				raw.Close()
			}
			return fail()
		}
		if !lifetime.track(raw) {
			return fail()
		}
		conn = &xraySOCKSPacketConn{Conn: &socksLifetimeConn{Conn: raw, lifetime: lifetime}, destination: destination, failure: failure}
	}
	if !lifetime.handoff() {
		return fail()
	}
	if udp {
		go func() { var one [1]byte; handshake.Read(one[:]); lifetime.Close() }()
	}
	return conn, nil
}
func (h *guardedSOCKS) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	c, e := h.DialContext(ctx, "udp", destination)
	if e != nil {
		return nil, e
	}
	return c.(net.PacketConn), nil
}

type xraySOCKSPacketConn struct {
	net.Conn
	destination M.Socksaddr
	failure     error
}

func (c *xraySOCKSPacketConn) Write(p []byte) (int, error) {
	return c.WriteTo(p, c.destination.UDPAddr())
}
func (c *xraySOCKSPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	destination := M.SocksaddrFromNet(a)
	request := &protocol.RequestHeader{Address: xnet.ParseAddress(destination.AddrString()), Port: xnet.Port(destination.Port)}
	// The upstream helper's default 8 KiB buffer is sufficient for the header;
	// append the payload separately so ordinary large UDP datagrams are retained.
	wire, err := xsocks.EncodeUDPPacket(request, nil)
	if err != nil {
		return 0, c.failure
	}
	defer wire.Release()
	if wire.IsEmpty() || int(wire.Len())+len(p) > 65507 {
		return 0, c.failure
	}
	packet := make([]byte, 0, int(wire.Len())+len(p))
	packet = append(packet, wire.Bytes()...)
	packet = append(packet, p...)
	if n, writeErr := c.Conn.Write(packet); writeErr != nil || n != len(packet) {
		if writeErr == nil {
			writeErr = c.failure
		}
		return 0, writeErr
	}
	return len(p), nil
}
func (c *xraySOCKSPacketConn) Read(p []byte) (int, error) { n, _, e := c.ReadFrom(p); return n, e }
func (c *xraySOCKSPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	// Xray's UDP codec owns framing; a datagram never crosses read boundaries.
	b := xbuf.NewWithSize(65535)
	defer b.Release()
	if _, err := b.ReadFrom(c.Conn); err != nil {
		return 0, nil, err
	}
	request, err := xsocks.DecodeUDPPacket(b)
	if err != nil {
		return 0, nil, c.failure
	}
	address := M.ParseSocksaddr(net.JoinHostPort(request.Address.String(), request.Port.String()))
	return copy(p, b.Bytes()), address.UDPAddr(), nil
}

// Validate the inbound headers before the address decoder can erase wire
// evidence. The
// wrapper is per call; credentials and application payload are never buffered.
type socksHandshakeConn struct {
	net.Conn
	headers  []byte
	position int
	failure  error
	failed   bool
}

func (c *socksHandshakeConn) complete() bool {
	return !c.failed && c.position == len(c.headers)
}

func (c *socksHandshakeConn) Read(p []byte) (int, error) {
	if c.failed {
		return 0, c.failure
	}
	n, err := c.Conn.Read(p)
	for _, value := range p[:n] {
		if c.complete() {
			break
		}
		if value != c.headers[c.position] {
			c.failed = true
			return 0, c.failure
		}
		c.position++
	}
	return n, err
}

// Caller cancellation governs only establishment and the wire handshake.
// Session cancellation governs the entire socket lifetime. Handoff detaches the
// caller before returning a connection; track closes late dial results.
type socksSocketLifetime struct {
	ctx, session             context.Context
	caller                   context.Context
	cancel                   context.CancelFunc
	stopCaller               func() bool
	stopSession, stopContext func() bool
	failure                  error
	mu                       sync.Mutex
	closed                   bool
	sockets                  []net.Conn
}

func newSOCKSSocketLifetime(caller, session context.Context, failure error) *socksSocketLifetime {
	// Preserve routing metadata without retaining the caller's deadline or
	// cancellation on the established socket (DNS pools cancel after dialing).
	ctx, cancel := context.WithCancel(context.WithoutCancel(caller))
	l := &socksSocketLifetime{ctx: ctx, session: session, caller: caller, cancel: cancel, failure: failure}
	// A canceled context can start its callback immediately. Publish all stop
	// functions under the same lock used by Close before allowing it to proceed.
	l.mu.Lock()
	l.stopCaller = context.AfterFunc(caller, cancel)
	l.stopSession = context.AfterFunc(session, cancel)
	l.stopContext = context.AfterFunc(ctx, func() { _ = l.Close() })
	l.mu.Unlock()
	return l
}

func (l *socksSocketLifetime) active() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.activeLocked()
}

func (l *socksSocketLifetime) activeLocked() bool {
	return !l.closed && l.ctx.Err() == nil && l.session.Err() == nil && (l.caller == nil || l.caller.Err() == nil)
}

func (l *socksSocketLifetime) handoff() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.activeLocked() || l.stopCaller == nil || !l.stopCaller() {
		// A callback that has already started may still call cancel. Its socket
		// cannot be handed off, even if lifetime.ctx is not canceled yet.
		return false
	}
	if l.caller.Err() != nil {
		return false
	}
	// The final Err check commits the handoff. The stopped caller callback can
	// no longer revoke it; session cancellation and explicit Close still can.
	l.caller, l.stopCaller = nil, nil
	return true
}

func (l *socksSocketLifetime) track(conn net.Conn) bool {
	if conn == nil {
		return false
	}
	l.mu.Lock()
	if !l.activeLocked() {
		l.mu.Unlock()
		_ = conn.Close()
		return false
	}
	l.sockets = append(l.sockets, conn)
	l.mu.Unlock()
	return true
}

func (l *socksSocketLifetime) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	sockets := l.sockets
	l.sockets = nil
	stopCaller, stopSession, stopContext := l.stopCaller, l.stopSession, l.stopContext
	l.mu.Unlock()
	if stopCaller != nil {
		stopCaller()
	}
	stopSession()
	stopContext()
	l.cancel()
	var result error
	for _, conn := range sockets {
		if conn.Close() != nil {
			result = l.failure
		}
	}
	return result
}

type socksLifetimeConn struct {
	net.Conn
	lifetime *socksSocketLifetime
}

func (c *socksLifetimeConn) Close() error { return c.lifetime.Close() }

func (c *socksLifetimeConn) Read(p []byte) (int, error) {
	if !c.lifetime.active() {
		return 0, c.lifetime.failure
	}
	n, err := c.Conn.Read(p)
	return n, c.socketError(err)
}

func (c *socksLifetimeConn) Write(p []byte) (int, error) {
	if !c.lifetime.active() {
		return 0, c.lifetime.failure
	}
	n, err := c.Conn.Write(p)
	return n, c.socketError(err)
}

func (c *socksLifetimeConn) socketError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	if errors.Is(err, net.ErrClosed) {
		return net.ErrClosed
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		// Preserve net.Conn deadline semantics without retaining a net.OpError's
		// input-derived endpoint or other physical-dialer details.
		return os.ErrDeadlineExceeded
	}
	return c.lifetime.failure
}
