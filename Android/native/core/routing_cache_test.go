package bridge

import (
	"bluegrass/native/core/routingpolicy"
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

func TestRoutingFreshFakeAnswerSeedsHundredConnections(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	ip := routingAnswerIP(t, s, "google.com")
	for i := 0; i < 100; i++ {
		c, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ip, 443))
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	if selected.count() != 1 {
		t.Fatalf("100 connections made %d DNS queries, want only the seeding query", selected.count())
	}
}

func expireRoutingAnswer(s *Session, name string) {
	s.resolver.fake.mu.Lock()
	defer s.resolver.fake.mu.Unlock()
	a := s.resolver.fake.answers[name]
	a.expires = time.Now().Add(-time.Second)
	s.resolver.fake.answers[name] = a
}

func TestRoutingExpiredCacheRefreshesAndNegativeAnswersNeverPersist(t *testing.T) {
	for _, code := range []int{mdns.RcodeNameError, mdns.RcodeSuccess, mdns.RcodeServerFailure} {
		t.Run(mdns.RcodeToString[code], func(t *testing.T) {
			s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
			old := routingAnswerIP(t, s, "google.com")
			expireRoutingAnswer(s, "google.com")
			selected.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) { return new(mdns.Msg).SetRcode(q, code), nil }
			if _, err := s.resolver.resolve(context.Background(), "google.com"); err == nil {
				t.Fatal("negative refresh reused stale candidates")
			}
			selected.hook = nil
			selected.ips = []string{"9.9.9.9"}
			route, err := s.resolver.resolve(context.Background(), "google.com")
			if err != nil || route.Address.String() != "9.9.9.9" || selected.count() != 3 {
				t.Fatal("negative persisted or refresh lost", route, err, selected.count())
			}
			if same, ok := s.resolver.fake.lookup(old); !ok || same.name != "google.com" {
				t.Fatal("expiry reassigned synthetic identity")
			}
		})
	}
}

func TestRoutingNewNegativeDNSInvalidatesFreshSeed(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	_ = routingAnswerIP(t, s, "google.com")
	selected.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		return new(mdns.Msg).SetRcode(q, mdns.RcodeNameError), nil
	}
	a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("google.com.", mdns.TypeA))
	if err != nil || a.Rcode != mdns.RcodeNameError || len(a.Answer) != 0 {
		t.Fatal("NXDOMAIN changed", a, err)
	}
	if _, err = s.resolver.resolve(context.Background(), "google.com"); err == nil {
		t.Fatal("NXDOMAIN left positive seed usable")
	}
}

func waitRoutingFlight(t *testing.T, s *Session, selected *routingDNSFixture, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.resolver.fake.mu.Lock()
		f := s.resolver.fake.flights["google.com"]
		got := 0
		if f != nil {
			got = f.waiters
		}
		s.resolver.fake.mu.Unlock()
		if got == want {
			return
		}
		if selected.count() > 1 {
			t.Fatalf("concurrent resolve started %d upstream queries", selected.count())
		}
		runtime.Gosched()
	}
	t.Fatal("callers did not join shared DNS flight")
}

func TestRoutingConcurrentResolveCoalescesAndCallerCancelIsIndependent(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	release := make(chan struct{})
	defer close(release)
	selected.hook = func(ctx context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		a := new(mdns.Msg).SetReply(q)
		rr, _ := mdns.NewRR("google.com. 60 IN A 8.8.8.8")
		a.Answer = []mdns.RR{rr}
		return a, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.resolver.resolve(ctx, "google.com"); first <- err }()
	waitRoutingFlight(t, s, selected, 1)
	others := make(chan error, 15)
	for i := 0; i < 15; i++ {
		go func() { _, err := s.resolver.resolve(context.Background(), "google.com"); others <- err }()
	}
	waitRoutingFlight(t, s, selected, 16)
	cancel()
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("canceled waiter succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter stuck")
	}
	waitRoutingFlight(t, s, selected, 15)
	// Release via a send to each waiter rather than closing twice on failure.
	release <- struct{}{}
	for i := 0; i < 15; i++ {
		select {
		case err := <-others:
			if err != nil {
				t.Fatal("one cancellation killed shared flight", err)
			}
		case <-time.After(time.Second):
			t.Fatal("coalesced waiter stuck")
		}
	}
	if selected.count() != 1 {
		t.Fatal("duplicate upstream query", selected.count())
	}
}

