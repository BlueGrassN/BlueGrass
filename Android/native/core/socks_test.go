package bridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

// These fixtures implement only physical I/O. The real SOCKS client, wire
// parsers, registry constructor and guard run without any OS network sockets.
type socksScriptConn struct {
	mu       sync.Mutex
	reply    []byte
	writes   [][]byte
	chunk    int
	eof      bool
	ioErr    error
	closed   chan struct{}
	waiting  chan struct{}
	waitOnce sync.Once
	closes   int
}

func newSOCKSScript(reply []byte) *socksScriptConn {
	return &socksScriptConn{reply: bytes.Clone(reply), chunk: 1, closed: make(chan struct{}), waiting: make(chan struct{})}
}

func (c *socksScriptConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return 0, net.ErrClosed
	default:
	}
	if len(c.reply) > 0 {
		n := min(len(p), len(c.reply), c.chunk)
		copy(p, c.reply[:n])
		c.reply = c.reply[n:]
		c.mu.Unlock()
		return n, nil
	}
	eof, err := c.eof, c.ioErr
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if eof {
		return 0, io.EOF
	}
	c.waitOnce.Do(func() { close(c.waiting) })
	<-c.closed
	return 0, net.ErrClosed
}

func (c *socksScriptConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	if c.ioErr != nil {
		return 0, c.ioErr
	}
	c.writes = append(c.writes, bytes.Clone(p))
	return len(p), nil
}

