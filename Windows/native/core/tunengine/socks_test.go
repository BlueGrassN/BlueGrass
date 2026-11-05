package tunengine

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bluegrass/native/core/protocolconfig"
	M "github.com/sagernet/sing/common/metadata"
)

// No listeners or OS UDP sockets: the real pinned SOCKS client exchanges wire
// messages over net.Pipe. Only the last physical dial is replaced by a fixture.
type relayFixture struct {
	client, server net.Conn
	done           chan error
	udpPeer        net.Conn
	udpReady       chan net.Conn
	udpDials       int
	udpDestination M.Socksaddr
	beforeUDP      func()
}

type relayReply struct {
	bind       M.Socksaddr
	method     byte
	authStatus byte
	replyCode  byte
	mappedWire bool
}

func goodRelayReply() relayReply {
	return relayReply{bind: M.ParseSocksaddr("8.8.8.8:42000"), method: byte(2)}
}

func newRelayFixture(t *testing.T, reply relayReply) *relayFixture {
	t.Helper()
	f := &relayFixture{done: make(chan error, 1), udpReady: make(chan net.Conn, 1)}
	f.client, f.server = net.Pipe()
	_ = f.server.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() {
		_ = f.client.Close()
		_ = f.server.Close()
		if f.udpPeer != nil {
			_ = f.udpPeer.Close()
		}
	})
	go func() {
		defer f.server.Close()
		f.done <- f.serve(reply)
	}()
	return f
}

func (f *relayFixture) serve(reply relayReply) error {
	var greeting [3]byte
	if _, err := io.ReadFull(f.server, greeting[:]); err != nil {
		return err
	}
	if greeting != [3]byte{5, 1, 2} {
		return errors.New("fixture expected authenticated client")
	}
	var err error
	// Deliberately fragment the negotiation response to exercise stream framing.
	if _, err = f.server.Write([]byte{5}); err != nil {
		return err
	}
	if _, err = f.server.Write([]byte{reply.method}); err != nil {
		return err
	}
	if reply.method == byte(2) {
		var header [2]byte
		if _, err = io.ReadFull(f.server, header[:]); err != nil {
			return err
		}
		username := make([]byte, int(header[1]))
		if _, err = io.ReadFull(f.server, username); err != nil {
			return err
		}
		var length [1]byte
		if _, err = io.ReadFull(f.server, length[:]); err != nil {
			return err
		}
		password := make([]byte, int(length[0]))
		if _, err = io.ReadFull(f.server, password); err != nil {
			return err
		}
		if header[0] != 1 || string(username) != "fixture-user" || string(password) != "fixture-password" {
			return errors.New("fixture credentials differ")
		}
		if _, err = f.server.Write([]byte{1, reply.authStatus}); err != nil {
			return err
		}
	}
	if reply.method == byte(2) && reply.authStatus == 0 {
		var header [4]byte
		if _, err = io.ReadFull(f.server, header[:]); err != nil {
			return err
		}
		if header[0] != 5 || header[1] != 3 && header[1] != 1 {
			return errors.New("unexpected command")
		}
		size := 0
		switch header[3] {
		case 1:
			size = 4
		case 4:
			size = 16
		case 3:
			var l [1]byte
			if _, err = io.ReadFull(f.server, l[:]); err != nil {
				return err
			}
			size = int(l[0])
		default:
			return errors.New("unexpected address type")
		}
		if _, err = io.CopyN(io.Discard, f.server, int64(size+2)); err != nil {
			return err
		}
		if reply.mappedWire {
			// Upstream's address decoder Unwraps mapped IPv6. Test the wire ATYP,
			// so an IPv6 BND.ADDR cannot masquerade as an IPv4 literal.
			wire := []byte{5, reply.replyCode, 0, 4}
			a := netip.MustParseAddr("::ffff:8.8.8.8").As16()
			wire = append(wire, a[:]...)
			wire = binary.BigEndian.AppendUint16(wire, reply.bind.Port)
			_, err = f.server.Write(wire)
		} else {
			wire := []byte{5, reply.replyCode, 0}
			if reply.bind.Fqdn != "" {
				wire = append(wire, 3, byte(len(reply.bind.Fqdn)))
				wire = append(wire, reply.bind.Fqdn...)
			} else if reply.bind.Addr.Is4() {
				wire = append(wire, 1)
				wire = append(wire, reply.bind.Addr.AsSlice()...)
			} else {
				wire = append(wire, 4)
				wire = append(wire, reply.bind.Addr.AsSlice()...)
			}
			wire = binary.BigEndian.AppendUint16(wire, reply.bind.Port)
			_, err = f.server.Write(wire)
		}
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return err
		}
	}
	var one [1]byte
	_, err = f.server.Read(one[:])
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func (f *relayFixture) DialContext(_ context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if network == "tcp" {
		if destination != M.ParseSocksaddr("8.8.8.8:1080") {
			return nil, errors.New("unexpected physical TCP destination")
		}
		return f.client, nil
	}
	if f.beforeUDP != nil {
		f.beforeUDP()
	}
	f.udpDials++
	f.udpDestination = destination
	client, peer := net.Pipe()
	f.udpPeer = peer
	if f.udpReady != nil {
		f.udpReady <- peer
	}
	return client, nil
}

