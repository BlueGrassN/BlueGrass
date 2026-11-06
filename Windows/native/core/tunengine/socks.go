package tunengine

import (
	"bluegrass/native/core/gate"
	"bluegrass/native/core/protocolconfig"
	"context"
	"encoding/binary"
	"errors"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

var (
	errUDPRelayDenied = errors.New("UDP_RELAY_DENIED")
	errSOCKSFailed    = errors.New("SOCKS_CONNECT_FAILED")
)

// Each Xray flow gets its own relayDialer, so handshake
// evidence cannot be shared between concurrent associations or Prepare epochs.
// Physical TCP is confined to the resolved entry. Only UDP ASSOCIATE's actual
// response.Bind may reach the physical UDP dialer, after host authorization.
type guardedSOCKS struct {
	base               N.Dialer
	selected           N.Dialer
	session            context.Context
	gate               *sessionGate
	entry              netip.AddrPort
	allowUDPRelay      func(netip.AddrPort) error
	releaseUDPRelay    func(netip.AddrPort) error
	username, password string
}

func newGuardedSOCKS(ctx context.Context, p preparedConfig, g *sessionGate, username, password string, base N.Dialer) *guardedSOCKS {
	return &guardedSOCKS{base: base, session: ctx, gate: g, entry: p.entry, allowUDPRelay: p.allowUDPRelay, releaseUDPRelay: p.releaseUDPRelay, username: username, password: password}
}

func (h *guardedSOCKS) active(ctx context.Context, udp bool) bool {
	return ctx.Err() == nil && h.session.Err() == nil && !h.gate.isRetired() && (!udp || h.gate.isOpen())
}

func (h *guardedSOCKS) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) == N.NetworkUDP {
		packet, err := h.ListenPacket(ctx, destination)
		if err != nil {
			return nil, err
		}
		return &connectedRelay{PacketConn: packet, remote: destination.UDPAddr()}, nil
	}
	if N.NetworkName(network) != N.NetworkTCP || !h.active(ctx, false) || h.selected == nil {
		return nil, errSOCKSFailed
	}
	return h.selected.DialContext(ctx, "tcp", destination)
}

func (h *guardedSOCKS) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if !h.active(ctx, true) {
		return nil, errGateClosed
	}
	if h.allowUDPRelay == nil || h.username == "" || h.password == "" || len(h.username) > 255 || len(h.password) > 255 || h.selected == nil {
		return nil, errUDPRelayDenied
	}
	flow, cancel := context.WithCancel(ctx)
	d := &relayDialer{owner: h, result: make(chan error, 1), cancel: cancel, request: flow}
	packet, err := h.selected.ListenPacket(protocolconfig.WithPhysicalDialer(flow, &relayPhysical{d}), destination)
	if err != nil {
		cancel()
		return nil, errUDPRelayDenied
	}
	if err = d.wait(flow); err != nil || !h.active(flow, true) {
		cancel()
		_ = packet.Close()
		return nil, errUDPRelayDenied
	}
	return &relayPacket{PacketConn: packet, cancel: cancel}, nil
}

type relayPhysical struct{ dialer *relayDialer }

func (p *relayPhysical) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := p.dialer.DialContext(ctx, network, M.ParseSocksaddr(address))
	if err != nil {
		p.dialer.report(err)
	}
	return conn, err
}
func (d *relayDialer) report(err error) {
	select {
	case d.result <- err:
	default:
	}
}
func (d *relayDialer) wait(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case err := <-d.result:
		return err
	case <-ctx.Done():
		return errGateClosed
	case <-d.owner.session.Done():
		return errGateClosed
	case <-timer.C:
		return errUDPRelayDenied
	}
}

type relayPacket struct {
	net.PacketConn
	cancel context.CancelFunc
}

func (c *relayPacket) Close() error { c.cancel(); return c.PacketConn.Close() }

type connectedRelay struct {
	net.PacketConn
	remote net.Addr
}

func (c *connectedRelay) Read(b []byte) (int, error)  { n, _, e := c.ReadFrom(b); return n, e }
func (c *connectedRelay) Write(b []byte) (int, error) { return c.WriteTo(b, c.remote) }
func (c *connectedRelay) RemoteAddr() net.Addr        { return c.remote }

type relayDialer struct {
	request   context.Context
	result    chan error
	cancel    context.CancelFunc
	owner     *guardedSOCKS
	handshake *relayHandshakeConn
}