func (c *socksScriptConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (*socksScriptConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*socksScriptConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*socksScriptConn) SetDeadline(time.Time) error      { return nil }
func (*socksScriptConn) SetReadDeadline(time.Time) error  { return nil }
func (*socksScriptConn) SetWriteDeadline(time.Time) error { return nil }

type socksDialRecord struct {
	network string
	address M.Socksaddr
}

type socksScriptDialer struct {
	tcp, udp  *socksScriptConn
	calls     []socksDialRecord
	listeners int
	before    func(context.Context, string) error
}

func (d *socksScriptDialer) DialContext(ctx context.Context, network string, address M.Socksaddr) (net.Conn, error) {
	d.calls = append(d.calls, socksDialRecord{network, address})
	if d.before != nil {
		if err := d.before(ctx, network); err != nil {
			return nil, err
		}
	}
	if network == "tcp" {
		return d.tcp, nil
	}
	if network == "udp" {
		return d.udp, nil
	}
	return nil, errors.New("fixture unexpected network")
}

func (d *socksScriptDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	d.listeners++
	return nil, errors.New("fixture unguarded listener")
}

func socksOptionsForTest(auth bool) socksOptions {
	o := socksOptions{Server: "8.8.8.8", ServerPort: 1080}
	if auth {
		o.Username, o.Password = "fixture-user", "fixture-password"
	}
	return o
}

func socksOutboundForTest(t *testing.T, session context.Context, options socksOptions, base *socksScriptDialer) *guardedSOCKS {
	t.Helper()
	h, err := newGuardedSOCKS(session, options)
	if err != nil {
		t.Fatal(err)
	}
	h.base = base
	return h
}

func socksWireReply(auth bool, bind []byte) []byte {
	wire := []byte{5, 0}
	if auth {
		wire = []byte{5, 2, 1, 0}
	}
	wire = append(wire, 5, 0, 0)
	return append(wire, bind...)
}

func socksIPv4Bind(ip [4]byte, port uint16) []byte {
	return binary.BigEndian.AppendUint16(append([]byte{1}, ip[:]...), port)
}

func socksFixture(t *testing.T, wire []byte) *socksScriptDialer {
	t.Helper()
	d := &socksScriptDialer{tcp: newSOCKSScript(wire), udp: newSOCKSScript(nil)}
	d.udp.chunk = 65535
	t.Cleanup(func() {
		_ = d.tcp.Close()
		_ = d.udp.Close()
	})
	return d
}

func socksWait(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func assertSOCKSError(t *testing.T, err error, udp bool) {
	t.Helper()
	want := "SOCKS_CONNECT_FAILED"
	if udp {
		want = "UDP_RELAY_DENIED"
	}
	if err == nil || err.Error() != want {
		t.Fatalf("expected fixed error %s, got %v", want, err)
	}
}

func TestGuardedSOCKSConnectAssociateAndPacketFraming(t *testing.T) {
	for _, auth := range []bool{false, true} {
		for _, mode := range []string{"tcp", "udp", "listen"} {
			t.Run(mode+map[bool]string{true: "/auth", false: "/noauth"}[auth], func(t *testing.T) {
				bind := socksIPv4Bind([4]byte{8, 8, 8, 8}, 42001)
				if mode == "tcp" {
					// TCP's bound endpoint is informational, not a physical dial target.
					bind = socksIPv4Bind([4]byte{}, 0)
				}
				d := socksFixture(t, socksWireReply(auth, bind))
				h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(auth), d)
				destination := M.ParseSocksaddr("9.9.9.9:443")
				var conn net.Conn
				var packet net.PacketConn
				var err error
				if mode == "listen" {
					packet, err = h.ListenPacket(context.Background(), destination)
				} else {
					conn, err = h.DialContext(context.Background(), mode, destination)
				}
				if err != nil {
					t.Fatal(err)
				}
				if d.listeners != 0 || d.calls[0] != (socksDialRecord{"tcp", M.ParseSocksaddr("8.8.8.8:1080")}) {
					t.Fatal("physical entry pin or listener confinement lost")
				}
				wantOffer := []byte{5, 1, 0}
				commandIndex := 1
				if auth {
					wantOffer = []byte{5, 1, 2}
					commandIndex = 2
					wantAuth := append([]byte{1, 12}, []byte("fixture-user")...)
					wantAuth = append(wantAuth, 16)
					wantAuth = append(wantAuth, []byte("fixture-password")...)
					if !bytes.Equal(d.tcp.writes[1], wantAuth) {
						t.Fatal("RFC1929 request differs")
					}
				}
				if !bytes.Equal(d.tcp.writes[0], wantOffer) {
					t.Fatal("client offered an unexpected authentication method")
				}
				wantCommand := []byte{5, 1, 0, 1, 9, 9, 9, 9, 1, 187}
				if mode != "tcp" {
					wantCommand = []byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}
					if len(d.calls) != 2 || d.calls[1] != (socksDialRecord{"udp", M.ParseSocksaddr("8.8.8.8:42001")}) {
						t.Fatal("UDP did not dial the exact BND endpoint")
					}
				} else if len(d.calls) != 1 {
					t.Fatal("TCP opened extra physical sockets")
				}
				if !bytes.Equal(d.tcp.writes[commandIndex], wantCommand) {
					t.Fatal("wrong SOCKS command or logical destination")
				}
				if packet != nil {
					_, err = packet.WriteTo([]byte("ping"), destination.UDPAddr())
				} else {
					_, err = conn.Write([]byte("ping"))
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode != "tcp" {
					wantPacket := []byte{0, 0, 0, 1, 9, 9, 9, 9, 1, 187, 'p', 'i', 'n', 'g'}
					if !bytes.Equal(d.udp.writes[0], wantPacket) {
						t.Fatal("SOCKS UDP framing or packet destination lost")
					}
					d.udp.reply = bytes.Clone(wantPacket)
				} else {
					d.tcp.reply = []byte("ping")
				}
				payload := make([]byte, 64)
				var n int
				if packet != nil {
					var peer net.Addr
					n, peer, err = packet.ReadFrom(payload)
					if err == nil && peer.String() != "9.9.9.9:443" {
						t.Fatal("packet source lost")
					}
				} else if mode == "tcp" {
					n, err = io.ReadFull(conn, payload[:4])
				} else {
					n, err = conn.Read(payload)
				}
				if err != nil || string(payload[:n]) != "ping" {
					t.Fatal("post-handshake payload was rejected or corrupted", err)
				}
				if packet != nil {
					_ = packet.Close()
				} else {
					_ = conn.Close()
				}
				socksWait(t, d.tcp.closed, "TCP association survived Close")
				if mode != "tcp" {
					socksWait(t, d.udp.closed, "UDP socket survived Close")
				}
			})
		}
	}
}

func TestGuardedSOCKSRejectsNegotiationDowngradesAndMalformedHeaders(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		for _, tc := range []struct {
			name  string
			auth  bool
			at    int
			value byte
		}{
			{"credential downgrade", true, 1, 0},
			{"unsolicited auth", false, 1, 2},
			{"unknown method", true, 1, 1},
			{"no acceptable method", true, 1, 255},
			{"method version", true, 0, 4},
			{"noauth method version", false, 0, 4},
			{"auth version", true, 2, 5},
			{"auth failed", true, 3, 1},
			{"reply version", true, 4, 4},
			{"reply rejected", true, 5, 2},
			{"reserved byte", true, 6, 1},
			{"noauth reply version", false, 2, 4},
			{"noauth reply rejected", false, 3, 2},
			{"noauth reserved byte", false, 4, 1},
		} {
			t.Run(network+"/"+tc.name, func(t *testing.T) {
				wire := socksWireReply(tc.auth, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
				if tc.name == "credential downgrade" {
					wire = socksWireReply(false, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
				}
				if tc.name == "unsolicited auth" {
					wire = socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
				}
				wire[tc.at] = tc.value
				d := socksFixture(t, wire)
				h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(tc.auth), d)
				conn, err := h.DialContext(context.Background(), network, M.ParseSocksaddr("probe.example:443"))
				assertSOCKSError(t, err, network == "udp")
				if conn != nil || len(d.calls) != 1 || d.listeners != 0 {
					t.Fatal("malformed handshake opened a transport")
				}
				socksWait(t, d.tcp.closed, "failed handshake leaked TCP")
			})
		}
	}
}

func TestGuardedSOCKSAuthenticatedRelayUsesActualPublicBind(t *testing.T) {
	for _, authenticated := range []bool{true, false} {
		t.Run(map[bool]string{true: "authenticated", false: "unauthenticated"}[authenticated], func(t *testing.T) {
			d := socksFixture(t, socksWireReply(authenticated, socksIPv4Bind([4]byte{9, 9, 9, 9}, 42000)))
			h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(authenticated), d)
			conn, err := h.DialContext(context.Background(), "udp", M.ParseSocksaddr("1.1.1.1:3478"))
			if !authenticated {
				assertSOCKSError(t, err, true)
				if conn != nil || len(d.calls) != 1 {
					t.Fatal("unauthenticated reply authorized a new relay")
				}
				return
			}
			if err != nil {
				t.Fatal("authenticated public relay must be usable:", err)
			}
			defer conn.Close()
			if len(d.calls) != 2 || d.calls[1] != (socksDialRecord{"udp", M.ParseSocksaddr("9.9.9.9:42000")}) {
				t.Fatal("UDP did not use the exact authenticated relay tuple")
			}
			if _, err := conn.Write([]byte("ping")); err != nil {
				t.Fatal(err)
			}
			want := []byte{0, 0, 0, 1, 1, 1, 1, 1, 13, 150, 'p', 'i', 'n', 'g'}
			if !bytes.Equal(d.udp.writes[0], want) {
				t.Fatal("logical destination was replaced by relay")
			}
		})
	}
}

