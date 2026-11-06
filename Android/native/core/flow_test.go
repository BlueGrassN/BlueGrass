package bridge

import (
	"context"
	"encoding/binary"
	mdns "github.com/miekg/dns"
	xbuf "github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	xtun "github.com/xtls/xray-core/proxy/tun"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stackTunFixture struct {
	packets chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newStackTunFixture() *stackTunFixture {
	return &stackTunFixture{packets: make(chan []byte, 32), closed: make(chan struct{})}
}
func (*stackTunFixture) Start() error                        { return nil }
func (t *stackTunFixture) Close() error                      { t.once.Do(func() { close(t.closed) }); return nil }
func (*stackTunFixture) Name() (string, error)               { return "test", nil }
func (*stackTunFixture) Index() (int, error)                 { return 0, nil }
func (t *stackTunFixture) ReadPacket([]byte) ([]byte, error) { <-t.closed; return nil, net.ErrClosed }
func (t *stackTunFixture) WritePacket(header, payload []byte) error {
	p := append(append([]byte{}, header...), payload...)
	select {
	case t.packets <- p:
		return nil
	case <-t.closed:
		return net.ErrClosed
	}
}

type flowDialFixture struct {
	mu          sync.Mutex
	calls       []string
	connections []net.Conn
}

func (d *flowDialFixture) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	left, right := net.Pipe()
	d.mu.Lock()
	d.calls = append(d.calls, network+":"+address)
	d.connections = append(d.connections, left, right)
	d.mu.Unlock()
	go func() {
		defer right.Close()
		b := make([]byte, 65535)
		for {
			n, e := right.Read(b)
			if e != nil {
				return
			}
			if _, e = right.Write(b[:n]); e != nil {
				return
			}
		}
	}()
	return left, nil
}
func (d *flowDialFixture) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.connections {
		c.Close()
	}
}
func (d *flowDialFixture) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.calls) }
func nativeStackFixture(t *testing.T) (*Session, xtun.Stack, *stackTunFixture, *flowDialFixture, *atomic.Bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	valid := new(atomic.Bool)
	valid.Store(true)
	lease := newSessionLease(func() bool { return ctx.Err() == nil && valid.Load() })
	d := &flowDialFixture{}
	p := &platformBridge{callback: &fakePlatform{}, info: fixtureNetwork, lease: lease}
	s := &Session{ctx: ctx, cancel: cancel, lease: lease, selected: d, platform: p}
	s.resolver, _ = newDNSRouter(ctx, p, d, "global")
	device := newStackTunFixture()
	stack, err := xtun.NewStack(ctx, xtun.StackOptions{Tun: device, MTU: 1500}, &flowHandler{session: s})
	if err != nil {
		t.Fatal(err)
	}
	s.stack = stack
	p.device = device
	t.Cleanup(func() { s.Close(); d.Close() })
	return s, stack, device, d, valid
}
func udpPacketFixture(destination string, port uint16, payload []byte) []byte {
	b := make([]byte, 28+len(payload))
	b[0] = 0x45
	b[8] = 64
	b[9] = 17
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[12:16], []byte{172, 19, 0, 2})
	copy(b[16:20], netip.MustParseAddr(destination).AsSlice())
	binary.BigEndian.PutUint16(b[20:22], 50000)
	binary.BigEndian.PutUint16(b[22:24], port)
	binary.BigEndian.PutUint16(b[24:26], uint16(len(payload)+8))
	copy(b[28:], payload)
	return b
}
func awaitStackPacket(t *testing.T, device *stackTunFixture) []byte {
	t.Helper()
	select {
	case p := <-device.packets:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("native stack did not return a packet")
		return nil
	}
}
func TestNativeXrayTUNUDPRoutesEachDestinationAndNeverFallsBack(t *testing.T) {
	s, stack, device, d, _ := nativeStackFixture(t)
	// One application source sends to two public destinations with a forbidden
	// private destination between them. Xray keeps one UDP handler per source.
	stack.Input(udpPacketFixture("8.8.8.8", 443, []byte("first")))
	p := awaitStackPacket(t, device)
	if string(p[28:]) != "first" {
		t.Fatal("UDP payload boundary lost")
	}
	stack.Input(udpPacketFixture("10.99.0.1", 443, []byte("denied")))
	stack.Input(udpPacketFixture("9.9.9.9", 8443, []byte("second")))
	p = awaitStackPacket(t, device)
	if string(p[28:]) != "second" || net.IP(p[12:16]).String() != "9.9.9.9" {
		t.Fatal("later destination used first flow route")
	}
	if d.count() != 2 {
		t.Fatal("forbidden private UDP destination reached selected outbound")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v := trafficValues(t, s); v["uploadBytes"] != "11" || v["downloadBytes"] != "11" {
		t.Fatal("UDP payload counting", v)
	}
}
func TestNativeXrayTUNDNSInterceptsEveryUDP53Destination(t *testing.T) {
	s, stack, device, d, _ := nativeStackFixture(t)
	s.resolver.public = &dnsPolicyFailureTransport{}
	query := new(mdns.Msg).SetQuestion("example.com.", mdns.TypeA)
	wire, _ := query.Pack()
	stack.Input(udpPacketFixture("8.8.8.8", 53, wire))
	packet := awaitStackPacket(t, device)
	var answer mdns.Msg
	if answer.Unpack(packet[28:]) != nil || answer.Id != query.Id || answer.Rcode != mdns.RcodeServerFailure {
		t.Fatal("DNS failure not returned through TUN")
	}
	if d.count() != 0 {
		t.Fatal("application DNS bypassed selected DNS policy")
	}
	if v := trafficValues(t, s); v["uploadBytes"] != "0" || v["downloadBytes"] != "0" {
		t.Fatal("DNS was counted", v)
	}
}
func TestNativeXrayTUNUDPDropsExpiredRead(t *testing.T) {
	s, _, _, d, valid := nativeStackFixture(t)
	c := &expiringPacketFixture{valid: valid, closed: make(chan struct{})}
	h := &flowHandler{session: s}
	h.HandleConnection(c, xnet.UDPDestination(xnet.ParseAddress("8.8.8.8"), 443))
	if d.count() != 0 {
		t.Fatal("expired buffer reached outbound")
	}
}

type expiringPacketFixture struct {
	net.Conn
	valid  *atomic.Bool
	closed chan struct{}
}

func (c *expiringPacketFixture) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func TestNativeXrayTUNTCPUsesSelectedAndSessionCloseRevokesStream(t *testing.T) {
	s, _, _, d, _ := nativeStackFixture(t)
	app, inbound := net.Pipe()
	done := make(chan struct{})
	go func() {
		(&flowHandler{session: s}).HandleConnection(inbound, xnet.TCPDestination(xnet.ParseAddress("8.8.8.8"), 443))
		close(done)
	}()
	defer app.Close()
	app.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := app.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if _, err := io.ReadFull(app, b); err != nil || string(b) != "hello" {
		t.Fatal("TCP payload lost", err)
	}
	if d.count() != 1 {
		t.Fatal("TCP bypassed selected")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v := trafficValues(t, s); v["uploadBytes"] != "5" || v["downloadBytes"] != "5" {
		t.Fatal("TCP payload counting", v)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session close leaked TCP handler")
	}
}

func (c *expiringPacketFixture) ReadMultiBuffer() (xbuf.MultiBuffer, error) {
	b := xbuf.FromBytes([]byte("expired"))
	destination := xnet.UDPDestination(xnet.ParseAddress("8.8.8.8"), 443)
	b.UDP = &destination
	c.valid.Store(false)
	return xbuf.MultiBuffer{b}, nil
}
func (c *expiringPacketFixture) WriteMultiBuffer(b xbuf.MultiBuffer) error {
	xbuf.ReleaseMulti(b)
	return nil
}
