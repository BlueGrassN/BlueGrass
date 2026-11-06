package tunengine

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	tun "github.com/xtls/xray-core/proxy/tun"
)

func TestTUNCreatedOnActivationAndGateRemainsClosedDuringCreation(t *testing.T) {
	r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	if e.InterfaceLUID() != 0 {
		t.Fatal("Prepare created TUN")
	}
	if len(e.VirtualPrefixes()) != 3 || e.ResolvedEntry().String() != "8.8.8.8:1080" {
		t.Fatal("missing WFP preparation metadata")
	}
	if e.VirtualPrefixes()[2].String() != "198.18.0.0/15" {
		t.Fatal("WFP metadata omitted stale FakeIP candidate pool")
	}
	if _, err := e.ProbeExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.InterfaceLUID() != 0 {
		t.Fatal("probe created TUN")
	}
	if err := e.SetBusinessGate(true); err != nil {
		t.Fatal(err)
	}
	if e.InterfaceLUID() != 42 || r.enableSawOpen {
		t.Fatal("incorrect TUN activation boundary")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if e.InterfaceLUID() != 0 || e.Prepare(context.Background(), testProfile()) == nil {
		t.Fatal("closed engine reused")
	}
}

func TestPartialRuntimeFailureRetiresGateAndSanitizesErrors(t *testing.T) {
	for _, prepareFailure := range []bool{true, false} {
		r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
		secretErr := errors.New("do-not-log-credential")
		if prepareFailure {
			r.startErr = secretErr
		} else {
			r.enableErr = secretErr
		}
		e := newTestEngine(t, r)
		err := e.Prepare(context.Background(), testProfile())
		if !prepareFailure {
			if err != nil {
				t.Fatal(err)
			}
			_, _ = e.ProbeExit(context.Background())
			err = e.SetBusinessGate(true)
		}
		if err == nil || strings.Contains(err.Error(), "credential") {
			t.Fatal("missing or unsanitized startup error")
		}
		if e.VerifiedIPv4() != "" || e.InterfaceLUID() != 0 || !r.closed {
			t.Fatal("partial startup not rolled back")
		}
	}
}

func TestNativeOptionsRejectConflictingGlobalPolicy(t *testing.T) {
	for _, options := range []*tun.Config{
		{AutoOutboundsInterface: "auto"}, {AutoSystemDnsToGateway: true}, {AutoSystemWfpBlockLeak: []string{"dns"}}, nil,
	} {
		p := &nativePlatform{open: func(*tun.Config) (tun.Tun, uint64, error) { t.Fatal("native open must not run"); return nil, 0, nil }}
		if _, err := p.OpenInterface(options); err == nil {
			t.Fatal("accepted competing global policy")
		}
	}
}

type packetFixture struct {
	closed  chan struct{}
	once    sync.Once
	receive chan []byte
}

func (p *packetFixture) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	select {
	case <-p.closed:
		return M.Socksaddr{}, net.ErrClosed
	case data := <-p.receive:
		_, _ = b.Write(data)
		return M.Socksaddr{}, nil
	}
}
func (p *packetFixture) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }
func (p *packetFixture) Close() error                                   { p.once.Do(func() { close(p.closed) }); return nil }
func (*packetFixture) LocalAddr() net.Addr                              { return &net.UDPAddr{} }
func (*packetFixture) SetDeadline(time.Time) error                      { return nil }
func (*packetFixture) SetReadDeadline(time.Time) error                  { return nil }
func (*packetFixture) SetWriteDeadline(time.Time) error                 { return nil }

func TestUDPAccountingAndSynchronousTeardown(t *testing.T) {
	g := newSessionGate()
	if !g.open() {
		t.Fatal("open")
	}
	p := &packetFixture{closed: make(chan struct{}), receive: make(chan []byte, 1)}
	if !g.track(p) {
		t.Fatal("track")
	}
	c := &countedPacketConn{PacketConn: p, gate: g}
	p.receive <- []byte("upload")
	b := buf.NewSize(64)
	defer b.Release()
	if _, err := c.ReadPacket(b); err != nil {
		t.Fatal(err)
	}
	if err := c.WritePacket(buf.As([]byte("down")), M.Socksaddr{}); err != nil {
		t.Fatal(err)
	}
	if g.upload.Load() != 6 || g.download.Load() != 4 {
		t.Fatal("wrong UDP byte direction")
	}
	finished := make(chan error, 1)
	go func() { temp := buf.NewSize(64); defer temp.Release(); _, err := c.ReadPacket(temp); finished <- err }()
	g.retire()
	if err := <-finished; err == nil {
		t.Fatal("active UDP survived teardown")
	}
	if g.open() || g.track(p) {
		t.Fatal("retired gate reopened")
	}
}

func TestChangedObservationAndDetectorFailurePreserveAlreadyOpenGate(t *testing.T) {
	for _, observed := range []string{"8.8.4.4", "1.1.1.1"} {
		t.Run(observed, func(t *testing.T) {
			r := &fakeRuntime{probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
			e := newTestEngine(t, r)
			if err := e.Prepare(context.Background(), testProfile()); err != nil {
				t.Fatal(err)
			}
			_, _ = e.ProbeExit(context.Background())
			if err := e.SetBusinessGate(true); err != nil {
				t.Fatal(err)
			}
			r.probe = func(context.Context) (string, error) { return "", errors.New("fixture temporary network failure") }
			if _, err := e.ProbeExit(context.Background()); err == nil || e.VerifiedIPv4() != "9.9.9.9" {
				t.Fatal("transient diagnostic failure changed current proof")
			}
			r.probe = func(context.Context) (string, error) { return observed, nil }
			result, err := e.ProbeExit(context.Background())
			if err != nil || result.IPv4 != observed || !result.Authenticated || !result.Reachable {
				t.Fatal("mismatch evidence hidden")
			}
			if e.VerifiedIPv4() != observed || !e.gate.isOpen() || r.closed || e.InterfaceLUID() == 0 {
				t.Fatal("observation retired runtime")
			}
		})
	}
}

func TestAsynchronousCleanupFailureCannotBecomeSuccessfulStop(t *testing.T) {
	r := &fakeRuntime{closeErr: errors.New("native-close-failed"), probe: func(context.Context) (string, error) { return "9.9.9.9", nil }}
	e := newTestEngine(t, r)
	if err := e.Prepare(context.Background(), testProfile()); err != nil {
		t.Fatal(err)
	}
	e.stopIfCurrent(e.gate)
	err := e.Stop(context.Background())
	if err == nil {
		t.Fatal("lost cleanup error; host could wrongly release WFP")
	}
	if err.Error() != "ENGINE_STOP_FAILED" || !errors.Is(err, r.closeErr) {
		t.Fatal("cleanup must retain its native cause without exposing it in the public message")
	}
}