func TestGuardedSOCKSRejectsUnsafeUDPWireBind(t *testing.T) {
	binds := map[string][]byte{
		"unspecified":            socksIPv4Bind([4]byte{}, 42000),
		"loopback":               socksIPv4Bind([4]byte{127, 0, 0, 1}, 42000),
		"LAN":                    socksIPv4Bind([4]byte{192, 168, 1, 1}, 42000),
		"link local":             socksIPv4Bind([4]byte{169, 254, 1, 1}, 42000),
		"zero port":              socksIPv4Bind([4]byte{8, 8, 8, 8}, 0),
		"domain":                 append(append([]byte{3, 13}, []byte("relay.example")...), 164, 16),
		"IPv4 spelled as domain": append(append([]byte{3, 7}, []byte("8.8.8.8")...), 164, 16),
	}
	for _, ip := range []string{"::ffff:8.8.8.8", "2001:4860:4860::8888"} {
		address := netip.MustParseAddr(ip).As16()
		binds[ip] = append(append([]byte{4}, address[:]...), 164, 16)
	}
	for _, auth := range []bool{false, true} {
		for name, bind := range binds {
			t.Run(name+map[bool]string{true: "/auth", false: "/noauth"}[auth], func(t *testing.T) {
				d := socksFixture(t, socksWireReply(auth, bind))
				h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(auth), d)
				conn, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
				assertSOCKSError(t, err, true)
				if conn != nil || len(d.calls) != 1 || d.listeners != 0 {
					t.Fatal("unsafe BND endpoint reached a UDP socket")
				}
				socksWait(t, d.tcp.closed, "unsafe association leaked TCP")
			})
		}
	}
}

func TestGuardedSOCKSRejectsTruncatedHandshake(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		wire := socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
		for end := 0; end < len(wire); end++ {
			d := socksFixture(t, wire[:end])
			d.tcp.eof = true
			h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), d)
			conn, err := h.DialContext(context.Background(), network, M.ParseSocksaddr("9.9.9.9:443"))
			assertSOCKSError(t, err, network == "udp")
			if conn != nil || len(d.calls) != 1 {
				t.Fatal("truncated handshake was accepted")
			}
			socksWait(t, d.tcp.closed, "truncated handshake leaked TCP")
		}
	}
}