func TestRoutingTCPTriesAllValidatedCandidates(t *testing.T) {
	s, _, selected, out, protected, _ := routingSession(t, routingpolicy.BypassCN)
	selected.ips = []string{"223.5.5.5", "119.29.29.29"}
	ip := routingAnswerIP(t, s, "google.com")
	var calls []string
	s.selected = routingDialFunc(func(ctx context.Context, n, a string) (net.Conn, error) {
		calls = append(calls, a)
		if a == "223.5.5.5:443" {
			return nil, errors.New("fixture establishment failed")
		}
		return out.DialContext(ctx, n, a)
	})
	c, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ip, 443))
	if c != nil {
		c.Close()
	}
	if err != nil || len(calls) != 2 || calls[0] != "223.5.5.5:443" || calls[1] != "119.29.29.29:443" {
		t.Fatalf("missing TCP candidate failover: calls=%v err=%v", calls, err)
	}
	if protected.calls.Load() != 0 {
		t.Fatal("foreign domain candidates fell back to direct")
	}
	if selected.count() != 1 {
		t.Fatal("failover repeated seeded DNS lookup")
	}
}

func TestRoutingFakeTTLUsesMinimumRelevantRecordAndCaps(t *testing.T) {
	for _, tt := range []struct {
		name    string
		records []string
		ttl     uint32
	}{
		{"a-min", []string{"google.com. 30 IN A 8.8.8.8", "google.com. 10 IN A 9.9.9.9"}, 10},
		{"cname-min", []string{"google.com. 5 IN CNAME edge.invalid.", "edge.invalid. 30 IN A 8.8.8.8", "unrelated.invalid. 0 IN A 9.9.9.9"}, 5},
		{"zero-a", []string{"google.com. 0 IN A 8.8.8.8"}, 0},
		{"zero-cname", []string{"google.com. 0 IN CNAME edge.invalid.", "edge.invalid. 30 IN A 8.8.8.8"}, 0},
		{"cap", []string{"google.com. 4294967295 IN A 8.8.8.8"}, 300},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
			selected.hook = func(_ context.Context, q *mdns.Msg) (*mdns.Msg, error) {
				a := new(mdns.Msg).SetReply(q)
				for _, text := range tt.records {
					rr, err := mdns.NewRR(text)
					if err != nil {
						t.Fatal(err)
					}
					a.Answer = append(a.Answer, rr)
				}
				return a, nil
			}
			a, err := s.resolver.Exchange(context.Background(), new(mdns.Msg).SetQuestion("google.com.", mdns.TypeA))
			wantRecords := 1
			if tt.name == "a-min" {
				wantRecords = 2
			}
			if err != nil || len(a.Answer) != wantRecords {
				t.Fatal(a, err)
			}
			if got := a.Answer[0].Header().Ttl; got != tt.ttl {
				t.Errorf("TTL=%d want %d", got, tt.ttl)
			}
			if _, err = s.resolver.resolve(context.Background(), "google.com"); err != nil {
				t.Fatal(err)
			}
			want := 1
			if tt.ttl == 0 {
				want = 2
			}
			if selected.count() != want {
				t.Errorf("DNS queries=%d want %d", selected.count(), want)
			}
		})
	}
}

