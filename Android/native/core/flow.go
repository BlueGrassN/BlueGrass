package bridge

import (
	"bluegrass/native/core/gate"
	"bluegrass/native/core/routingpolicy"
	"context"
	"encoding/binary"
	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	xbuf "github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

type flowHandler struct{ session *Session }

func (s *Session) dial(ctx context.Context, network string, dest M.Socksaddr) (net.Conn, error) {
	if ctx.Err() != nil || s.ctx.Err() != nil || !s.lease.allowed() || dest.Port == 0 {
		return nil, errLease
	}
	if network != "tcp" && network != "udp" {
		return nil, errNetwork
	}
	// Use the same tested handoff as guarded SOCKS: cancellation must interrupt
	// DNS and a pending physical/proxy dial; after handoff only session/lease or
	// explicit Close owns the socket. Do not cancel Xray's context on return.
	lifetime := newSOCKSSocketLifetime(ctx, s.ctx, errNetwork)
	committed := false
	defer func() {
		if !committed {
			_ = lifetime.Close()
		}
	}()
	addr := dest.Addr
	var routes []routingpolicy.Route
	if dest.Fqdn != "" || fakeRange.Contains(addr) {
		if s.resolver == nil {
			return nil, errNetwork
		}
		host := dest.Fqdn
		var preferred netip.Addr
		if host == "" {
			binding, ok := s.resolver.fake.lookup(addr)
			if !ok || s.resolver.mode != routingpolicy.BypassCN {
				return nil, errNetwork
			}
			host, preferred = binding.name, binding.candidate
		}
		var err error
		routes, err = s.resolver.resolveCandidates(lifetime.ctx, host)
		if err != nil {
			return nil, errNetwork
		}
		if preferred.IsValid() {
			index := -1
			for i, route := range routes {
				if route.Address == preferred {
					index = i
					break
				}
			}
			// Expiry may remove this candidate. Never retarget its old FakeIP.
			if index < 0 {
				return nil, errNetwork
			}
			first := routes[index]
			copy(routes[1:index+1], routes[:index])
			routes[0] = first
		}
	} else {
		var d streamDialer
		if !addr.IsValid() || addr.IsLoopback() || addr.IsMulticast() || addr.IsUnspecified() {
			return nil, errNetwork
		}
		for _, p := range dnsExcludedPhysicalRanges {
			if p.Contains(addr) {
				return nil, errNetwork
			}
		}
		for _, p := range localRanges(s.platform.info) {
			if p.Contains(addr) {
				d = s.platform
				break
			}
		}
		if d == nil && s.resolver != nil && s.resolver.mode == routingpolicy.BypassCN {
			if s.resolver.rules == nil {
				return nil, errNetwork
			}
			if s.resolver.rules.IsCN(addr) {
				d = s.platform
			}
		}
		if d == nil && !gate.PublicIPv4(addr.String()) {
			return nil, errNetwork
		}
		routes = []routingpolicy.Route{{Address: addr, Direct: d != nil}}
	}
	// UDP socket creation cannot detect an unreachable remote peer. Only TCP
	// synchronous dial failures try another candidate, always with that candidate's
	// own policy; a failed proxy never authorizes direct for the same address.
	// Xray can fail asynchronously after returning a stream: expose distinct DNS
	// answers so the application can retry another candidate, without replaying
	// bytes from a connection already handed to the application.
	if network == "udp" {
		routes = routes[:1]
	}
	var c net.Conn
	proxy := false
	for _, route := range routes {
		if !lifetime.active() || !s.lease.allowed() {
			return nil, errLease
		}
		var d streamDialer
		if route.Direct {
			d = s.platform
		} else {
			s.mu.Lock()
			d = s.selected
			s.mu.Unlock()
		}
		if d == nil {
			return nil, errClosed
		}
		var err error
		c, err = d.DialContext(lifetime.ctx, network, M.SocksaddrFrom(route.Address, dest.Port).String())
		if err == nil && c != nil {
			proxy = !route.Direct
			break
		}
		if c != nil {
			c.Close()
			c = nil
		}
	}
	if c == nil {
		return nil, errNetwork
	}
	if !lifetime.track(c) {
		return nil, errLease
	}
	guarded := &socksLifetimeConn{Conn: c, lifetime: lifetime}
	if !s.lease.track(guarded) {
		return nil, errLease
	}
	if !lifetime.handoff() || !s.lease.allowed() {
		s.lease.forget(guarded)
		return nil, errLease
	}
	committed = true
	leased := &leaseConn{Conn: guarded, lease: s.lease}
	if proxy {
		return &trafficConn{Conn: leased, counters: &s.traffic}, nil
	}
	return leased, nil
}

// Xray invokes one handler per TCP stream or UDP source. UDP buffers retain
// each packet's destination; never route a later packet using the first target.
func (h *flowHandler) HandleConnection(c net.Conn, dest xnet.Destination) {
	s := h.session
	if !s.lease.track(c) {
		c.Close()
		return
	}
	defer s.lease.forget(c)
	defer c.Close()
	guarded := &leaseConn{Conn: c, lease: s.lease}
	if dest.Network == xnet.Network_UDP {
		h.serveUDP(c)
		return
	}
	if dest.Network != xnet.Network_TCP {
		return
	}
	if dest.Port == 53 {
		h.serveDNSStream(s.ctx, guarded)
		return
	}
	address := M.ParseSocksaddr(dest.NetAddr())
	out, err := s.dial(s.ctx, "tcp", address)
	if err != nil {
		return
	}
	defer out.Close()
	stopped := make(chan struct{}, 1)
	go func() { io.Copy(out, guarded); out.Close(); guarded.Close(); stopped <- struct{}{} }()
	io.Copy(guarded, out)
	out.Close()
	guarded.Close()
	<-stopped
}

type multiPacketConn interface {
	ReadMultiBuffer() (xbuf.MultiBuffer, error)
	WriteMultiBuffer(xbuf.MultiBuffer) error
}
type udpFlowSocket struct {
	conn net.Conn
	dead atomic.Bool
}

func (h *flowHandler) serveUDP(c net.Conn) {
	packets, ok := c.(multiPacketConn)
	if !ok {
		return
	}
	s := h.session
	var active atomic.Bool
	active.Store(true)
	defer active.Store(false)
	var writes sync.Mutex
	write := func(data []byte, dest xnet.Destination) error {
		if !active.Load() || !s.lease.allowed() {
			return errLease
		}
		if len(data) > 65507 {
			return errNetwork
		}
		b := xbuf.NewWithSize(int32(len(data)))
		b.Write(data)
		b.UDP = &dest
		writes.Lock()
		defer writes.Unlock()
		if !active.Load() || !s.lease.allowed() {
			b.Release()
			return errLease
		}
		return packets.WriteMultiBuffer(xbuf.MultiBuffer{b})
	}
	sockets := map[xnet.Destination]*udpFlowSocket{}
	defer func() {
		active.Store(false)
		for _, out := range sockets {
			out.conn.Close()
		}
	}()
	idle := time.AfterFunc(5*time.Minute, func() { c.Close() })
	defer idle.Stop()
	for {
		if !s.lease.allowed() {
			return
		}
		mb, err := packets.ReadMultiBuffer()
		if err != nil {
			return
		}
		if !s.lease.allowed() {
			xbuf.ReleaseMulti(mb)
			return
		}
		idle.Reset(5 * time.Minute)
		for _, packet := range mb {
			if packet.UDP == nil {
				packet.Release()
				continue
			}
			dest := *packet.UDP
			if dest.Network != xnet.Network_UDP || dest.Address.Family().IsDomain() {
				packet.Release()
				continue
			}
			if dest.Port == 53 {
				var query mdns.Msg
				err := query.Unpack(packet.Bytes())
				packet.Release()
				if err != nil {
					continue
				}
				reply, err := s.resolver.Exchange(s.ctx, &query)
				if err != nil {
					reply = new(mdns.Msg).SetRcode(&query, mdns.RcodeServerFailure)
				}
				wire, err := reply.Pack()
				if err == nil {
					_ = write(wire, dest)
				}
				continue
			}
			for destination, state := range sockets {
				if state.dead.Load() {
					delete(sockets, destination)
				}
			}
			state := sockets[dest]
			if state == nil {
				// Bound state per application UDP port; never evict a socket into a direct fallback.
				if len(sockets) >= 128 {
					packet.Release()
					continue
				}
				out, err := s.dial(s.ctx, "udp", M.ParseSocksaddr(dest.NetAddr()))
				if err != nil {
					packet.Release()
					continue
				}
				state = &udpFlowSocket{conn: out}
				sockets[dest] = state
				go func(state *udpFlowSocket, dest xnet.Destination) {
					defer state.dead.Store(true)
					defer state.conn.Close()
					b := make([]byte, 65535)
					for {
						n, e := state.conn.Read(b)
						if e != nil {
							return
						}
						if write(b[:n], dest) != nil {
							return
						}
						idle.Reset(5 * time.Minute)
						state.conn.SetDeadline(time.Now().Add(5 * time.Minute))
					}
				}(state, dest)
			}
			out := state.conn
			out.SetDeadline(time.Now().Add(5 * time.Minute))
			_, err = out.Write(packet.Bytes())
			packet.Release()
			if err != nil {
				state.dead.Store(true)
				out.Close()
			}
		}
	}
}
func (h *flowHandler) serveDNSStream(ctx context.Context, c net.Conn) {
	for {
		c.SetDeadline(time.Now().Add(15 * time.Second))
		var size [2]byte
		if _, err := io.ReadFull(c, size[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(size[:]))
		if n < 12 {
			return
		}
		wire := make([]byte, n)
		if _, err := io.ReadFull(c, wire); err != nil {
			return
		}
		var query mdns.Msg
		if query.Unpack(wire) != nil {
			return
		}
		reply, err := h.session.resolver.Exchange(ctx, &query)
		if err != nil {
			reply = new(mdns.Msg).SetRcode(&query, mdns.RcodeServerFailure)
		}
		wire, err = reply.Pack()
		if err != nil || len(wire) > 65535 {
			return
		}
		binary.BigEndian.PutUint16(size[:], uint16(len(wire)))
		if _, err = c.Write(append(size[:], wire...)); err != nil {
			return
		}
	}
}