func TestGuardedSOCKSRegistryRejectsUnboundedOptions(t *testing.T) {
	cases := map[string]func(*socksOptions){
		"domain":        func(o *socksOptions) { o.Server = "entry.example" },
		"loopback":      func(o *socksOptions) { o.Server = "127.0.0.1" },
		"LAN":           func(o *socksOptions) { o.Server = "192.168.1.1" },
		"CGNAT":         func(o *socksOptions) { o.Server = "100.64.1.1" },
		"documentation": func(o *socksOptions) { o.Server = "192.0.2.1" },
		"unspecified":   func(o *socksOptions) { o.Server = "0.0.0.0" },
		"mapped IPv6":   func(o *socksOptions) { o.Server = "::ffff:8.8.8.8" },
		"IPv6":          func(o *socksOptions) { o.Server = "2001:4860:4860::8888" },
		"zero port":     func(o *socksOptions) { o.ServerPort = 0 },
		"password only": func(o *socksOptions) { o.Username = "" },
		"username only": func(o *socksOptions) { o.Password = "" },
		"long username": func(o *socksOptions) { o.Username = strings.Repeat("u", 256) },
		"long password": func(o *socksOptions) { o.Password = strings.Repeat("p", 256) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			o := socksOptionsForTest(true)
			change(&o)
			value, err := newGuardedSOCKS(context.Background(), o)
			assertSOCKSError(t, err, false)
			if value != nil {
				t.Fatal("unsafe constructor returned an outbound")
			}
		})
	}
}

func TestGuardedSOCKSCancellationDuringEstablishmentAndSessionLifetime(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		for _, source := range []string{"call", "session"} {
			for _, phase := range []string{"before dial", "dial pending", "handshake", "late TCP", "late UDP", "established"} {
				if network == "tcp" && phase == "late UDP" {
					continue
				}
				t.Run(network+"/"+source+"/"+phase, func(t *testing.T) {
					session, cancelSession := context.WithCancel(context.Background())
					defer cancelSession()
					call, cancelCall := context.WithCancel(context.Background())
					defer cancelCall()
					cancel := cancelCall
					if source == "session" {
						cancel = cancelSession
					}
					wire := socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
					if phase == "handshake" {
						wire = nil
					}
					d := socksFixture(t, wire)
					h := socksOutboundForTest(t, session, socksOptionsForTest(true), d)
					entered := make(chan struct{})
					if phase == "dial pending" {
						d.before = func(ctx context.Context, _ string) error {
							close(entered)
							<-ctx.Done()
							return errors.New("secret-dial-error")
						}
					}
					if phase == "late TCP" || phase == "late UDP" {
						d.before = func(_ context.Context, net string) error {
							if phase == "late TCP" && net == "tcp" || phase == "late UDP" && net == "udp" {
								cancel()
							}
							return nil
						}
					}
					if phase == "before dial" {
						cancel()
					}
					type result struct {
						conn net.Conn
						err  error
					}
					done := make(chan result, 1)
					go func() {
						conn, err := h.DialContext(call, network, M.ParseSocksaddr("9.9.9.9:443"))
						done <- result{conn, err}
					}()
					if phase == "dial pending" {
						socksWait(t, entered, "dial was not entered")
						cancel()
					}
					if phase == "handshake" {
						socksWait(t, d.tcp.waiting, "handshake was not entered")
						cancel()
					}
					var got result
					select {
					case got = <-done:
					case <-time.After(3 * time.Second):
						t.Fatal("cancellation did not release pending operation")
					}
					if phase == "established" {
						if got.err != nil || got.conn == nil {
							t.Fatal("connection failed before cancellation", got.err)
						}
						defer got.conn.Close()
						cancel()
						if source == "call" {
							assertSOCKSPayloadAfterHandoff(t, got.conn, d, network)
							cancelSession()
						}
					} else {
						assertSOCKSError(t, got.err, network == "udp")
						if got.conn != nil {
							t.Fatal("canceled call returned live socket")
						}
					}
					if phase == "before dial" {
						if len(d.calls) != 0 {
							t.Fatal("canceled operation dialed")
						}
					} else if phase != "dial pending" {
						socksWait(t, d.tcp.closed, "TCP survived cancellation")
						if len(d.calls) > 1 {
							socksWait(t, d.udp.closed, "UDP survived cancellation")
						}
					}
				})
			}
		}
	}
}