func (d *relayDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	h := d.owner
	var release func() error
	defer func() {
		if release != nil {
			_ = release()
		}
	}()
	if !h.active(ctx, true) || d.request.Err() != nil {
		return nil, errGateClosed
	}
	switch network {
	case N.NetworkTCP:
		if destination.Fqdn != "" || !destination.Addr.Is4() || destination.AddrPort() != h.entry || d.handshake != nil {
			return nil, errSOCKSFailed
		}
	case N.NetworkUDP:
		// Xray ClientHandshake passes response.Bind here, not the
		// logical packet destination. No name resolution or peer substitution.
		if d.handshake == nil || !d.handshake.complete() || d.handshake.endpoint() != destination.AddrPort() ||
			destination.Fqdn != "" || !gate.PublicIPv4(destination.Addr.String()) || destination.Port == 0 || h.allowUDPRelay == nil {
			return nil, errUDPRelayDenied
		}
		if err := h.allowUDPRelay(destination.AddrPort()); err != nil {
			return nil, errUDPRelayDenied
		}
		if h.releaseUDPRelay != nil {
			release = func() error { return h.releaseUDPRelay(destination.AddrPort()) }
		}
		// Callback runs without engine locks. The host serializes its WFP rule
		// update with Block and binds it to this session. Recheck our side too.
		if !h.active(ctx, true) || d.request.Err() != nil {
			return nil, errGateClosed
		}
	default:
		return nil, errSOCKSFailed
	}
	// Retiring a session cancels a pending dial even if the caller supplied an
	// unrelated background context. Keep successful sockets tied to its lifetime.
	dialCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.session, cancel)
	conn, err := h.base.DialContext(dialCtx, network, destination)
	stop()
	cancel()
	if err != nil {
		return nil, errSOCKSFailed
	}
	c := &relaySocket{
		Conn:       conn,
		release:    release,
		stop:       context.AfterFunc(h.session, func() { _ = conn.Close() }),
		stopCaller: context.AfterFunc(ctx, func() { _ = conn.Close() }),
	}
	release = nil // socket now owns the permission, including late failures
	c.gate = h.gate
	if !h.gate.track(c) {
		_ = c.Close()
		return nil, errGateClosed
	}
	if !h.active(ctx, true) || d.request.Err() != nil {
		_ = c.Close()
		return nil, errGateClosed
	}
	if network == N.NetworkTCP {
		d.handshake = &relayHandshakeConn{Conn: c, report: d.report}
		return d.handshake, nil
	}
	if network == N.NetworkUDP {
		// Xray leaves the SOCKS control socket idle while the packet relay runs.
		// Its EOF must retire this flow even if the UDP side is still readable.
		go func() { var one [1]byte; _, _ = d.handshake.Read(one[:]); d.cancel() }()
		d.report(nil)
		return &relayDatagramConn{Conn: c}, nil
	}

	return c, nil
}

// The pinned Xray codec accepts reserved bytes and reads at most its buffer
// size. Enforce the host's wire contract without decoding/forwarding packets.
type relayDatagramConn struct{ net.Conn }

func (c *relayDatagramConn) Read(p []byte) (int, error) {
	wire := make([]byte, len(p)+1)
	n, err := c.Conn.Read(wire)
	if err != nil {
		return 0, err
	}
	if n > len(p) {
		return 0, io.ErrShortBuffer
	}
	if n < 3 || wire[0] != 0 || wire[1] != 0 {
		return 0, errUDPRelayDenied
	}
	return copy(p, wire[:n]), nil
}

func (*relayDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errUDPRelayDenied
}

type relaySocket struct {
	net.Conn
	release    func() error
	gate       *sessionGate
	stop       func() bool
	stopCaller func() bool
	once       sync.Once
	err        error
}

func (c *relaySocket) Close() error {
	c.once.Do(func() {
		c.stop()
		c.stopCaller()
		if c.gate != nil {
			c.gate.forget(c)
		}
		c.err = c.Conn.Close()
		if c.release != nil {
			c.err = errors.Join(c.err, c.release())
		}
	})
	return c.err
}

// Xray validates the selected method; additionally validate the auth version,
// reply version/reserved byte and wire ATYP before authorizing the relay. Inspect headers
// before allowing either: method=RFC1929, successful auth, successful SOCKS5
// reply, and wire ATYP=IPv4. Credentials and payload never enter this buffer.
type relayHandshakeConn struct {
	net.Conn
	position int
	reply    [6]byte
	report   func(error)
}

var relayHandshakeHeaders = [...]byte{5, 2, 1, 0, 5, 0, 0, 1}

func (c *relayHandshakeConn) complete() bool {
	return c.position == len(relayHandshakeHeaders)+len(c.reply)
}
func (c *relayHandshakeConn) endpoint() netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte(c.reply[:4])), binary.BigEndian.Uint16(c.reply[4:]))
}
func (c *relayHandshakeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	for _, value := range p[:n] {
		if c.complete() {
			break
		}
		if c.position < len(relayHandshakeHeaders) {
			if value != relayHandshakeHeaders[c.position] {
				c.report(errUDPRelayDenied)
				return 0, errUDPRelayDenied
			}
		} else {
			c.reply[c.position-len(relayHandshakeHeaders)] = value
		}
		c.position++
	}
	if err != nil && !c.complete() {
		c.report(errUDPRelayDenied)
	}
	return n, err
}
