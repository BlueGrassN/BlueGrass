package protocolconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	// Register only the runtime features/transports used by the closed builder.
	// Platform socket lifetime handling is isolated in platform-specific files.
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/http"
	_ "github.com/xtls/xray-core/proxy/hysteria"
	_ "github.com/xtls/xray-core/proxy/shadowsocks"
	_ "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/trojan"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/proxy/vmess/outbound"
	_ "github.com/xtls/xray-core/transport/internet/hysteria"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
)

var (
	errSessionClosed = errors.New("PROTOCOL_SESSION_CLOSED")
	errConstruction  = errors.New("PROTOCOL_CONSTRUCTION_FAILED")
	errStart         = errors.New("PROTOCOL_START_FAILED")
	errDial          = errors.New("PROTOCOL_DIAL_FAILED")
	errDestination   = errors.New("PROTOCOL_DESTINATION_INVALID")
	errPacketSize    = errors.New("PROTOCOL_PACKET_TOO_LARGE")
	errPacketEmpty   = errors.New("PROTOCOL_PACKET_EMPTY")
	errPacketWrite   = errors.New("PROTOCOL_PACKET_WRITE_FAILED")
	errStream        = errors.New("PROTOCOL_STREAM_FAILED")
	errProtocolClose = errors.New("PROTOCOL_CLOSE_FAILED")
)

// Carries the owning session through flow contexts for shared physical transports.
type outboundLifetimeKey struct{}

// Outbound owns one Xray instance and all its live flow contexts. Construction
// starts no listener and performs no remote dial. Xray dispatch is asynchronous:
// a successful DialContext is not evidence of a completed proxy handshake.
type Outbound struct {
	ctx            context.Context
	cancel         context.CancelFunc
	instance       *core.Instance
	udp            bool
	packetProtocol string
	packetCipher   string
	mu             sync.Mutex
	closed         bool
	flows          map[*nativeFlow]struct{}
	workers        sync.WaitGroup
	closeOnce      sync.Once
	closeErr       error
}

func NewOutbound(ctx context.Context, c Config, entry netip.AddrPort) (*Outbound, error) {
	selected, err := Build(c, entry)
	if err != nil {
		return nil, err
	}
	return StartOutbound(ctx, selected)
}

// StartOutbound starts trusted application-built settings, after Build validates
// the resource. Platform code may add its physical-interface binding. It must
// never be called with raw core configuration from the API or IPC.
func StartOutbound(ctx context.Context, selected *conf.OutboundDetourConfig) (*Outbound, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errSessionClosed
	}
	if selected == nil {
		return nil, errConstruction
	}
	configuration, err := (&conf.Config{
		LogConfig:       &conf.LogConfig{LogLevel: "none", AccessLog: "none", ErrorLog: "none"},
		OutboundConfigs: []conf.OutboundDetourConfig{*selected},
	}).Build()
	if err != nil {
		return nil, errConstruction
	}
	lifetime, cancel := context.WithCancel(ctx)
	lifetime = context.WithValue(lifetime, outboundLifetimeKey{}, lifetime)
	instance, err := core.NewWithContext(lifetime, configuration)
	if err != nil {
		cancel()
		return nil, errConstruction
	}
	if err = instance.Start(); err != nil {
		cancel()
		_ = instance.Close()
		return nil, errStart
	}
	out := &Outbound{ctx: lifetime, cancel: cancel, instance: instance, udp: selected.Protocol != "http", flows: make(map[*nativeFlow]struct{})}
	out.packetProtocol = selected.Protocol
	if selected.Protocol == "shadowsocks" && selected.Settings != nil {
		// Keep only the cipher name needed for framing, never another credential copy.
		var settings struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(*selected.Settings, &settings)
		out.packetCipher = settings.Method
	}
	// Cancellation retires even an idle instance; there is no polling goroutine.
	context.AfterFunc(lifetime, func() { _ = out.Close() })
	if lifetime.Err() != nil {
		_ = out.Close()
		return nil, errSessionClosed
	}
	return out, nil
}

