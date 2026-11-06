package tunengine

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"errors"
	"github.com/miekg/dns"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

type dnsExchanger interface {
	Exchange(context.Context, *dns.Msg) (*dns.Msg, error)
}
type nativeRouter struct {
	ctx         context.Context
	config      nativeConfig
	gate        *sessionGate
	dns         dnsExchanger
	fake        fakeIPStore
	selected    N.Dialer
	socks       *guardedSOCKS
	directs     map[string]N.Dialer
	current     func(context.Context) bool
	routingMode routingpolicy.Mode
	rules       *routingpolicy.Rules
}

func (r *nativeRouter) active(ctx context.Context) bool {
	return ctx.Err() == nil && r.ctx.Err() == nil && r.gate.isOpen() && (r.current == nil || r.current(ctx))
}
func (r *nativeRouter) resolve(ctx context.Context, m inboundContext) (inboundContext, error) {
	if r.fake != nil && r.fake.Contains(m.Destination.Addr) {
		name, ok := r.fake.Lookup(m.Destination.Addr)
		if !ok {
			return m, errFakeIPCache
		}
		m.Destination = M.Socksaddr{Fqdn: name, Port: m.Destination.Port}
	}
	if m.Destination.Fqdn != "" {
		// Resolve with the same classifier that serves intercepted application DNS,
		// except public fake names use the selected TCP resolver to recover real IPv4.
		var d dnsTransport
		if policy, ok := r.dns.(*policyDNS); ok {
			rule := policy.config.dnsRule(dns.Fqdn(m.Destination.Fqdn), dns.TypeA)
			if rule.Server == "fakeip" {
				if r.routingMode == routingpolicy.BypassCN {
					route, err := routingpolicy.Resolve(ctx, r.routingMode, r.rules, m.Destination.Fqdn,
						func(ctx context.Context, name string) ([]netip.Addr, error) {
							return lookupIPv4(ctx, policy.transports["domestic"], name)
						},
						func(ctx context.Context, name string) ([]netip.Addr, error) {
							return lookupIPv4(ctx, policy.transports["public"], name)
						})
					if err != nil {
						return m, err
					}
					m.Destination = M.SocksaddrFrom(route.Address, m.Destination.Port)
					m.domestic = route.Direct
					return m, nil
				}
				d = policy.transports["public"]
			} else {
				d = &exchangeTransport{exchange: policy.Exchange}
			}
		} else {
			d = &exchangeTransport{exchange: r.dns.Exchange}
		}
		addresses, e := lookupIPv4(ctx, d, m.Destination.Fqdn)
		if e != nil || len(addresses) == 0 {
			return m, errors.New("DESTINATION_RESOLUTION_FAILED")
		}
		m.Destination = M.SocksaddrFrom(addresses[0], m.Destination.Port)
	} else if r.routingMode == routingpolicy.BypassCN && r.rules != nil {
		m.domestic = r.rules.IsCN(m.Destination.Addr)
	}
	return m, nil
}

type exchangeTransport struct {
	exchange func(context.Context, *dns.Msg) (*dns.Msg, error)
}

func (*exchangeTransport) Start() error { return nil }
func (*exchangeTransport) Close() error { return nil }
func (*exchangeTransport) Reset()       {}
func (t *exchangeTransport) Exchange(c context.Context, m *dns.Msg) (*dns.Msg, error) {
	return t.exchange(c, m)
}
func (r *nativeRouter) dialer(ctx context.Context, m inboundContext) (N.Dialer, inboundContext, error) {
	if !r.active(ctx) {
		return nil, m, errGateClosed
	}
	resolved, e := r.resolve(ctx, m)
	if e != nil {
		return nil, m, e
	}
	tag := r.config.route(resolved)
	if tag == "selected" {
		if resolved.domestic {
			if direct := r.directs["domestic"]; direct != nil {
				return direct, resolved, nil
			}
			return nil, m, errors.New("DIRECT_PROTECTION_REQUIRED")
		}
		if resolved.Network == "udp" && r.socks != nil {
			return r.socks, resolved, nil
		}
		if r.selected != nil {
			return r.selected, resolved, nil
		}
	}
	if d := r.directs[tag]; d != nil {
		return d, resolved, nil
	}
	return nil, m, errors.New("ROUTE_REJECTED")
}
func (r *nativeRouter) RouteConnection(ctx context.Context, c net.Conn, m inboundContext) error {
	defer c.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	closeIngress := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer closeIngress()
	if !r.active(ctx) {
		return errGateClosed
	}
	if m.Destination.Port == 53 {
		return r.dnsTCP(ctx, c)
	}
	// Only exact trusted executable paths returned by the OS can grant the control exception.
	if m.Network == "tcp" && m.Destination.Port == 443 {
		m.ProcessPath = connectionProcessPath(m.Source, m.Destination)
	}
	d, m, e := r.dialer(ctx, m)
	if e != nil {
		return e
	}
	out, e := d.DialContext(ctx, "tcp", m.Destination)
	if e != nil {
		return e
	}
	defer out.Close()
	closeOutbound := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer closeOutbound()
	if !r.gate.track(out) {
		return errGateClosed
	}
	defer r.gate.forget(out)
	return RelayHalfClosedTCP(c, out, 30*time.Second)
}

