package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
	xtun "github.com/xtls/xray-core/proxy/tun"
)

func TestRoutingFakeIPNeverAliasesActiveOrReconnectedSessions(t *testing.T) {
	s1, _, _, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	old := routingAnswerIP(t, s1, "google.com")
	if same := routingAnswerIP(t, s1, "GOOGLE.COM."); old != same {
		t.Fatal("canonical name changed binding")
	}
	for _, closeOld := range []bool{false, true} {
		if closeOld {
			if err := s1.Close(); err != nil {
				t.Fatal(err)
			}
		}
		s2, _, _, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
		fresh := routingAnswerIP(t, s2, "baidu.com")
		if fresh == old {
			t.Fatal("old fake reassigned to new name")
		}
		c, err := s2.dial(context.Background(), "tcp", M.SocksaddrFrom(old, 443))
		if c != nil {
			c.Close()
		}
		if err == nil || out.count() != 0 || protected.calls.Load() != 0 {
			t.Fatal("old session fake acquired a new route")
		}
	}
	if _, ok := s1.resolver.fake.lookup(old); ok {
		t.Fatal("closed map retained binding")
	}
	if _, err := allocateTestFake(&s1.resolver.fake, "late.invalid"); err == nil {
		t.Fatal("closed map resurrected")
	}
}

func TestRoutingFakeMapBoundedWithoutEvictingExistingNames(t *testing.T) {
	f := new(fakeNames)
	defer f.close()
	first, err := allocateTestFake(f, "first.invalid")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxSessionFakeNames; i++ {
		if _, err := allocateTestFake(f, fmt.Sprintf("n%d.invalid", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = allocateTestFake(f, "overflow.invalid"); err == nil {
		t.Fatal("unbounded name map")
	}
	if binding, ok := f.lookup(first); !ok || binding.name != "first.invalid" {
		t.Fatal("capacity evicted active binding")
	}
	if again, err := allocateTestFake(f, "first.invalid"); err != nil || again != first {
		t.Fatal("full map lost existing binding")
	}
}

func TestRoutingFakeConcurrentAllocationAndClose(t *testing.T) {
	f := new(fakeNames)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[netip.Addr]string)
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 64; i++ {
				name := fmt.Sprintf("concurrent%d.invalid", i)
				ip, err := allocateTestFake(f, name)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				old, ok := seen[ip]
				seen[ip] = name
				mu.Unlock()
				if ok && old != name {
					t.Error("concurrent fake collision")
				}
				if got, ok := f.lookup(ip); !ok || got.name != name {
					t.Error("reverse binding lost")
				}
			}
		}()
	}
	wg.Wait()
	if len(seen) != 64 {
		t.Fatalf("same names gained multiple identities: %d", len(seen))
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = allocateTestFake(f, "close-race.invalid"); f.close() }()
	}
	wg.Wait()
	for ip := range seen {
		if _, ok := f.lookup(ip); ok {
			t.Fatal("close retained route")
		}
	}
}

func allocateTestFake(f *fakeNames, name string) (netip.Addr, error) {
	ips, err := f.allocate(name, []netip.Addr{netip.MustParseAddr("8.8.8.8")})
	if err != nil {
		return netip.Addr{}, err
	}
	return ips[0], nil
}

func TestRoutingFakeRevalidationCannotEnterConnectedLAN(t *testing.T) {
	s, domestic, _, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	ip := routingAnswerIP(t, s, "baidu.com")
	// A fresh validated answer remains usable until TTL expiry. Revalidation
	// after expiry must reject a DNS rebind into the connected LAN exception.
	expireRoutingAnswer(s, "baidu.com")
	domestic.ips = []string{"192.168.8.100"}
	c, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ip, 443))
	if c != nil {
		c.Close()
	}
	if err == nil || out.count() != 0 || protected.calls.Load() != 0 {
		t.Fatal("DNS rebinding entered LAN exception")
	}
}