func assertSOCKSPayloadAfterHandoff(t *testing.T, conn net.Conn, d *socksScriptDialer, network string) {
	t.Helper()
	physical := d.tcp
	reply := []byte("pong")
	if network == "udp" {
		physical = d.udp
		reply = []byte{0, 0, 0, 1, 9, 9, 9, 9, 1, 187, 'p', 'o', 'n', 'g'}
	}
	physical.mu.Lock()
	physical.reply = reply
	physical.mu.Unlock()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal("caller cancellation broke an established write:", err)
	}
	payload := make([]byte, 64)
	var n int
	var err error
	if network == "tcp" {
		n, err = io.ReadFull(conn, payload[:4])
	} else {
		n, err = conn.Read(payload)
	}
	if err != nil || string(payload[:n]) != "pong" {
		t.Fatal("caller cancellation broke an established read:", err)
	}
}

func TestGuardedSOCKSHandoffDetachesCallerCancellation(t *testing.T) {
	for _, auth := range []bool{false, true} {
		for _, network := range []string{"tcp", "udp"} {
			t.Run(network+map[bool]string{true: "/auth", false: "/noauth"}[auth], func(t *testing.T) {
				session, cancelSession := context.WithCancel(context.Background())
				defer cancelSession()
				type contextKey struct{}
				call, cancelCall := context.WithTimeout(context.WithValue(context.Background(), contextKey{}, "preserved"), time.Hour)
				defer cancelCall()
				d := socksFixture(t, socksWireReply(auth, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000)))
				var physicalCtx context.Context
				d.before = func(ctx context.Context, _ string) error { physicalCtx = ctx; return nil }
				h := socksOutboundForTest(t, session, socksOptionsForTest(auth), d)
				conn, err := h.DialContext(call, network, M.ParseSocksaddr("9.9.9.9:443"))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				// DNS pooling and the shared exit probe cancel their dial context
				// immediately after success, before performing application I/O.
				cancelCall()
				assertSOCKSPayloadAfterHandoff(t, conn, d, network)
				if physicalCtx.Value(contextKey{}) != "preserved" {
					t.Fatal("dial context values were lost")
				}
				if _, set := physicalCtx.Deadline(); set {
					t.Fatal("caller deadline remained attached to an established socket")
				}
				cancelSession()
				socksWait(t, d.tcp.closed, "TCP survived session cancellation")
				if network == "udp" {
					socksWait(t, d.udp.closed, "UDP survived session cancellation")
				}
			})
		}
	}
}

func TestGuardedSOCKSHandoffRacesCallerCancellation(t *testing.T) {
	// Exercise the ownership boundary directly: either cancellation wins and
	// rejects the handoff, or handoff wins and subsequent caller cancellation
	// cannot revoke the socket. All I/O remains scripted and in process.
	for i := 0; i < 200; i++ {
		call, cancelCall := context.WithCancel(context.Background())
		session, cancelSession := context.WithCancel(context.Background())
		lifetime := newSOCKSSocketLifetime(call, session, errSOCKSFailed)
		physical := newSOCKSScript([]byte("pong"))
		if !lifetime.track(physical) {
			t.Fatal("uncanceled lifetime rejected a socket")
		}
		conn := &socksLifetimeConn{Conn: physical, lifetime: lifetime}
		start, canceled := make(chan struct{}), make(chan struct{})
		go func() {
			<-start
			cancelCall()
			close(canceled)
		}()
		close(start)
		accepted := lifetime.handoff()
		<-canceled
		if accepted {
			// Synchronize with session teardown after the I/O assertions below;
			// the caller's callback must be unregistered for every accepted race.
			if _, err := conn.Write([]byte("ping")); err != nil {
				_ = conn.Close()
				cancelSession()
				t.Fatal("accepted handoff retained caller cancellation:", err)
			}
			var payload [4]byte
			if _, err := io.ReadFull(conn, payload[:]); err != nil || string(payload[:]) != "pong" {
				_ = conn.Close()
				cancelSession()
				t.Fatal("accepted handoff lost its readable socket:", err)
			}
		} else {
			_ = conn.Close()
		}
		cancelSession()
		socksWait(t, physical.closed, "handoff race leaked a socket")
	}
}

