package tunengine

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/infra/conf"
	_ "github.com/xtls/xray-core/proxy/freedom"
)

// Shared by the real Darwin kernel test and a development-host stack test.
// This keeps the redirect/echo fixture executable on Windows too.
func darwinKernelOutboundFixture(t *testing.T, ctx context.Context, iface string) *xrayOutbound {
	t.Helper()
	// TCP and UDP have different excluded ranges on Windows. Port-zero UDP
	// allocations can repeatedly pick adjacent ports inside a TCP exclusion.
	// Independently sample unprivileged loopback ports and reserve both sockets;
	// do not modify exclusions or use the client's 10808/12808 ports.
	var listener net.Listener
	var udp net.PacketConn
	var err error
	for attempt := 0; attempt < 32; attempt++ {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(20000+rand.IntN(29000)))
		udp, err = net.ListenPacket("udp4", address)
		if err != nil {
			continue
		}
		listener, err = net.Listen("tcp4", udp.LocalAddr().String())
		if err == nil {
			break
		}
		_ = udp.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	var serving sync.WaitGroup
	t.Cleanup(func() { _ = listener.Close(); _ = udp.Close(); serving.Wait() })
	serving.Add(2)
	go func() {
		defer serving.Done()
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	go func() {
		defer serving.Done()
		b := make([]byte, 65535)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			_, _ = udp.WriteTo(b[:n], a)
		}
	}()
	settings := json.RawMessage(`{"redirect":"` + listener.Addr().String() + `","finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}`)
	selected, err := newXrayOutbound(ctx, &conf.OutboundDetourConfig{Tag: "selected", Protocol: "freedom", Settings: &settings})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := selected.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = selected.start(iface); err != nil {
		t.Fatal(err)
	}
	return selected
}

func TestDarwinKernelOutboundFixtureThroughXrayStack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	selected := darwinKernelOutboundFixture(t, ctx, "")
	gate := newSessionGate()
	router := &nativeRouter{ctx: ctx, gate: gate, selected: selected}
	router.config.Route.Final = "selected"
	app := memoryApplication(t, router, gate)
	destination := netip.MustParseAddrPort("10.253.233.9:4242")
	stream, err := app.DialTCP(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stackIO(t, stream, func() error {
		_, err := stream.Write([]byte("native-echo"))
		if err != nil {
			return err
		}
		got := make([]byte, 11)
		_, err = io.ReadFull(stream, got)
		if string(got) != "native-echo" {
			t.Errorf("TCP echo %q", got)
		}
		return err
	})
	packet, err := app.DialUDP()
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	got := stackPacketExchange(t, packet, destination, []byte("native-udp"))
	if string(got) != "native-udp" {
		t.Fatalf("UDP echo %q", got)
	}
}
