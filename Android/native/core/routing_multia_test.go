package bridge

import (
	"bluegrass/native/core/protocolconfig"
	"bluegrass/native/core/routingpolicy"
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

func routingAnswerIPs(t *testing.T, s *Session, host string) []netip.Addr {
	t.Helper()
	a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion(mdns.Fqdn(host), mdns.TypeA))
	if err != nil || a == nil {
		t.Fatal("DNS failed", err)
	}
	var ips []netip.Addr
	for _, rr := range a.Answer {
		record, ok := rr.(*mdns.A)
		if !ok {
			t.Fatal("not A")
		}
		ip, ok := netip.AddrFromSlice(record.A)
		if !ok {
			t.Fatal("invalid A")
		}
		ips = append(ips, ip.Unmap())
	}
	return ips
}

func TestRoutingSuccessfulSeedSatisfiesWaitingConnections(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	started, retired := make(chan struct{}), make(chan struct{})
	selected.hook = func(ctx context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		close(started)
		<-ctx.Done()
		defer close(retired)
		a := new(mdns.Msg).SetReply(q)
		rr, _ := mdns.NewRR("google.com. 60 IN A 9.9.9.9")
		a.Answer = []mdns.RR{rr}
		return a, nil
	}
	type result struct {
		route routingpolicy.Route
		err   error
	}
	done := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			route, err := s.resolver.resolve(context.Background(), "google.com")
			done <- result{route, err}
		}()
	}
	waitRoutingFlight(t, s, selected, 2)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("flight never started")
	}
	q := new(mdns.Msg).SetQuestion("google.com.", mdns.TypeA)
	a := new(mdns.Msg).SetReply(q)
	rr, _ := mdns.NewRR("google.com. 60 IN A 8.8.8.8")
	a.Answer = []mdns.RR{rr}
	if _, err := s.resolver.fakeAnswer(q, a, "google.com"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-done:
			if got.err != nil || got.route.Address.String() != "8.8.8.8" {
				t.Errorf("fresh seed failed waiting connection: %v %v", got.route, got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("seed left waiter blocked")
		}
	}
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("seed failed to cancel obsolete query")
	}
	route, err := s.resolver.resolve(context.Background(), "google.com")
	if err != nil || route.Address.String() != "8.8.8.8" || selected.count() != 1 {
		t.Fatal("late result replaced fresh seed", route, err)
	}
}

type allowingRoutingPlatform struct{ calls atomic.Int32 }

func (*allowingRoutingPlatform) IsValid() bool                  { return true }
func (p *allowingRoutingPlatform) Protect(int32) error          { p.calls.Add(1); return nil }
func (*allowingRoutingPlatform) Resolve(string) (string, error) { return "", errNetwork }
func (*allowingRoutingPlatform) NetworkInfo() (string, error)   { return "", errNetwork }