func (*relayFixture) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unguarded UDP listener")
}

func relayOutboundForTest(t *testing.T, f *relayFixture, g *sessionGate, session context.Context, allow func(netip.AddrPort) error) *guardedSOCKS {
	t.Helper()
	h := newGuardedSOCKS(session, preparedConfig{entry: netip.MustParseAddrPort("8.8.8.8:1080"), allowUDPRelay: allow}, g, "fixture-user", "fixture-password", f)
	selected, err := protocolconfig.Build(protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: "fixture-user", Password: "fixture-password"}}, h.entry)
	if err != nil {
		t.Fatal(err)
	}
	outbound, err := newXrayOutbound(session, selected)
	if err != nil {
		t.Fatal(err)
	}
	if err = outbound.start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outbound.Close() })
	h.selected = outbound
	return h
}

func assertAssociationClosed(t *testing.T, f *relayFixture) {
	t.Helper()
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal("fixture handshake or association teardown failed:", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TCP association survived teardown")
	}
}

func TestSOCKSRelayAuthorizesActualBindBeforeUDPDial(t *testing.T) {
	for _, listen := range []bool{false, true} {
		for _, port := range []uint16{42000, 42001} {
			reply := goodRelayReply()
			reply.bind.Port = port
			f := newRelayFixture(t, reply)
			g := newSessionGate()
			g.open()
			calls := 0
			h := relayOutboundForTest(t, f, g, context.Background(), func(endpoint netip.AddrPort) error {
				calls++
				if endpoint != netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), port) || f.udpDials != 0 {
					t.Error("authorization did not receive actual response.Bind before socket creation")
				}
				return nil
			})
			var conn io.Closer
			var err error
			if listen {
				conn, err = h.ListenPacket(context.Background(), M.ParseSocksaddr("9.9.9.9:443"))
			} else {
				conn, err = h.DialContext(context.Background(), "udp", M.ParseSocksaddr("9.9.9.9:443"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || f.udpDials != 1 || f.udpDestination != reply.bind {
				t.Fatal("relay callback/dial count or destination differs")
			}
			g.retire()
			assertAssociationClosed(t, f)
			_ = f.udpPeer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := f.udpPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatal("UDP socket survived gate retirement")
			}
			_ = conn.Close()
		}
	}
}

func TestSOCKSRelayAllowsAuthenticatedPublicRelayOnDifferentHost(t *testing.T) {
	reply := goodRelayReply()
	reply.bind = M.ParseSocksaddr("9.9.9.9:42000")
	f := newRelayFixture(t, reply)
	g := newSessionGate()
	g.open()
	calls := 0
	h := relayOutboundForTest(t, f, g, context.Background(), func(endpoint netip.AddrPort) error {
		calls++
		if endpoint != reply.bind.AddrPort() || f.udpDials != 0 {
			t.Fatal("permission does not match authenticated relay")
		}
		return nil
	})
	packet, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("1.1.1.1:3478"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || f.udpDials != 1 || f.udpDestination != reply.bind {
		t.Fatal("relay was substituted or permission was broadened")
	}
	_ = packet.Close()
	assertAssociationClosed(t, f)
}

func TestSOCKSRelayRejectsUnsafeBindAndUnauthenticatedReplies(t *testing.T) {
	cases := map[string]func(*relayReply){
		"private IP":           func(r *relayReply) { r.bind = M.ParseSocksaddr("192.168.1.1:42000") },
		"CGNAT IP":             func(r *relayReply) { r.bind = M.ParseSocksaddr("100.64.0.1:42000") },
		"virtual IP":           func(r *relayReply) { r.bind = M.ParseSocksaddr("198.18.0.1:42000") },
		"domain":               func(r *relayReply) { r.bind = M.ParseSocksaddr("relay.example:42000") },
		"IP spelled as domain": func(r *relayReply) { r.bind = M.Socksaddr{Fqdn: "8.8.8.8", Port: 42000} },
		"IPv6":                 func(r *relayReply) { r.bind = M.ParseSocksaddr("[2001:4860:4860::8888]:42000") },
		"mapped wire IPv6":     func(r *relayReply) { r.mappedWire = true },
		"unspecified":          func(r *relayReply) { r.bind = M.ParseSocksaddr("0.0.0.0:42000") },
		"zero port":            func(r *relayReply) { r.bind.Port = 0 },
		"failed auth":          func(r *relayReply) { r.authStatus = byte(1) },
		"auth downgrade":       func(r *relayReply) { r.method = byte(0) },
		"rejected associate":   func(r *relayReply) { r.replyCode = byte(2) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			reply := goodRelayReply()
			change(&reply)
			f := newRelayFixture(t, reply)
			g := newSessionGate()
			g.open()
			calls := 0
			h := relayOutboundForTest(t, f, g, context.Background(), func(netip.AddrPort) error { calls++; return nil })
			conn, err := h.DialContext(context.Background(), "udp", M.ParseSocksaddr("9.9.9.9:443"))
			if err == nil || conn != nil || calls != 0 || f.udpDials != 0 {
				t.Fatal("unsafe/unauthenticated relay authorized or dialed")
			}
			assertAssociationClosed(t, f)
		})
	}
}