func TestGuardedSOCKSDoesNotReuseAssociationEvidence(t *testing.T) {
	h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), nil)
	for _, ip := range [][4]byte{{8, 8, 8, 8}, {192, 168, 1, 1}, {8, 8, 8, 8}} {
		d := socksFixture(t, socksWireReply(true, socksIPv4Bind(ip, 42000)))
		h.base = d
		conn, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
		if ip[0] == 192 {
			assertSOCKSError(t, err, true)
			if conn != nil || len(d.calls) != 1 {
				t.Fatal("old handshake authorized foreign relay")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
		}
		socksWait(t, d.tcp.closed, "association leaked")
	}
}

func TestGuardedSOCKSMasksPhysicalErrorsAndClosesAssociation(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		for _, phase := range []string{"TCP dial", "handshake", "UDP dial"} {
			if network == "tcp" && phase == "UDP dial" {
				continue
			}
			t.Run(network+"/"+phase, func(t *testing.T) {
				d := socksFixture(t, socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000)))
				secret := errors.New("secret-credentials-physical-address")
				if phase == "handshake" {
					d.tcp.ioErr = secret
				} else {
					d.before = func(_ context.Context, net string) error {
						if phase == "TCP dial" && net == "tcp" || phase == "UDP dial" && net == "udp" {
							return secret
						}
						return nil
					}
				}
				h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), d)
				conn, err := h.DialContext(context.Background(), network, M.ParseSocksaddr("secret.example:443"))
				assertSOCKSError(t, err, network == "udp")
				if conn != nil {
					t.Fatal("failed physical operation returned socket")
				}
				if phase != "TCP dial" {
					socksWait(t, d.tcp.closed, "failed UDP operation leaked TCP")
				}
			})
		}
	}
}

func TestGuardedSOCKSTCPAssociationEndClosesUDP(t *testing.T) {
	for _, mode := range []string{"EOF", "unexpected data"} {
		t.Run(mode, func(t *testing.T) {
			wire := socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000))
			if mode == "unexpected data" {
				wire = append(wire, 1)
			}
			d := socksFixture(t, wire)
			d.tcp.eof = true
			h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), d)
			conn, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			socksWait(t, d.udp.closed, "UDP outlived its TCP association")
			socksWait(t, d.tcp.closed, "ended association was not closed locally")
		})
	}
}

func TestGuardedSOCKSRejectsUnknownNetworkBeforePhysicalDial(t *testing.T) {
	d := socksFixture(t, nil)
	h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(false), d)
	conn, err := h.DialContext(context.Background(), "secret-network", M.ParseSocksaddr("secret.example:443"))
	assertSOCKSError(t, err, false)
	if conn != nil || len(d.calls) != 0 || d.listeners != 0 {
		t.Fatal("unknown network reached a physical socket")
	}
}

func TestGuardedSOCKSSanitizesSocketTimeoutWithoutLosingTimeoutSemantics(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			d := socksFixture(t, socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000)))
			h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), d)
			conn, err := h.DialContext(context.Background(), network, M.ParseSocksaddr("9.9.9.9:443"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			physical := d.tcp
			if network == "udp" {
				physical = d.udp
			}
			physical.mu.Lock()
			physical.ioErr = &net.OpError{Op: "secret-operation", Net: "secret-network", Err: os.ErrDeadlineExceeded}
			physical.mu.Unlock()
			_, readErr := conn.Read(make([]byte, 64))
			_, writeErr := conn.Write([]byte("ping"))
			for _, got := range []error{readErr, writeErr} {
				var timeout net.Error
				if !errors.Is(got, os.ErrDeadlineExceeded) || !errors.As(got, &timeout) || !timeout.Timeout() || strings.Contains(got.Error(), "secret") {
					t.Fatalf("socket timeout lost its semantics or exposed physical details: %v", got)
				}
			}
		})
	}
}

func TestXraySOCKSPreservesLargeDatagramBoundaries(t *testing.T) {
	d := socksFixture(t, socksWireReply(true, socksIPv4Bind([4]byte{8, 8, 8, 8}, 42000)))
	h := socksOutboundForTest(t, context.Background(), socksOptionsForTest(true), d)
	conn, err := h.DialContext(context.Background(), "udp", M.ParseSocksaddr("9.9.9.9:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := bytes.Repeat([]byte{42}, 32000)
	if n, err := conn.Write(payload); err != nil || n != len(payload) {
		t.Fatal("large datagram lost", n, err)
	}
	d.udp.reply = bytes.Clone(d.udp.writes[0])
	response := make([]byte, 65535)
	n, err := conn.Read(response)
	if err != nil || !bytes.Equal(response[:n], payload) {
		t.Fatal("large datagram boundary changed", n, err)
	}
}
