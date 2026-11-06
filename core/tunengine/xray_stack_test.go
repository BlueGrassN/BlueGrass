package tunengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/xtls/xray-core/proxy/tun"
)

// Each end copies complete IP packets to the other end. Both protocol stacks
// are real Xray stacks; no native adapter, route, DNS or WFP API is invoked.
type memoryPacketTun struct {
	name   string
	input  chan []byte
	peer   *memoryPacketTun
	closed chan struct{}
	once   sync.Once
}

var _ tun.Tun = (*memoryPacketTun)(nil)

func (*memoryPacketTun) Start() error            { return nil }
func (d *memoryPacketTun) Name() (string, error) { return d.name, nil }
func (*memoryPacketTun) Index() (int, error)     { return 1, nil }
func (d *memoryPacketTun) Close() error {
	d.once.Do(func() { close(d.closed) })
	return nil
}
func (d *memoryPacketTun) ReadPacket(b []byte) ([]byte, error) {
	select {
	case packet := <-d.input:
		if len(packet) > len(b) {
			return nil, io.ErrShortBuffer
		}
		return b[:copy(b, packet)], nil
	case <-d.closed:
		return nil, net.ErrClosed
	}
}
func (d *memoryPacketTun) WritePacket(header, payload []byte) error {
	packet := make([]byte, 0, len(header)+len(payload))
	packet = append(append(packet, header...), payload...)
	select {
	case d.peer.input <- packet:
		return nil
	case <-d.closed:
		return net.ErrClosed
	case <-d.peer.closed:
		return net.ErrClosed
	}
}

func memoryApplication(t *testing.T, r *nativeRouter, g *sessionGate) tun.Stack {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	appWire := &memoryPacketTun{name: "application", input: make(chan []byte, 128), closed: make(chan struct{})}
	tunWire := &memoryPacketTun{name: "native", input: make(chan []byte, 128), closed: make(chan struct{})}
	appWire.peer, tunWire.peer = tunWire, appWire
	var app, native tun.Stack
	t.Cleanup(func() {
		g.retire()
		cancel()
		if native != nil {
			_ = native.Close()
		}
		if app != nil {
			_ = app.Close()
		}
		_ = tunWire.Close()
		_ = appWire.Close()
	})
	var err error
	app, err = tun.NewStack(ctx, tun.StackOptions{
		Tun: appWire, MTU: 1500,
		Addresses: []netip.Addr{netip.MustParseAddr("172.31.255.2")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(); err != nil {
		t.Fatal(err)
	}
	platform := &nativePlatform{
		open:      func(*tun.Config) (tun.Tun, uint64, error) { return tunWire, 42, nil },
		makeStack: tun.NewStack,
	}
	_, native, err = startNative(ctx, platform, nativeOptions(netip.MustParsePrefix("172.31.255.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126")), r, g)
	if err != nil {
		t.Fatal(err)
	}
	if g.isOpen() || platform.luid.Load() != 42 {
		t.Fatal("native activation opened gate early or lost identity")
	}
	g.open()
	return app
}

// The pinned Xray stack's socket deadline methods are no-ops. Bound I/O here
// and close the socket on timeout so a regression fails instead of hanging.
func stackIO(t *testing.T, socket io.Closer, operation func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		_ = socket.Close()
		t.Fatal("Xray stack I/O timed out")
	}
}

func stackPacketExchange(t *testing.T, socket net.PacketConn, destination netip.AddrPort, payload []byte) []byte {
	t.Helper()
	var reply []byte
	stackIO(t, socket, func() error {
		if n, err := socket.WriteTo(payload, net.UDPAddrFromAddrPort(destination)); err != nil {
			return err
		} else if n != len(payload) {
			return io.ErrShortWrite
		}
		b := make([]byte, 65535)
		n, from, err := socket.ReadFrom(b)
		if err != nil {
			return err
		}
		if from == nil || from.String() != destination.String() {
			return fmt.Errorf("UDP reply source = %v, want %v", from, destination)
		}
		reply = b[:n]
		return nil
	})
	return reply
}

func stackDNSExchange(t *testing.T, socket net.PacketConn, destination netip.AddrPort) *dns.Msg {
	t.Helper()
	query := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
	wire, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	reply := new(dns.Msg)
	if err := reply.Unpack(stackPacketExchange(t, socket, destination, wire)); err != nil {
		t.Fatal(err)
	}
	if !reply.Response || reply.Id != query.Id || reply.Rcode != dns.RcodeSuccess || len(reply.Question) != 1 || reply.Question[0] != query.Question[0] {
		t.Fatal("DNS response does not match the application query", reply)
	}
	return reply
}

// The multi-destination test replaces only the outbound physical socket. The
// destination included in the echo proves which address each datagram reached.
type stackEchoDialer struct{}

func (*stackEchoDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("unexpected TCP dial in packet fixture")
}
func (*stackEchoDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return &stackEchoPacketConn{replies: make(chan stackEchoPacket, 16), closed: make(chan struct{})}, nil
}

type stackEchoPacket struct {
	data []byte
	from netip.AddrPort
}
type stackEchoPacketConn struct {
	replies chan stackEchoPacket
	closed  chan struct{}
	once    sync.Once
}

func (c *stackEchoPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case p := <-c.replies:
		return copy(b, p.data), net.UDPAddrFromAddrPort(p.from), nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}
func (c *stackEchoPacketConn) WriteTo(b []byte, to net.Addr) (int, error) {
	address, ok := to.(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected outbound packet address %T", to)
	}
	packet := stackEchoPacket{data: append([]byte(to.String()+":"), b...), from: address.AddrPort()}
	select {
	case c.replies <- packet:
		return len(b), nil
	case <-c.closed:
		return 0, net.ErrClosed
	}
}
func (c *stackEchoPacketConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
func (*stackEchoPacketConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.1:40000"))
}
func (*stackEchoPacketConn) SetDeadline(time.Time) error      { return nil }
func (*stackEchoPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*stackEchoPacketConn) SetWriteDeadline(time.Time) error { return nil }