func TestRoutingXrayHTTPAsyncFailureAllowsSecondDNSCandidate(t *testing.T) {
	s, _, selected, _, direct, _ := routingSession(t, routingpolicy.BypassCN)
	selected.ips = []string{"223.5.5.5", "119.29.29.29"}
	ips := routingAnswerIPs(t, s, "google.com")
	if len(ips) != 2 || ips[0] == ips[1] {
		t.Fatalf("DNS collapsed distinct candidates: %v", ips)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var targets []string
	serverErrors := make(chan error, 16)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(c)
				request, err := http.ReadRequest(reader)
				if err != nil {
					serverErrors <- err
					return
				}
				mu.Lock()
				targets = append(targets, request.Host)
				mu.Unlock()
				if request.Method != "CONNECT" {
					serverErrors <- errNetwork
					return
				}
				if request.Host == "223.5.5.5:443" {
					_, err = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
					if err != nil {
						serverErrors <- err
					}
					return
				}
				if request.Host != "119.29.29.29:443" {
					serverErrors <- errNetwork
					return
				}
				if _, err = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					serverErrors <- err
					return
				}
				b := make([]byte, 4)
				if _, err = io.ReadFull(reader, b); err != nil || string(b) != "ping" {
					serverErrors <- errNetwork
					return
				}
				if _, err = io.WriteString(c, "pong"); err != nil {
					serverErrors <- err
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	// Run the real HTTP Xray adapter against a loopback fixture. Only trusted
	// test-built physical entry settings are redirected; session routing and the
	// protected platform dialer are the production implementations.
	options, err := protocolconfig.Build(protocolconfig.Config{Version: 1, Type: "http", HTTP: &protocolconfig.HTTP{}}, netip.MustParseAddrPort("8.8.4.4:8080"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{"address": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(settings)
	options.Settings = &raw
	callback := new(allowingRoutingPlatform)
	physical := &platformBridge{callback: callback, lease: s.lease}
	out, err := protocolconfig.StartOutbound(context.WithValue(s.ctx, platformKey{}, physical), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	s.selected = out
	first, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ips[0], 443))
	if err != nil {
		t.Fatal("expected asynchronous adapter return", err)
	}
	first.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = first.Write([]byte("ping"))
	var b [4]byte
	n, readErr := first.Read(b[:])
	first.Close()
	if n != 0 || readErr == nil {
		t.Fatal("502 did not fail the first stream")
	}
	if e, ok := readErr.(net.Error); ok && e.Timeout() {
		t.Fatal("first stream timed out instead of observing 502")
	}
	mu.Lock()
	before := append([]string(nil), targets...)
	mu.Unlock()
	if len(before) == 0 {
		t.Fatal("real adapter never sent CONNECT")
	}
	for _, target := range before {
		if target != "223.5.5.5:443" {
			t.Fatal("async failure replayed to another candidate", before)
		}
	}
	second, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ips[1], 443))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = second.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(second, b[:]); err != nil || string(b[:]) != "pong" {
		t.Fatal("second DNS address did not use second CONNECT candidate", err)
	}
	mu.Lock()
	last := targets[len(targets)-1]
	mu.Unlock()
	if last != "119.29.29.29:443" || direct.calls.Load() != 0 || callback.calls.Load() < 2 || selected.count() != 1 {
		t.Fatal("candidate, protection or DNS cache contract lost", last)
	}
	select {
	case err := <-serverErrors:
		t.Fatal("HTTP fixture failed", err)
	default:
	}
}

func TestRoutingCandidateBindingsStayStableAndRetiredCandidatesFailClosed(t *testing.T) {
	s, _, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	selected.ips = []string{"223.5.5.5", "119.29.29.29"}
	old := routingAnswerIPs(t, s, "google.com")
	if len(old) != 2 {
		t.Fatal("missing candidates")
	}
	selected.ips = []string{"119.29.29.29", "8.8.8.8"}
	fresh := routingAnswerIPs(t, s, "google.com")
	if len(fresh) != 2 || fresh[0] != old[1] || fresh[1] == old[0] || fresh[1] == old[1] {
		t.Fatal("candidate refresh reassigned binding", old, fresh)
	}
	for _, network := range []string{"tcp", "udp"} {
		c, err := s.dial(context.Background(), network, M.SocksaddrFrom(old[0], 443))
		if c != nil {
			c.Close()
		}
		if err == nil || out.count() != 0 {
			t.Fatal("retired candidate retargeted to a different IP")
		}
	}
	for i, ip := range fresh {
		c, err := s.dial(context.Background(), "udp", M.SocksaddrFrom(ip, 443))
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		out.mu.Lock()
		last := out.calls[len(out.calls)-1]
		out.mu.Unlock()
		want := []string{"udp:119.29.29.29:443", "udp:8.8.8.8:443"}[i]
		if last != want {
			t.Fatal("UDP ignored its candidate", last, want)
		}
	}
	if protected.calls.Load() != 0 {
		t.Fatal("foreign candidates switched to direct")
	}
}

func TestRoutingBindingCapacityRejectsWholeAnswerWithoutReassignment(t *testing.T) {
	f := new(fakeNames)
	defer f.close()
	candidate := netip.MustParseAddr("8.0.0.1")
	var first netip.Addr
	for count := 0; count < maxSessionFakeBindings-1; {
		var batch []netip.Addr
		for len(batch) < maxRoutingCandidates && count+len(batch) < maxSessionFakeBindings-1 {
			batch = append(batch, candidate)
			candidate = candidate.Next()
		}
		ips, err := f.allocate("capacity.invalid", batch)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			first = ips[0]
		}
		count += len(batch)
	}
	before := processFakeSequence.Load()
	if _, err := f.allocate("capacity.invalid", []netip.Addr{candidate, candidate.Next()}); err == nil {
		t.Fatal("partial oversized answer accepted")
	}
	if processFakeSequence.Load() != before {
		t.Fatal("failed answer consumed or reassigned process identities")
	}
	if got, ok := f.lookup(first); !ok || got.name != "capacity.invalid" || got.candidate.String() != "8.0.0.1" {
		t.Fatal("capacity evicted existing binding")
	}
	if _, err := f.allocate("capacity.invalid", []netip.Addr{candidate}); err != nil {
		t.Fatal("last available binding was lost", err)
	}
	if _, err := f.allocate("another.invalid", []netip.Addr{candidate.Next()}); err == nil {
		t.Fatal("unbounded binding map")
	}
}
