package tunengine

import (
	"context"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestXrayRelayPermissionReleasedOnceAfterSocketCloses(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := newRelayFixture(t, goodRelayReply())
		g := newSessionGate()
		g.open()
		var acquired, released atomic.Int32
		done := make(chan struct{}, 1)
		h := relayOutboundForTest(t, f, g, context.Background(), func(netip.AddrPort) error { acquired.Add(1); return nil })
		h.releaseUDPRelay = func(a netip.AddrPort) error {
			if a != goodRelayReply().bind.AddrPort() {
				t.Error("wrong released endpoint")
			}
			released.Add(1)
			done <- struct{}{}
			return nil
		}
		if fail {
			f.beforeUDP = g.retire
		}
		c, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
		if fail {
			if err == nil {
				t.Fatal("late blocked association succeeded")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			_ = c.Close()
			_ = c.Close()
			g.retire()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("relay permission survived closed socket")
		}
		assertAssociationClosed(t, f)
		if acquired.Load() != 1 || released.Load() != 1 {
			t.Fatal("unbalanced permissions", acquired.Load(), released.Load())
		}
	}
}

func TestXraySOCKSConcurrentGuardsRemainFlowLocal(t *testing.T) {
	f1 := newRelayFixture(t, goodRelayReply())
	reply := goodRelayReply()
	reply.bind.Port++
	f2 := newRelayFixture(t, reply)
	g := newSessionGate()
	g.open()
	defer g.retire()
	h1 := relayOutboundForTest(t, f1, g, context.Background(), func(a netip.AddrPort) error {
		if a != goodRelayReply().bind.AddrPort() {
			t.Error("first flow adopted second relay")
		}
		return nil
	})
	h2 := *h1
	h2.base = f2
	h2.allowUDPRelay = func(a netip.AddrPort) error {
		if a != reply.bind.AddrPort() {
			t.Error("second flow adopted first relay")
		}
		return nil
	}
	type result struct {
		packet net.PacketConn
		err    error
	}
	r1, r2 := make(chan result, 1), make(chan result, 1)
	for _, job := range []struct {
		h   *guardedSOCKS
		out chan result
	}{{h1, r1}, {&h2, r2}} {
		go func(h *guardedSOCKS, out chan result) {
			c, e := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
			out <- result{c, e}
		}(job.h, job.out)
	}
	a, b := <-r1, <-r2
	if a.err != nil || b.err != nil {
		t.Fatal(a.err, b.err)
	}
	defer b.packet.Close()
	_ = a.packet.Close()
	assertAssociationClosed(t, f1)
	select {
	case <-f2.done:
		t.Fatal("closing first Xray flow closed second association")
	default:
	}
	_ = b.packet.Close()
	assertAssociationClosed(t, f2)
}

func TestXraySOCKSControlEOFClosesPacketSide(t *testing.T) {
	f := newRelayFixture(t, goodRelayReply())
	g := newSessionGate()
	g.open()
	defer g.retire()
	h := relayOutboundForTest(t, f, g, context.Background(), func(netip.AddrPort) error { return nil })
	c, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = f.server.Close()
	_ = f.udpPeer.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err = f.udpPeer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("control EOF left UDP socket alive", err)
	}
	assertAssociationClosed(t, f)
}