// RelayHalfClosedTCP forwards both directions and closes both connections on
// completion. After upload EOF, downstream activity resets the idle drain;
// callers use 30s to match Xray. Outbounds without CloseWrite can still reply.
func RelayHalfClosedTCP(c, out net.Conn, drainTimeout time.Duration) error {
	closeBoth := func() { _ = out.Close(); _ = c.Close() }
	var drainMu sync.Mutex
	var drain *flowIdleTimer
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, c)
		if err == nil {
			drainMu.Lock()
			drain = newFlowIdleTimer(drainTimeout, closeBoth)
			drain.refresh()
			drainMu.Unlock()
			if half, ok := out.(interface{ CloseWrite() error }); ok {
				err = half.CloseWrite()
			}
		}
		if err != nil {
			closeBoth()
		}
		done <- err
	}()
	_, e := io.Copy(&activityWriter{Writer: c, activity: func() {
		drainMu.Lock()
		defer drainMu.Unlock()
		if drain != nil {
			drain.refresh()
		}
	}}, out)
	closeBoth()
	uploadErr := <-done
	if drain != nil {
		drain.stop()
	}
	if e == nil {
		e = uploadErr
	}
	return e
}

type activityWriter struct {
	io.Writer
	activity func()
}

func (w *activityWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	if n > 0 {
		w.activity()
	}
	return n, err
}

func (r *nativeRouter) dnsTCP(ctx context.Context, c net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Xray's TCP SetDeadline is a no-op. Force close also interrupts partial
	// DNS frames and blocked replies; cancel interrupts an in-flight exchange.
	idle := newFlowIdleTimer(15*time.Second, func() { cancel(); _ = c.Close() })
	defer idle.stop()
	wire := &dns.Conn{Conn: c}
	for r.active(ctx) {
		idle.refresh()
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		m, e := wire.ReadMsg()
		if e != nil {
			return e
		}
		reply, e := r.dns.Exchange(ctx, m)
		if e != nil {
			return e
		}
		if !r.active(ctx) {
			return errGateClosed
		}
		if e = wire.WriteMsg(reply); e != nil {
			return e
		}
	}
	return errGateClosed
}
func (r *nativeRouter) RoutePacketConnection(ctx context.Context, c N.PacketConn, m inboundContext) error {
	defer c.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	closeIngress := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer closeIngress()
	if !r.active(ctx) {
		return errGateClosed
	}
	if m.Destination.Port == 53 {
		for r.active(ctx) {
			b := buf.NewSize(65535)
			dest, e := c.ReadPacket(b)
			if e != nil {
				b.Release()
				return e
			}
			query := append([]byte(nil), b.Bytes()...)
			b.Release()
			r.HijackDNSPacket(ctx, query, c, inboundContext{Destination: dest})
		}
		return errGateClosed
	}
	original := m.Destination
	d, m, e := r.dialer(ctx, m)
	if e != nil {
		return e
	}
	out, e := d.ListenPacket(ctx, m.Destination)
	if e != nil {
		return e
	}
	defer out.Close()
	closeOutbound := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer closeOutbound()
	if !r.gate.track(out) {
		return errGateClosed
	}
	defer r.gate.forget(out)
	// Inactivity includes traffic in either direction, even when only the
	// application is sending. The timer also interrupts a blocked inbound read.
	idle := newFlowIdleTimer(udpIdleTimeout, func() { cancel(); _ = out.Close(); _ = c.Close() })
	idle.refresh()
	defer idle.stop()
	destination := m.Destination.UDPAddr()
	done := make(chan error, 1)
	go func() {
		for r.active(ctx) {
			b := buf.NewSize(65535)
			to, e := c.ReadPacket(b)
			if e == nil && to != original {
				e = errors.New("PACKET_DESTINATION_CHANGED")
			}
			if e == nil {
				_, e = out.WriteTo(b.Bytes(), destination)
				if e == nil {
					idle.refresh()
				}
			}
			b.Release()
			if e != nil {
				done <- e
				_ = out.Close()
				_ = c.Close()
				return
			}
		}
		done <- errGateClosed
		_ = out.Close()
		_ = c.Close()
	}()
	data := make([]byte, 65535)
	for r.active(ctx) {
		n, from, e := out.ReadFrom(data)
		if e != nil {
			break
		}
		if from == nil || from.String() != destination.String() {
			continue
		}
		if e = c.WritePacket(buf.As(append([]byte(nil), data[:n]...)), original); e != nil {
			break
		}
		idle.refresh()
	}
	_ = out.Close()
	_ = c.Close()
	return <-done
}
func (r *nativeRouter) RouteConnectionEx(ctx context.Context, c net.Conn, m inboundContext, close N.CloseHandlerFunc) {
	go func() {
		e := r.RouteConnection(ctx, c, m)
		if close != nil {
			close(e)
		}
	}()
}
func (r *nativeRouter) RoutePacketConnectionEx(ctx context.Context, c N.PacketConn, m inboundContext, close N.CloseHandlerFunc) {
	go func() {
		e := r.RoutePacketConnection(ctx, c, m)
		if close != nil {
			close(e)
		}
	}()
}
func (r *nativeRouter) HijackDNSPacket(ctx context.Context, data []byte, w N.PacketWriter, m inboundContext) {
	if !r.active(ctx) {
		return
	}
	query := new(dns.Msg)
	if query.Unpack(data) != nil {
		return
	}
	reply, e := r.dns.Exchange(ctx, query)
	if e != nil || reply == nil || !r.active(ctx) {
		return
	}
	wire, e := reply.Pack()
	if e == nil {
		_ = w.WritePacket(buf.As(wire), m.Destination)
	}
}