func TestRoutingCandidateErrorsNeverChangeForeignPolicy(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			s, _, selected, _, protected, _ := routingSession(t, routingpolicy.BypassCN)
			selected.ips = []string{"223.5.5.5", "119.29.29.29"}
			ip := routingAnswerIP(t, s, "google.com")
			calls := 0
			s.selected = routingDialFunc(func(context.Context, string, string) (net.Conn, error) {
				calls++
				return nil, errors.New("FAKE_SECRET")
			})
			c, err := s.dial(context.Background(), network, M.SocksaddrFrom(ip, 443))
			if c != nil {
				c.Close()
			}
			want := 2
			if network == "udp" {
				want = 1
			}
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != want || protected.calls.Load() != 0 {
				t.Fatalf("wrong failure boundary: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRoutingCandidateClassificationUsesOriginalDomainAndExactIP(t *testing.T) {
	for _, tt := range []struct {
		host          string
		ips           []string
		direct, proxy int
	}{
		{"baidu.com", []string{"8.8.8.8", "9.9.9.9"}, 2, 0},
		{"unlisted-routing-fixture.invalid", []string{"223.5.5.5", "8.8.8.8"}, 1, 1},
	} {
		t.Run(tt.host, func(t *testing.T) {
			s, domestic, selected, _, protected, _ := routingSession(t, routingpolicy.BypassCN)
			domestic.ips, selected.ips = tt.ips, tt.ips
			ip := routingAnswerIP(t, s, tt.host)
			var calls []string
			s.selected = routingDialFunc(func(_ context.Context, _, a string) (net.Conn, error) {
				calls = append(calls, a)
				return nil, errNetwork
			})
			_, err := s.dial(context.Background(), "tcp", M.SocksaddrFrom(ip, 443))
			if err == nil || int(protected.calls.Load()) != tt.direct || len(calls) != tt.proxy {
				t.Fatalf("candidate policy lost: direct=%d proxy=%v err=%v", protected.calls.Load(), calls, err)
			}
			if tt.proxy == 1 && calls[0] != "8.8.8.8:443" {
				t.Fatal("wrong exact proxy address", calls)
			}
		})
	}
}

func TestRoutingCanceledFlightAllowsFreshCallerAndCloseClearsState(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	var calls atomic.Int32
	canceled := make(chan struct{})
	selected.hook = func(ctx context.Context, q *mdns.Msg) (*mdns.Msg, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		a := new(mdns.Msg).SetReply(q)
		rr, _ := mdns.NewRR("google.com. 60 IN A 9.9.9.9")
		a.Answer = []mdns.RR{rr}
		return a, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.resolver.resolve(ctx, "google.com"); done <- err }()
	waitRoutingFlight(t, s, selected, 1)
	// Ensure the physical operation has started before cancellation.
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if calls.Load() == 0 {
		t.Fatal("DNS never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled flight succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter stuck")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("last waiter failed to cancel upstream")
	}
	route, err := s.resolver.resolve(context.Background(), "google.com")
	if err != nil || route.Address.String() != "9.9.9.9" || selected.count() != 2 {
		t.Fatal("fresh caller joined canceled flight", route, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s.resolver.fake.mu.Lock()
	remaining := len(s.resolver.fake.byName) + len(s.resolver.fake.byIP) + len(s.resolver.fake.answers) + len(s.resolver.fake.flights)
	s.resolver.fake.mu.Unlock()
	if remaining != 0 {
		t.Fatal("session close retained DNS state")
	}
	if _, err = s.resolver.resolve(context.Background(), "google.com"); err == nil || selected.count() != 2 {
		t.Fatal("closed cache resurrected")
	}
}

func TestRoutingSessionCloseCancelsAllCoalescedWaiters(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	started, canceled := make(chan struct{}), make(chan struct{})
	selected.hook = func(ctx context.Context, _ *mdns.Msg) (*mdns.Msg, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := s.resolver.resolve(context.Background(), "google.com"); done <- err }()
	}
	waitRoutingFlight(t, s, selected, 2)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream not started")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("session-canceled waiter succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("session cancellation stuck")
		}
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("session close left upstream running")
	}
	if selected.count() != 1 {
		t.Fatal("coalesced session made duplicate queries")
	}
}

func TestRoutingCancelledOrPausedTCPFailureCannotTryNextCandidate(t *testing.T) {
	for _, kind := range []string{"caller", "session", "lease"} {
		t.Run(kind, func(t *testing.T) {
			s, _, selected, _, protected, valid := routingSession(t, routingpolicy.BypassCN)
			selected.ips = []string{"8.8.8.8", "9.9.9.9"}
			ip := routingAnswerIP(t, s, "google.com")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s.selected = routingDialFunc(func(context.Context, string, string) (net.Conn, error) {
				calls++
				switch kind {
				case "caller":
					cancel()
				case "session":
					s.cancel()
				case "lease":
					valid.Store(false)
				}
				return nil, errNetwork
			})
			c, err := s.dial(ctx, "tcp", M.SocksaddrFrom(ip, 443))
			if c != nil {
				c.Close()
			}
			if err == nil || calls != 1 || protected.calls.Load() != 0 {
				t.Fatal("revoked TCP failure tried another candidate", calls, err)
			}
		})
	}
}

func TestRoutingCacheCannotBeMutatedByLookupCaller(t *testing.T) {
	s, _, selected, _, _, _ := routingSession(t, routingpolicy.BypassCN)
	_ = routingAnswerIP(t, s, "google.com")
	addresses, err := s.resolver.lookupRouting(context.Background(), "google.com", selected)
	if err != nil {
		t.Fatal(err)
	}
	addresses[0] = M.ParseSocksaddr("192.168.8.100:443").Addr
	route, err := s.resolver.resolve(context.Background(), "google.com")
	if err != nil || route.Address.String() != "223.5.5.5" || selected.count() != 1 {
		t.Fatal("caller mutated cached candidates", route, err)
	}
}