func TestRoutingCanceledDNSWaitAndLateDial(t *testing.T) {
	for _, kind := range []string{"caller", "session", "lease"} {
		for _, phase := range []string{"dns", "dial"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				s, domestic, selected, out, _, valid := routingSession(t, routingpolicy.BypassCN)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				var peer net.Conn
				if phase == "dns" {
					domestic.hook = func(call context.Context, q *mdns.Msg) (*mdns.Msg, error) {
						close(entered)
						if kind == "lease" {
							<-release
						} else {
							<-call.Done()
						}
						a := new(mdns.Msg).SetReply(q)
						rr, _ := mdns.NewRR("baidu.com. 60 IN A 8.8.8.8")
						a.Answer = []mdns.RR{rr}
						return a, nil
					}
				} else {
					s.selected = routingDialFunc(func(call context.Context, _, _ string) (net.Conn, error) {
						left, right := net.Pipe()
						peer = right
						close(entered)
						if kind == "lease" {
							<-release
						} else {
							<-call.Done()
						}
						return left, nil
					})
				}
				host := "baidu.com"
				if phase == "dial" {
					host = "8.8.8.8"
				}
				go func() {
					c, err := s.dial(ctx, "tcp", M.ParseSocksaddr(net.JoinHostPort(host, "443")))
					if c != nil {
						c.Close()
					}
					done <- err
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("operation never started")
				}
				switch kind {
				case "caller":
					cancel()
				case "session":
					s.cancel()
				case "lease":
					valid.Store(false)
					close(release)
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("late result escaped cancellation")
					}
				case <-time.After(time.Second):
					t.Fatal("cancel failed to interrupt pending work")
				}
				if peer != nil {
					defer peer.Close()
					peer.SetReadDeadline(time.Now().Add(time.Second))
					var b [1]byte
					if _, err := peer.Read(b[:]); err != io.EOF {
						t.Fatal("late socket leaked", err)
					}
				}
				if selected.count() != 0 || out.count() != 0 {
					t.Fatal("failure fell back")
				}
			})
		}
	}
}

type routingDialFunc func(context.Context, string, string) (net.Conn, error)

func (f routingDialFunc) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return f(ctx, n, a)
}

func TestRoutingEstablishedSocketHandoffAndRevocation(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			s, _, _, _, _, valid := routingSession(t, routingpolicy.BypassCN)
			ctx, cancel := context.WithCancel(context.Background())
			c, err := s.dial(ctx, network, M.ParseSocksaddr("8.8.8.8:443"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			cancel()
			c.SetDeadline(time.Now().Add(time.Second))
			if _, err = c.Write([]byte("ok")); err != nil {
				t.Fatal("caller cancellation killed handed-off socket", err)
			}
			b := make([]byte, 2)
			if _, err = io.ReadFull(c, b); err != nil || string(b) != "ok" {
				t.Fatal("echo failed", err)
			}
			valid.Store(false)
			if n, err := c.Write([]byte("denied")); n != 0 || err == nil {
				t.Fatal("paused lease wrote payload")
			}
			if _, err = s.dial(context.Background(), network, M.ParseSocksaddr("223.5.5.5:443")); err == nil {
				t.Fatal("paused lease started direct dial")
			}
		})
	}
}

func TestRoutingTUNUDPReturnsOriginalFakeDestination(t *testing.T) {
	s, _, _, out, _, _ := routingSession(t, routingpolicy.BypassCN)
	ip := routingAnswerIP(t, s, "google.com")
	device := newStackTunFixture()
	stack, err := xtun.NewStack(s.ctx, xtun.StackOptions{Tun: device, MTU: 1500}, &flowHandler{session: s})
	if err != nil {
		t.Fatal(err)
	}
	s.stack = stack
	s.platform.device = device
	stack.Input(udpPacketFixture(ip.String(), 443, []byte("domain-payload")))
	packet := awaitStackPacket(t, device)
	if string(packet[28:]) != "domain-payload" || net.IP(packet[12:16]).String() != ip.String() {
		t.Fatal("UDP lost synthetic source identity")
	}
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.calls) != 1 || out.calls[0] != "udp:223.5.5.5:443" {
		t.Fatalf("fake destination reached outbound: %v", out.calls)
	}
}
