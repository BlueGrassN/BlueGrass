package tunengine

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	xbuf "github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
)

type nativeHandler struct {
	ctx    context.Context
	gate   *sessionGate
	router *guardedRouter
}

const udpIdleTimeout = 3 * time.Minute

// AfterFunc.Reset cannot retract a callback that has already started. Check
// the latest activity under the same lock so an old callback cannot expire a
// flow that was refreshed in the meantime. Expiration is terminal.
type flowIdleTimer struct {
	mu       sync.Mutex
	timeout  time.Duration
	until    time.Time
	timer    *time.Timer
	stopped  bool
	onExpire func()
}

func newFlowIdleTimer(timeout time.Duration, onExpire func()) *flowIdleTimer {
	return &flowIdleTimer{timeout: timeout, onExpire: onExpire}
}

func (t *flowIdleTimer) refresh() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.until = time.Now().Add(t.timeout)
	if t.timer == nil {
		t.timer = time.AfterFunc(t.timeout, t.expire)
	} else {
		t.timer.Reset(t.timeout)
	}
}

func (t *flowIdleTimer) expire() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	if remaining := time.Until(t.until); remaining > 0 {
		t.timer.Reset(remaining)
		t.mu.Unlock()
		return
	}
	t.stopped = true
	t.mu.Unlock()
	t.onExpire()
}

func (t *flowIdleTimer) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (h *nativeHandler) HandleConnection(c net.Conn, destination xnet.Destination) {
	defer c.Close()
	if !h.gate.track(c) {
		return
	}
	defer h.gate.forget(c)
	if h.ctx.Err() != nil {
		return
	}
	stop := context.AfterFunc(h.ctx, func() { _ = c.Close() })
	defer stop()
	m := inboundContext{Source: M.SocksaddrFromNet(c.RemoteAddr()), Destination: M.ParseSocksaddr(destination.NetAddr())}
	if destination.Network == xnet.Network_TCP {
		m.Network = "tcp"
		_ = h.router.RouteConnection(h.ctx, c, m)
	} else if destination.Network == xnet.Network_UDP {
		m.Network = "udp"
		h.serveUDP(c, m)
	}
}

// Xray groups UDP by source socket, so each packet may have a different target.
// Keep independent, bounded policy flows: DNS and LAN traffic must not inherit
// the first packet's authorization or return address.
func (h *nativeHandler) serveUDP(c net.Conn, m inboundContext) {
	wire, ok := c.(interface {
		xbuf.Reader
		xbuf.Writer
	})
	if !ok {
		return
	} // net.Conn.Read would discard the per-packet destination.
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	// The pinned stack does not implement UDP deadlines or consume IdleTimeout.
	// Close the source itself, including when every target was already rejected.
	sourceIdle := newFlowIdleTimer(udpIdleTimeout, func() { cancel(); _ = c.Close() })
	sourceIdle.refresh()
	defer sourceIdle.stop()
	var mu, writeMu sync.Mutex
	var workers sync.WaitGroup
	flows := make(map[M.Socksaddr]*xrayPacketFlow)
	defer func() {
		cancel()
		_ = c.Close()
		mu.Lock()
		remaining := make([]*xrayPacketFlow, 0, len(flows))
		for _, f := range flows {
			remaining = append(remaining, f)
		}
		mu.Unlock()
		for _, f := range remaining {
			_ = f.Close()
		}
		workers.Wait()
	}()
	for h.gate.isOpen() && ctx.Err() == nil {
		packets, err := wire.ReadMultiBuffer()
		if err != nil {
			xbuf.ReleaseMulti(packets)
			return
		}
		for i, packet := range packets {
			if packet == nil {
				continue
			}
			to := m.Destination
			if packet.UDP != nil {
				if packet.UDP.Network != xnet.Network_UDP || !packet.UDP.Address.Family().IsIP() {
					packet.Release()
					continue
				}
				to = M.ParseSocksaddr(packet.UDP.NetAddr())
			}
			if !h.gate.isOpen() || ctx.Err() != nil {
				xbuf.ReleaseMulti(packets[i:])
				return
			}
			sourceIdle.refresh()
			mu.Lock()
			flow := flows[to]
			if flow == nil && len(flows) < 64 {
				flow = &xrayPacketFlow{destination: to, source: c.RemoteAddr(), input: make(chan []byte, 32), done: make(chan struct{}), wire: wire, writeMu: &writeMu}
				flowCtx, cancelFlow := context.WithCancel(ctx)
				flow.onActivity = sourceIdle.refresh
				flows[to] = flow
				flow.onClose = func() {
					cancelFlow()
					mu.Lock()
					if flows[to] == flow {
						delete(flows, to)
					}
					mu.Unlock()
				}
				flow.idle = newFlowIdleTimer(udpIdleTimeout, func() { _ = flow.Close() })
				flow.idle.refresh()
				workers.Add(1)
				go func(f *xrayPacketFlow, target M.Socksaddr) {
					defer workers.Done()
					defer f.Close()
					metadata := m
					metadata.Destination = target
					_ = h.router.RoutePacketConnection(flowCtx, f, metadata)
				}(flow, to)
			}
			mu.Unlock()
			if flow != nil {
				data := append([]byte(nil), packet.Bytes()...)
				select {
				case flow.input <- data:
					flow.activity()
				case <-flow.done:
				default:
				}
			}
			packet.Release()
		}
	}
}

type xrayPacketFlow struct {
	destination M.Socksaddr
	source      net.Addr
	input       chan []byte
	done        chan struct{}
	once        sync.Once
	onClose     func()
	onActivity  func()
	idle        *flowIdleTimer
	wire        xbuf.Writer
	writeMu     *sync.Mutex
}

func (f *xrayPacketFlow) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	// The flow timer accounts for both directions; a read-only timeout would
	// terminate active downstream streams after the last application packet.
	select {
	case data := <-f.input:
		if len(data) > b.FreeLen() {
			return M.Socksaddr{}, io.ErrShortBuffer
		}
		_, err := b.Write(data)
		return f.destination, err
	case <-f.done:
		return M.Socksaddr{}, net.ErrClosed
	}
}

func (f *xrayPacketFlow) activity() {
	if f.idle != nil {
		f.idle.refresh()
	}
	if f.onActivity != nil {
		f.onActivity()
	}
}
func (f *xrayPacketFlow) WritePacket(b *buf.Buffer, destination M.Socksaddr) error {
	defer b.Release()
	if destination != f.destination {
		return errors.New("PACKET_DESTINATION_CHANGED")
	}
	select {
	case <-f.done:
		return net.ErrClosed
	default:
	}
	packet := xbuf.FromBytes(append([]byte(nil), b.Bytes()...))
	to := xnet.UDPDestination(xnet.ParseAddress(destination.Addr.String()), xnet.Port(destination.Port))
	packet.UDP = &to
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	select {
	case <-f.done:
		packet.Release()
		return net.ErrClosed
	default:
	}
	err := f.wire.WriteMultiBuffer(xbuf.MultiBuffer{packet})
	if err == nil {
		f.activity()
	}
	return err
}
func (f *xrayPacketFlow) Close() error {
	f.once.Do(func() {
		close(f.done)
		if f.idle != nil {
			f.idle.stop()
		}
		if f.onClose != nil {
			f.onClose()
		}
	})
	return nil
}
func (f *xrayPacketFlow) LocalAddr() net.Addr            { return f.source }
func (*xrayPacketFlow) SetDeadline(time.Time) error      { return nil }
func (*xrayPacketFlow) SetReadDeadline(time.Time) error  { return nil }
func (*xrayPacketFlow) SetWriteDeadline(time.Time) error { return nil }