func nativeDestination(network, address string) (xnet.Destination, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		network = "tcp"
	case "udp", "udp4", "udp6":
		network = "udp"
	default:
		return xnet.Destination{}, errDestination
	}
	host, portString, err := net.SplitHostPort(address)
	if err != nil || host == "" || len(host) > 253 || strings.ContainsAny(host, "\x00\r\n\t /?#@") {
		return xnet.Destination{}, errDestination
	}
	port, err := strconv.Atoi(portString)
	if err != nil || port <= 0 || port > 65535 {
		return xnet.Destination{}, errDestination
	}
	destination, err := xnet.ParseDestination(network + ":" + address)
	if err != nil || !destination.IsValid() {
		return xnet.Destination{}, errDestination
	}
	return destination, nil
}

func (o *Outbound) newFlow(ctx context.Context) (*nativeFlow, error) {
	if ctx == nil {
		return nil, errSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil {
		return nil, errSessionClosed
	}
	lifetime, cancel := context.WithCancel(o.ctx)
	if dialer, ok := ctx.Value(physicalDialerKey{}).(PhysicalDialer); ok {
		lifetime = WithPhysicalDialer(lifetime, dialer)
	}
	stop := context.AfterFunc(ctx, cancel)
	return &nativeFlow{owner: o, ctx: lifetime, cancel: cancel, stopCaller: stop}, nil
}

// attach publishes fully initialized flows before starting their two bounded
// I/O workers. Close prohibits new WaitGroup additions under the same lock.
func (o *Outbound) attach(f *nativeFlow) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil || f.ctx.Err() != nil {
		return errSessionClosed
	}
	o.flows[f] = struct{}{}
	o.workers.Add(2)
	return nil
}

// Caller cancellation remains attached after dispatch, so an HTTP transport or
// diagnostic timeout cannot leave an asynchronous Xray handshake running.
func (o *Outbound) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dest, err := nativeDestination(network, address)
	if err != nil {
		return nil, err
	}
	if dest.Network == xnet.Network_UDP {
		packet, err := o.ListenPacket(ctx, address)
		if err != nil {
			return nil, err
		}
		return &nativeConnectedPacket{PacketConn: packet, remote: nativeAddr{"udp", dest.NetAddr()}}, nil
	}
	f, err := o.newFlow(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := core.Dial(f.ctx, o.instance, dest)
	if err != nil {
		f.abort()
		return nil, errDial
	}
	reader, download := net.Pipe()
	writer, upload := net.Pipe()
	result := &nativeStream{Conn: reader, writer: writer, flow: f, remote: nativeAddr{"tcp", dest.NetAddr()}}
	f.interrupt = func() {
		result.stop()
		_ = raw.Close()
		_ = reader.Close()
		_ = download.Close()
		_ = writer.Close()
		_ = upload.Close()
	}
	if err = o.attach(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	// Independent pipes retain resettable deadlines and allow upload shutdown
	// without closing the download. Each direction has one bounded copy worker.
	go func() {
		defer o.workers.Done()
		_, err := io.Copy(download, raw)
		result.finishRead(err)
		_ = f.Close()
	}()
	go func() {
		defer o.workers.Done()
		if _, err := io.Copy(raw, upload); err != nil {
			_ = f.Close()
			return
		}
		_ = result.CloseWrite()
	}()
	context.AfterFunc(f.ctx, func() { _ = f.Close() })
	return result, nil
}

func (o *Outbound) ListenPacket(ctx context.Context, address string) (net.PacketConn, error) {
	if !o.udp {
		return nil, ErrUnsupportedProtocol
	}
	dest, err := nativeDestination("udp", address)
	if err != nil {
		return nil, err
	}
	f, err := o.newFlow(ctx)
	if err != nil {
		return nil, err
	}
	// Use core.Dial's packet-aware buffers, not core.DialUDP: the latter silently
	// truncates WriteTo at 8192 bytes and its Close does not interrupt both pipes.
	raw, err := core.Dial(f.ctx, o.instance, dest)
	if err != nil {
		f.abort()
		return nil, errDial
	}
	reader, readOK := raw.(buf.Reader)
	writer, writeOK := raw.(buf.Writer)
	if !readOK || !writeOK {
		_ = raw.Close()
		f.abort()
		return nil, errConstruction
	}
	p := &nativePacket{flow: f, destination: dest, maxPayload: o.packetLimit(dest), reader: reader, writer: writer, reads: make(chan nativeDatagram, 1), writes: make(chan nativePacketWrite), readDeadline: newNativeDeadline(), writeDeadline: newNativeDeadline()}
	f.interrupt = func() { _ = raw.Close() }
	if err = o.attach(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	go p.readLoop()
	go p.writeLoop()
	context.AfterFunc(f.ctx, func() { _ = f.Close() })
	return p, nil
}

func (o *Outbound) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		flows := make([]*nativeFlow, 0, len(o.flows))
		for f := range o.flows {
			flows = append(flows, f)
		}
		o.mu.Unlock()
		o.cancel()
		for _, f := range flows {
			_ = f.Close()
		}
		o.workers.Wait()
		if o.instance.Close() != nil {
			o.closeErr = errProtocolClose
		}
	})
	return o.closeErr
}

