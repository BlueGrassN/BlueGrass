package bridge

import (
	"context"
	"errors"
	xnet "github.com/xtls/xray-core/common/net"
	tun "github.com/xtls/xray-core/proxy/tun"
	"github.com/xtls/xray-core/transport/internet"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"
)

var errUnsupported = errors.New("UNSUPPORTED_PLATFORM_OPERATION")

type platformKey struct{}
type platformBridge struct {
	callback Platform
	info     networkInfo
	fd       int
	tunMu    sync.Mutex
	device   tun.Tun
	lease    *sessionLease
}

func (p *platformBridge) AutoDetectInterfaceControl(fd int) error {
	if p == nil || p.callback == nil || fd < 0 || !p.valid() || p.callback.Protect(int32(fd)) != nil || !p.valid() {
		return errors.New("SOCKET_PROTECTION_FAILED")
	}
	return nil
}
func (p *platformBridge) valid() bool {
	return p != nil && p.callback != nil && p.callback.IsValid() && (p.lease == nil || p.lease.allowed())
}
func (p *platformBridge) control(_, _ string, raw syscall.RawConn) error {
	var failure error
	if err := raw.Control(func(fd uintptr) { failure = p.AutoDetectInterfaceControl(int(fd)) }); err != nil {
		return errNetwork
	}
	return failure
}
func (p *platformBridge) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Numeric destinations only: the captured Network resolves the entry; public
	// DNS goes through the selected Xray outbound. Never invoke host DNS here.
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "0" || (network != "tcp" && network != "udp") {
		return nil, errNetwork
	}
	if _, err = netip.ParseAddr(host); err != nil || !p.valid() || ctx.Err() != nil {
		return nil, errNetwork
	}
	d := net.Dialer{Timeout: 12 * time.Second, Control: p.control}
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, errNetwork
	}
	if p.lease != nil {
		if !p.lease.track(conn) {
			conn.Close()
			return nil, errLease
		}
		return &leaseConn{Conn: conn, lease: p.lease}, nil
	}
	if !p.callback.IsValid() {
		conn.Close()
		return nil, errNetwork
	}
	return conn, nil
}

// Install once, before any session. Xray's default controllers only log errors;
// this implementation returns protection failures to the caller before connect.
func init() { internet.UseAlternativeSystemDialer(androidSystemDialer{}) }

type androidSystemDialer struct{}

func (androidSystemDialer) DestIpAddress() net.IP { return nil }
func (androidSystemDialer) Dial(ctx context.Context, _ xnet.Address, dest xnet.Destination, _ *internet.SocketConfig) (net.Conn, error) {
	p, _ := ctx.Value(platformKey{}).(*platformBridge)
	if p == nil {
		return nil, errNetwork
	}
	conn, err := p.DialContext(ctx, dest.Network.SystemString(), dest.NetAddr())
	if err != nil || dest.Network != xnet.Network_UDP {
		return conn, err
	}
	// Xray's QUIC/finalmask transports require this concrete wrapper. Keep the
	// socket connected and reject WriteTo attempts to change its physical peer.
	return &xnet.PacketConnWrapper{PacketConn: &protectedDatagram{Conn: conn}, Dest: conn.RemoteAddr()}, nil
}

type protectedDatagram struct{ net.Conn }

func (c *protectedDatagram) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := c.Read(b)
	return n, c.RemoteAddr(), err
}
func (c *protectedDatagram) WriteTo(b []byte, peer net.Addr) (int, error) {
	if peer == nil || peer.String() != c.RemoteAddr().String() {
		return 0, errNetwork
	}
	return c.Write(b)
}
func (p *platformBridge) closeTun() error {
	p.tunMu.Lock()
	defer p.tunMu.Unlock()
	if p.device == nil {
		return nil
	}
	err := p.device.Close()
	if err == nil || errors.Is(err, os.ErrClosed) {
		p.device = nil
		return nil
	}
	return errors.New("CORE_CLOSE_FAILED")
}