func TestSOCKSRelayAuthorizationFailureAndConcurrentBlock(t *testing.T) {
	for _, mode := range []string{"denied", "block", "request canceled", "session canceled", "block while dialing"} {
		t.Run(mode, func(t *testing.T) {
			f := newRelayFixture(t, goodRelayReply())
			g := newSessionGate()
			g.open()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session, cancelSession := context.WithCancel(context.Background())
			defer cancelSession()
			entered, proceed := make(chan struct{}), make(chan struct{})
			h := relayOutboundForTest(t, f, g, session, func(netip.AddrPort) error {
				close(entered)
				<-proceed
				if mode == "denied" {
					return errors.New("secret-from-host-must-not-escape")
				}
				return nil
			})
			if mode == "block while dialing" {
				f.beforeUDP = g.retire
			}
			done := make(chan error, 1)
			go func() {
				conn, err := h.DialContext(ctx, "udp", M.ParseSocksaddr("9.9.9.9:443"))
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("relay callback not reached")
			}
			switch mode {
			case "block":
				g.retire()
			case "request canceled":
				cancel()
			case "session canceled":
				cancelSession()
			}
			close(proceed)
			err := <-done
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("UDP denial/cancellation lost or raw host error escaped")
			}
			if mode != "block while dialing" && f.udpDials != 0 {
				t.Fatal("created UDP socket after rejected/stale authorization")
			}
			assertAssociationClosed(t, f)
			if f.udpPeer != nil {
				_ = f.udpPeer.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := f.udpPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
					t.Fatal("late socket was not closed")
				}
			}
		})
	}
}

func TestSOCKSRelayRequiresOpenSessionAndCallback(t *testing.T) {
	for _, mode := range []string{"closed", "retired", "missing callback", "missing auth", "session canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newRelayFixture(t, goodRelayReply())
			g := newSessionGate()
			g.open()
			session, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			h := relayOutboundForTest(t, f, g, session, func(netip.AddrPort) error { calls.Add(1); return nil })
			switch mode {
			case "closed":
				h.gate = newSessionGate()
			case "retired":
				g.retire()
			case "missing callback":
				h.allowUDPRelay = nil
			case "missing auth":
				h.username = ""
			case "session canceled":
				cancel()
			}
			if conn, err := h.DialContext(context.Background(), "udp", M.ParseSocksaddr("9.9.9.9:443")); err == nil || conn != nil || calls.Load() != 0 || f.udpDials != 0 {
				t.Fatal("unverified/unconfigured UDP accepted")
			}
		})
	}
}

func TestSOCKSRelayNeverReusesAuthorizationAcrossAssociations(t *testing.T) {
	g := newSessionGate()
	g.open()
	defer g.retire()
	var endpoints []netip.AddrPort
	h := relayOutboundForTest(t, nil, g, context.Background(), func(endpoint netip.AddrPort) error {
		endpoints = append(endpoints, endpoint)
		return nil
	})
	for _, peer := range []string{"8.8.8.8:42000", "9.9.9.9:42000", "8.8.8.8:42001", "8.8.8.8:42000"} {
		reply := goodRelayReply()
		reply.bind = M.ParseSocksaddr(peer)
		f := newRelayFixture(t, reply)
		h.base = f
		before := len(endpoints)
		conn, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("1.1.1.1:443"))
		if err != nil || len(endpoints) != before+1 || endpoints[before] != reply.bind.AddrPort() || f.udpDials != 1 {
			t.Fatal("association did not independently authorize its actual bind")
		}
		_ = conn.Close()
		assertAssociationClosed(t, f)
	}
}

func TestSOCKSRelayCallerCancellationClosesAssociation(t *testing.T) {
	f := newRelayFixture(t, goodRelayReply())
	g := newSessionGate()
	g.open()
	defer g.retire()
	h := relayOutboundForTest(t, f, g, context.Background(), func(netip.AddrPort) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := h.ListenPacket(ctx, M.ParseSocksaddr("9.9.9.9:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cancel()
	_ = f.udpPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := f.udpPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal("caller cancellation left physical relay open", err)
	}
	assertAssociationClosed(t, f)
}
