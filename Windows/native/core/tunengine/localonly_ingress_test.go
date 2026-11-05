package tunengine

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

type localOnlyPacket struct {
	input  chan []byte
	output chan []byte
	closed chan struct{}
	once   sync.Once
}

func (p *localOnlyPacket) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	select {
	case data := <-p.input:
		_, _ = b.Write(data)
		return M.ParseSocksaddr("8.8.8.8:53"), nil
	case <-p.closed:
		return M.Socksaddr{}, net.ErrClosed
	}
}
func (p *localOnlyPacket) WritePacket(b *buf.Buffer, _ M.Socksaddr) error {
	defer b.Release()
	select {
	case p.output <- append([]byte(nil), b.Bytes()...):
		return nil
	case <-p.closed:
		return net.ErrClosed
	}
}
func (p *localOnlyPacket) Close() error { p.once.Do(func() { close(p.closed) }); return nil }
func (*localOnlyPacket) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 45000}
}
func (*localOnlyPacket) SetDeadline(time.Time) error      { return nil }
func (*localOnlyPacket) SetReadDeadline(time.Time) error  { return nil }
func (*localOnlyPacket) SetWriteDeadline(time.Time) error { return nil }

func TestLocalOnlyActualRouterTCPAndUDPIngressWithoutNativeAdapter(t *testing.T) {
	c := localOnlyFixture()
	c.AllowLAN = false // no DNS upstream sockets can be constructed
	p, err := prepareLocalOnly(c)
	if err != nil {
		t.Fatal(err)
	}
	g := newSessionGate()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := newLocalOnlyRuntime(ctx, p, g, func(context.Context) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Delayed inbound's Start is inert. EnableTunnel is deliberately NEVER called.
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	if r.InterfaceLUID() != 0 {
		t.Fatal("unexpected native adapter")
	}
	g.open()
	defer g.retire()
	router := &guardedRouter{Router: r.(*localOnlyNative).router, gate: g}
	for _, name := range []string{"localhost.", "example.com.", "nas.lan."} {
		want := mDNS.RcodeRefused
		if name == "localhost." {
			want = mDNS.RcodeSuccess
		}
		if name == "nas.lan." {
			want = mDNS.RcodeServerFailure
		}
		metadata := inboundContext{Network: "tcp", Source: M.ParseSocksaddr("127.0.0.1:45000"), Destination: M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 53)}
		client, server := net.Pipe()
		_ = server.SetDeadline(time.Now().Add(2 * time.Second))
		go router.RouteConnectionEx(ctx, server, metadata, nil)
		_ = client.SetDeadline(time.Now().Add(2 * time.Second))
		wire := &mDNS.Conn{Conn: client}
		if err := wire.WriteMsg(localOnlyQuery(name, mDNS.TypeA)); err != nil {
			t.Fatal(err)
		}
		response, err := wire.ReadMsg()
		_ = client.Close()
		if err != nil || response.Rcode != want {
			t.Fatalf("TCP %s: %v %v", name, response, err)
		}
		metadata.Network = "udp"
		packet := &localOnlyPacket{input: make(chan []byte, 1), output: make(chan []byte, 1), closed: make(chan struct{})}
		query, _ := localOnlyQuery(name, mDNS.TypeA).Pack()
		packet.input <- query
		go router.RoutePacketConnectionEx(ctx, packet, metadata, nil)
		select {
		case data := <-packet.output:
			response = new(mDNS.Msg)
			if err := response.Unpack(data); err != nil || response.Rcode != want {
				t.Fatalf("UDP %s: %v %v", name, response, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("UDP ingress timed out", name)
		}
		_ = packet.Close()
	}
}