func CloseOutbound(out io.Closer) error {
	if out != nil && out.Close() != nil {
		return errProtocolClose
	}
	return nil
}

type nativeFlow struct {
	owner      *Outbound
	ctx        context.Context
	cancel     context.CancelFunc
	stopCaller func() bool
	interrupt  func()
	once       sync.Once
}

func (f *nativeFlow) abort() { f.stopCaller(); f.cancel() }
func (f *nativeFlow) Close() error {
	f.once.Do(func() {
		f.abort()
		if f.interrupt != nil {
			f.interrupt()
		}
		f.owner.mu.Lock()
		delete(f.owner.flows, f)
		f.owner.mu.Unlock()
	})
	return nil
}

type nativeAddr struct{ network, address string }

func (a nativeAddr) Network() string { return a.network }
func (a nativeAddr) String() string  { return a.address }

type nativeStream struct {
	net.Conn
	writer              net.Conn
	flow                *nativeFlow
	remote              net.Addr
	mu                  sync.Mutex
	readErr             error
	closed, writeClosed bool
	drainTimer          *time.Timer
	drainTimeout        time.Duration
	drainUntil          time.Time
}

func (c *nativeStream) Close() error         { return c.flow.Close() }
func (c *nativeStream) RemoteAddr() net.Addr { return c.remote }

const nativeDrainTimeout = 30 * time.Second

// core.Dial exposes no upload-only close, and some pinned proxy outbounds do
// not forward physical FIN. Preserve replies to completed requests while
// bounding idle time while an EOF-waiting peer retains this flow. An active
// download can outlive the timeout as long as the caller keeps receiving data.
func (c *nativeStream) CloseWrite() error { return c.closeWrite(nativeDrainTimeout) }
func (c *nativeStream) closeWrite(timeout time.Duration) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	if c.writeClosed {
		c.mu.Unlock()
		return nil
	}
	c.writeClosed = true
	c.drainTimeout = timeout
	c.drainUntil = time.Now().Add(timeout)
	c.drainTimer = time.AfterFunc(timeout, c.expireDrain)
	c.mu.Unlock()
	return c.writer.Close()
}

func (c *nativeStream) expireDrain() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	// Reset can race an already running AfterFunc. Recheck the last delivered
	// byte under the same lock instead of letting a stale callback close a flow.
	if remaining := time.Until(c.drainUntil); remaining > 0 {
		c.drainTimer.Reset(remaining)
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.readErr == nil {
		c.readErr = os.ErrDeadlineExceeded
	}
	c.mu.Unlock()
	_ = c.Close()
}

func (c *nativeStream) finishRead(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return
	}
	switch {
	case c.flow.ctx.Err() != nil:
		c.readErr = net.ErrClosed
	case err == nil:
		// io.Copy has delivered every byte before reporting clean source EOF.
		c.readErr = io.EOF
	default:
		c.readErr = errStream
	}
}

func (c *nativeStream) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.readErr == nil {
		c.readErr = net.ErrClosed
	}
	if c.drainTimer != nil {
		c.drainTimer.Stop()
	}
}

