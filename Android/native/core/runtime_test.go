package bridge

import (
	"bluegrass/native/core/protocolconfig"
	"context"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestPinnedXrayInvokesProtectionAndFailsClosedBeforeConnect(t *testing.T) {
	for _, network := range []xnet.Network{xnet.Network_TCP, xnet.Network_UDP} {
		callback := &fakePlatform{fail: true}
		ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), platformKey{}, &platformBridge{callback: callback}), time.Second)
		c, e := internet.DialSystem(ctx, xnet.Destination{Network: network, Address: xnet.IPAddress(net.IPv4(127, 0, 0, 1)), Port: 1}, nil)
		cancel()
		if c != nil {
			c.Close()
			t.Fatal("unprotected connection succeeded")
		}
		if e == nil || len(callback.protected) == 0 {
			t.Fatal("pinned core did not invoke fail-closed protection")
		}
	}
}
func TestXrayRejectsSocketWithoutSessionPlatform(t *testing.T) {
	c, e := internet.DialSystem(context.Background(), xnet.TCPDestination(xnet.IPAddress(net.IPv4(127, 0, 0, 1)), 1), nil)
	if c != nil {
		c.Close()
		t.Fatal("missing platform bypassed")
	}
	if e == nil {
		t.Fatal("missing context succeeded")
	}
}

type coreProtectionFixture struct {
	calls  atomic.Int32
	called chan struct{}
}

func (*coreProtectionFixture) IsValid() bool { return true }
func (p *coreProtectionFixture) Protect(int32) error {
	p.calls.Add(1)
	select {
	case p.called <- struct{}{}:
	default:
	}
	return errNetwork
}
func (*coreProtectionFixture) Resolve(string) (string, error) { return "", errNetwork }
func (*coreProtectionFixture) NetworkInfo() (string, error)   { return "", errNetwork }
func TestEmbeddedXrayInstanceRetainsSessionProtectionContext(t *testing.T) {
	c, err := parseConfig(validConfig)
	if err != nil {
		t.Fatal(err)
	}
	callback := &coreProtectionFixture{called: make(chan struct{}, 10)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, platformKey{}, &platformBridge{callback: callback})
	out, err := protocolconfig.NewOutbound(ctx, c.Protocol, netip.MustParseAddrPort("8.8.4.4:1080"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := out.DialContext(ctx, "tcp", "9.9.9.9:443")
		if e == nil {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(time.Second))
			conn.Write([]byte("probe"))
			var b [1]byte
			conn.Read(b[:])
		}
	}()
	select {
	case <-callback.called:
	case <-ctx.Done():
		t.Fatal("Xray instance lost session context before physical dial")
	}
	cancel()
	out.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Xray physical dial did not cancel")
	}
}
func TestXrayUDPWrapperKeepsLeaseAndExactPhysicalPeer(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var valid atomic.Bool
	valid.Store(true)
	lease := newSessionLease(valid.Load)
	defer lease.close()
	ctx := context.WithValue(context.Background(), platformKey{}, &platformBridge{callback: &fakePlatform{}, lease: lease})
	remote := listener.LocalAddr().(*net.UDPAddr)
	conn, err := internet.DialSystem(ctx, xnet.UDPDestination(xnet.IPAddress(remote.IP), xnet.Port(remote.Port)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	wrapper, ok := conn.(*xnet.PacketConnWrapper)
	if !ok {
		t.Fatal("Xray QUIC requires concrete PacketConnWrapper")
	}
	if _, err = wrapper.PacketConn.WriteTo([]byte("denied"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: remote.Port + 1}); err == nil {
		t.Fatal("physical UDP peer changed")
	}
	if _, err = conn.Write([]byte("allowed")); err != nil {
		t.Fatal(err)
	}
	listener.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 32)
	n, peer, err := listener.ReadFromUDP(b)
	if err != nil || string(b[:n]) != "allowed" {
		t.Fatal("protected UDP not transmitted", err)
	}
	valid.Store(false)
	listener.WriteToUDP([]byte("expired"), peer)
	if n, err = conn.Read(b); n != 0 || err == nil {
		t.Fatal("expired physical UDP read escaped lease")
	}
}
