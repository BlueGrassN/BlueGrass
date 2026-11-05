package tunengine

import (
	"bluegrass/native/core/routingpolicy"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestRealXrayStackFlowsThroughAuthorizedDirectSocket(t *testing.T) {
	rules, err := routingpolicy.Load()
	if err != nil {
		t.Fatal(err)
	}
	out, peer := net.Pipe()
	defer out.Close()
	defer peer.Close()
	go func() { _, _ = io.Copy(peer, peer) }()
	g := newSessionGate()
	d := &authorizedDirect{base: &regressionStreamDialer{conn: out}, session: context.Background(), gate: g,
		allow: func(network string, endpoint netip.AddrPort) error {
			if network != "tcp" || endpoint != netip.MustParseAddrPort("223.5.5.5:443") {
				return errors.New("unexpected direct endpoint")
			}
			return nil
		},
		release: func(string, netip.AddrPort) error { return nil }}
	r := &nativeRouter{ctx: context.Background(), gate: g, config: policyOptions(t, false), routingMode: routingpolicy.BypassCN, rules: rules, directs: map[string]N.Dialer{"domestic": d}}
	app := memoryApplication(t, r, g)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := app.DialTCP(ctx, netip.MustParseAddrPort("223.5.5.5:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	payload := bytes.Repeat([]byte("direct-data"), 500)
	stackIO(t, stream, func() error {
		if _, err := stream.Write(payload); err != nil {
			return err
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, got); err != nil {
			return err
		}
		if !bytes.Equal(got, payload) {
			return errors.New("direct stream changed")
		}
		return nil
	})
}

type directFixture struct {
	calls   int
	connect func() net.Conn
}

func (d *directFixture) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	d.calls++
	return d.connect(), nil
}
func (*directFixture) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unexpected packet listener")
}

func TestDirectSocketOwnsExactPermissionUntilClose(t *testing.T) {
	for _, mode := range []string{"normal", "allow-denied", "cancel-after-grant", "cancel-during-dial", "gate-retire"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			g := newSessionGate()
			g.open()
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			f := &directFixture{connect: func() net.Conn {
				if mode == "cancel-during-dial" {
					cancel()
				}
				return client
			}}
			allowed, released := 0, 0
			endpoint := netip.MustParseAddrPort("223.5.5.5:443")
			d := &authorizedDirect{base: f, session: ctx, gate: g, allow: func(network string, ep netip.AddrPort) error {
				if network != "tcp" || ep != endpoint || f.calls != 0 {
					t.Error("grant not precise or happened after dial")
				}
				if mode == "allow-denied" {
					return errors.New("fixture")
				}
				allowed++
				if mode == "cancel-after-grant" {
					cancel()
				}
				return nil
			}, release: func(network string, ep netip.AddrPort) error {
				if network != "tcp" || ep != endpoint {
					t.Error("wrong release")
				}
				released++
				return nil
			}}
			conn, err := d.DialContext(ctx, "tcp", M.ParseSocksaddr(endpoint.String()))
			if mode == "normal" || mode == "gate-retire" {
				if err != nil || conn == nil || allowed != 1 || released != 0 {
					t.Fatalf("premature release: %v", err)
				}
				if mode == "gate-retire" {
					g.retire()
				}
				_ = conn.Close()
				_ = conn.Close()
			} else if err == nil || conn != nil {
				t.Fatal("failed/canceled flow accepted")
			}
			if released != allowed {
				t.Fatalf("grant leak: allowed=%d released=%d", allowed, released)
			}
			if (mode == "allow-denied" || mode == "cancel-after-grant") && f.calls != 0 {
				t.Fatal("dial after rejection")
			}
		})
	}
}

func TestDirectNeverAcceptsUnsafeTargetsOrMissingRelease(t *testing.T) {
	g := newSessionGate()
	g.open()
	d := &authorizedDirect{session: context.Background(), gate: g, allow: func(string, netip.AddrPort) error { t.Fatal("unsafe grant"); return nil }, release: func(string, netip.AddrPort) error { return nil }}
	for _, target := range []string{"192.168.1.1:443", "100.64.0.1:443", "198.18.0.1:443", "domain.example:443", "[::ffff:223.5.5.5]:443", "[2001:4860::1]:443", "223.5.5.5:0"} {
		if conn, err := d.DialContext(context.Background(), "tcp", M.ParseSocksaddr(target)); err == nil || conn != nil {
			t.Fatal("unsafe direct target accepted")
		}
	}
	d.release = nil
	if _, err := d.DialContext(context.Background(), "tcp", M.ParseSocksaddr("223.5.5.5:443")); err == nil {
		t.Fatal("unreleasable permission accepted")
	}
}