func (c *nativeStream) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.mu.Lock()
		if c.drainTimer != nil && !c.closed {
			c.drainUntil = time.Now().Add(c.drainTimeout)
			c.drainTimer.Reset(c.drainTimeout)
		}
		c.mu.Unlock()
	}
	if err == io.ErrClosedPipe || err == io.EOF {
		c.mu.Lock()
		terminal := c.readErr
		c.mu.Unlock()
		if terminal != nil {
			err = terminal
		}
	}
	return n, err
}
func (c *nativeStream) Write(b []byte) (int, error) { return c.writer.Write(b) }
func (c *nativeStream) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *nativeStream) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if c.writeClosed {
		return nil
	}
	return c.writer.SetWriteDeadline(t)
}

type nativeConnectedPacket struct {
	net.PacketConn
	remote net.Addr
}

func (c *nativeConnectedPacket) Read(p []byte) (int, error) {
	n, _, err := c.ReadFrom(p)
	return n, err
}
func (c *nativeConnectedPacket) Write(p []byte) (int, error) { return c.WriteTo(p, c.remote) }
func (c *nativeConnectedPacket) RemoteAddr() net.Addr        { return c.remote }

type nativeDatagram struct {
	data []byte
	from net.Addr
}
type nativePacketWrite struct {
	data   []byte
	result chan error
}
type nativePacket struct {
	flow                        *nativeFlow
	destination                 xnet.Destination
	maxPayload                  int
	reader                      buf.Reader
	writer                      buf.Writer
	reads                       chan nativeDatagram
	writes                      chan nativePacketWrite
	readDeadline, writeDeadline *nativeDeadline
}

func (p *nativePacket) readLoop() {
	defer p.flow.owner.workers.Done()
	defer close(p.reads)
	defer p.Close()
	for {
		packets, err := p.reader.ReadMultiBuffer()
		for i, b := range packets {
			from := p.destination
			if b.UDP != nil {
				from = *b.UDP
			}
			item := nativeDatagram{data: append([]byte(nil), b.Bytes()...), from: nativeAddr{"udp", from.NetAddr()}}
			b.Release()
			select {
			case p.reads <- item:
			case <-p.flow.ctx.Done():
				buf.ReleaseMulti(packets[i+1:])
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *nativePacket) writeLoop() {
	defer p.flow.owner.workers.Done()
	for {
		select {
		case <-p.flow.ctx.Done():
			return
		case request := <-p.writes:
			if p.flow.ctx.Err() != nil {
				request.result <- net.ErrClosed
				return
			}
			b := buf.FromBytes(request.data)
			dest := p.destination
			b.UDP = &dest
			err := p.writer.WriteMultiBuffer(buf.MultiBuffer{b})
			if err != nil {
				err = errPacketWrite
			}
			request.result <- err
			if err != nil {
				_ = p.Close()
				return
			}
		}
	}
}

func (p *nativePacket) ReadFrom(b []byte) (int, net.Addr, error) {
	packet, err := nativeWait(p.flow.ctx, p.readDeadline, p.reads)
	if err != nil {
		return 0, nil, err
	}
	if len(packet.data) > len(b) {
		return 0, packet.from, io.ErrShortBuffer
	}
	return copy(b, packet.data), packet.from, nil
}

func (p *nativePacket) WriteTo(b []byte, addr net.Addr) (int, error) {
	// Reject before enqueueing: Xray's asynchronous writers can silently drop
	// packets that exceed their framing buffer, and discard empty datagrams.
	if len(b) == 0 {
		return 0, errPacketEmpty
	}
	if len(b) > p.maxPayload {
		return 0, errPacketSize
	}
	if addr == nil {
		return 0, errDestination
	}
	dest, err := nativeDestination("udp", addr.String())
	if err != nil || dest.NetAddr() != p.destination.NetAddr() {
		return 0, errDestination
	}
	request := nativePacketWrite{data: append([]byte(nil), b...), result: make(chan error, 1)}
	for {
		if p.flow.ctx.Err() != nil {
			return 0, net.ErrClosed
		}
		expired, changed, timer := p.writeDeadline.wait()
		if expired != nil {
			select {
			case <-expired:
				if timer != nil {
					timer.Stop()
				}
				return 0, os.ErrDeadlineExceeded
			default:
			}
		}
		select {
		case <-p.flow.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return 0, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-expired:
			if timer != nil {
				timer.Stop()
			}
			return 0, os.ErrDeadlineExceeded
		case p.writes <- request:
			if timer != nil {
				timer.Stop()
			}
			result, err := nativeWait(p.flow.ctx, p.writeDeadline, request.result)
			if err != nil {
				// A dispatched write cannot be safely retracted. Retire this one flow on
				// timeout, preventing a later send after its caller has returned.
				_ = p.Close()
				return 0, err
			}
			if result != nil {
				return 0, result
			}
			return len(b), nil
		}
	}
}

// Limits are for the pinned codecs, not the UDP wire maximum. Account for
// both directions where reply framing is larger than request framing.
func (o *Outbound) packetLimit(dest xnet.Destination) int {
	addressSize := 1 + 2 // ATYP, port
	if dest.Address.Family().IsDomain() {
		addressSize += 1 + len(dest.Address.Domain())
	} else {
		addressSize += len(dest.Address.IP())
	}
	switch o.packetProtocol {
	case "socks":
		return buf.Size - 3 - addressSize // RSV, FRAG + SOCKS address
	case "shadowsocks":
		overhead := 32 + 16 // salt and AEAD tag
		switch o.packetCipher {
		case "aes-128-gcm":
			overhead = 16 + 16
		case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm":
			overhead = 16 + 19 + 16 // session header, server header, tag
		case "2022-blake3-chacha20-poly1305":
			overhead = 24 + 35 + 16 // nonce, server header, tag
		}
		return buf.Size - overhead - addressSize
	case "trojan":
		return buf.Size - addressSize - 4 // packet length and CRLF
	case "vmess", "vless":
		// common/xudp.PacketWriter drops length+666 > buf.Size. This also
		// fits VMess's AEAD length/tag/padding on its non-XUDP DNS path.
		return buf.Size - 666
	case "hysteria":
		addressLen := len(dest.NetAddr())
		lengthBytes := 1
		if addressLen >= 64 {
			lengthBytes = 2
		}
		return buf.Size - 8 - lengthBytes - addressLen
	default:
		return buf.Size // test-only direct outbound
	}
}
func (p *nativePacket) Close() error        { return p.flow.Close() }
func (p *nativePacket) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4zero} }
func (p *nativePacket) SetDeadline(t time.Time) error {
	p.readDeadline.set(t)
	p.writeDeadline.set(t)
	return nil
}
func (p *nativePacket) SetReadDeadline(t time.Time) error  { p.readDeadline.set(t); return nil }
func (p *nativePacket) SetWriteDeadline(t time.Time) error { p.writeDeadline.set(t); return nil }

// Deadline changes wake operations already waiting. Clearing an expired read
// deadline preserves the pending datagram and allows the next read to proceed.
type nativeDeadline struct {
	mu      sync.Mutex
	at      time.Time
	changed chan struct{}
}

func newNativeDeadline() *nativeDeadline { return &nativeDeadline{changed: make(chan struct{})} }
func (d *nativeDeadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.at = t
	close(d.changed)
	d.changed = make(chan struct{})
}
func (d *nativeDeadline) wait() (<-chan time.Time, <-chan struct{}, *time.Timer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.at.IsZero() {
		return nil, d.changed, nil
	}
	if !d.at.After(time.Now()) {
		expired := make(chan time.Time)
		close(expired)
		return expired, d.changed, nil
	}
	timer := time.NewTimer(time.Until(d.at))
	return timer.C, d.changed, timer
}
func nativeWait[T any](ctx context.Context, d *nativeDeadline, result <-chan T) (T, error) {
	var zero T
	for {
		if ctx.Err() != nil {
			return zero, net.ErrClosed
		}
		expired, changed, timer := d.wait()
		if expired != nil {
			select {
			case <-expired:
				if timer != nil {
					timer.Stop()
				}
				return zero, os.ErrDeadlineExceeded
			default:
			}
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return zero, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-expired:
			if timer != nil {
				timer.Stop()
			}
			return zero, os.ErrDeadlineExceeded
		case value, ok := <-result:
			if timer != nil {
				timer.Stop()
			}
			if !ok {
				return zero, io.EOF
			}
			return value, nil
		}
	}
}
