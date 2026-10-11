package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"bluegrass/client-api/internal/model"
)

func TestLoginAndRefreshHaveIndependentDefaultIPLimits(t *testing.T) {
	a := &fakeAuth{}
	h := newHandler(t, a, nil, Config{})
	for _, tc := range []struct {
		path, body string
		allowed    int
	}{
		{"/auth/login", loginJSON, 120}, {"/auth/refresh", refreshJSON, 240},
	} {
		for i := 0; i < tc.allowed; i++ {
			checkResponse(t, serve(h, request("POST", tc.path, tc.body)), 200)
		}
		w := serve(h, request("POST", tc.path, tc.body))
		checkResponse(t, w, 429)
		retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if err != nil || retry < 1 || retry > 60 {
			t.Errorf("invalid rate-limit Retry-After: %q", w.Header().Get("Retry-After"))
		}
		other := request("POST", tc.path, tc.body)
		other.RemoteAddr = "198.51.100.20:1"
		checkResponse(t, serve(h, other), 200)
	}
	if a.loginCalls.Load() != 121 || a.refreshCalls.Load() != 241 {
		t.Error("denied requests reached authentication or route limits share a counter")
	}
	checkResponse(t, serve(h, authorized("GET", "/me", "")), 200)
}

func TestFailedAttemptsConsumeIPLimitWithoutAccountThrottle(t *testing.T) {
	a := &fakeAuth{login: func(context.Context, model.LoginInput) (model.Tokens, error) {
		return model.Tokens{}, model.Fail("invalid_credentials", 401, "Invalid credentials")
	}}
	h := newHandler(t, a, nil, Config{LoginPerMinute: 1})
	checkResponse(t, serve(h, request("POST", "/auth/login", loginJSON)), 401)
	checkResponse(t, serve(h, request("POST", "/auth/login", loginJSON)), 429)
	other := request("POST", "/auth/login", loginJSON)
	other.RemoteAddr = "198.51.100.11:1"
	checkResponse(t, serve(h, other), 401)
	if a.loginCalls.Load() != 2 {
		t.Error("HTTP boundary added account throttling or skipped failed IP attempts")
	}
}

func TestRateCountersSurviveSequentialAndConcurrentRequests(t *testing.T) {
	for _, pathBody := range []struct{ path, body string }{{"/auth/login", loginJSON}, {"/auth/refresh", refreshJSON}} {
		t.Run(pathBody.path, func(t *testing.T) {
			a := &fakeAuth{}
			h := newHandler(t, a, nil, Config{MaxInflight: 128, LoginPerMinute: 20, RefreshPerMinute: 20})
			for i := 0; i < 7; i++ {
				checkResponse(t, serve(h, request("POST", pathBody.path, pathBody.body)), 200)
			}
			start := make(chan struct{})
			results := make(chan *httptest.ResponseRecorder, 64)
			var wg sync.WaitGroup
			for i := 0; i < 64; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); <-start; results <- serve(h, request("POST", pathBody.path, pathBody.body)) }()
			}
			close(start)
			wg.Wait()
			close(results)
			accepted, denied := 0, 0
			for w := range results {
				switch w.Code {
				case 200:
					accepted++
					checkResponse(t, w, 200)
				case 429:
					denied++
					checkResponse(t, w, 429)
				default:
					t.Errorf("unexpected concurrent status %d", w.Code)
				}
			}
			if accepted != 13 || denied != 51 {
				t.Errorf("remaining capacity admitted=%d denied=%d; want 13,51", accepted, denied)
			}
			if calls := a.loginCalls.Load() + a.refreshCalls.Load(); calls != 20 {
				t.Errorf("authentication calls=%d, want 20", calls)
			}
			checkResponse(t, serve(h, request("POST", pathBody.path, pathBody.body)), 429)
		})
	}
}

func TestFullRateMapDeniesNewKeysUntilExpiredEviction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &fakeAuth{}
		h := newHandler(t, a, nil, Config{MaxRateKeys: 2, LoginPerMinute: 3, RefreshPerMinute: 3})
		for _, pathBody := range []struct{ path, body string }{{"/auth/login", loginJSON}, {"/auth/refresh", refreshJSON}} {
			call := func(ip string) *httptest.ResponseRecorder {
				r := request("POST", pathBody.path, pathBody.body)
				r.RemoteAddr = ip + ":1"
				return serve(h, r)
			}
			checkResponse(t, call("192.0.2.1"), 200)
			checkResponse(t, call("192.0.2.2"), 200)
			checkResponse(t, call("192.0.2.3"), 429)
			// A full key map still admits existing keys within their quota.
			checkResponse(t, call("192.0.2.1"), 200)
			checkResponse(t, call("192.0.2.1"), 200)
			checkResponse(t, call("192.0.2.1"), 429)
			// Expiry uses virtual time; no minute-long wall-clock wait.
			time.Sleep(61 * time.Second)
			checkResponse(t, call("192.0.2.3"), 200)
			checkResponse(t, call("192.0.2.4"), 200)
			checkResponse(t, call("192.0.2.5"), 429)
		}
		if a.loginCalls.Load() != 6 || a.refreshCalls.Load() != 6 {
			t.Error("full/expired rate map admitted the wrong requests")
		}
	})
}

func TestDefaultRateKeyCapacityIsBounded(t *testing.T) {
	h := newHandler(t, nil, nil, Config{LoginPerMinute: 1})
	for i := 0; i < 10000; i++ {
		r := request("POST", "/auth/login", loginJSON)
		r.RemoteAddr = fmt.Sprintf("10.0.%d.%d:1", i/256, i%256)
		if w := serve(h, r); w.Code != 200 {
			t.Fatalf("key %d status=%d, want 200", i, w.Code)
		}
	}
	r := request("POST", "/auth/login", loginJSON)
	r.RemoteAddr = "192.0.2.99:1"
	checkResponse(t, serve(h, r), 429)
}

func TestAdmissionIsNonblockingHealthBypassesAndCancelRecovers(t *testing.T) {
	entered := make(chan struct{}, 1)
	var calls atomic.Int64
	a := &fakeAuth{login: func(ctx context.Context, _ model.LoginInput) (model.Tokens, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			return model.Tokens{}, ctx.Err()
		}
		return tokens(), nil
	}}
	h := newHandler(t, a, nil, Config{MaxInflight: 1, RequestTimeout: 10 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- serve(h, request("POST", "/auth/login", loginJSON).WithContext(ctx)) }()
	select {
	case <-entered:
	case w := <-firstDone:
		t.Fatalf("first request never reached authentication; status=%d", w.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not enter authentication")
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/auth/login", loginJSON, 503}, {"GET", "/nodes", "", 503}, {"GET", "/health", "", 200},
	} {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- serve(h, authorized(tc.method, tc.path, tc.body)) }()
		select {
		case w := <-done:
			checkResponse(t, w, tc.status)
			if tc.status == 503 && w.Header().Get("Retry-After") != "1" {
				t.Error("overload must advertise Retry-After: 1")
			}
		case <-time.After(time.Second):
			t.Fatalf("%s queued behind an occupied admission slot", tc.path)
		}
	}
	if a.loginCalls.Load() != 1 || a.authCalls.Load() != 0 {
		t.Error("overloaded request reached dependencies")
	}
	cancel()
	select {
	case w := <-firstDone:
		checkResponse(t, w, 503)
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock accepted request")
	}
	checkResponse(t, serve(h, request("POST", "/auth/login", loginJSON)), 200)
}

func TestRequestTimeoutReleasesAdmissionAndPreservesParentCancellation(t *testing.T) {
	for _, parentShorter := range []bool{false, true} {
		t.Run(fmt.Sprint(parentShorter), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var captured context.Context
				var calls int
				a := &fakeAuth{login: func(ctx context.Context, _ model.LoginInput) (model.Tokens, error) {
					calls++
					captured = ctx
					if calls == 1 {
						<-ctx.Done()
						return model.Tokens{}, ctx.Err()
					}
					return tokens(), nil
				}}
				h := newHandler(t, a, nil, Config{MaxInflight: 1, RequestTimeout: 50 * time.Millisecond})
				parent := context.Background()
				var cancel context.CancelFunc
				if parentShorter {
					parent, cancel = context.WithTimeout(parent, 10*time.Millisecond)
					defer cancel()
				}
				started := time.Now()
				w := serve(h, request("POST", "/auth/login", loginJSON).WithContext(parent))
				checkResponse(t, w, 503)
				if w.Header().Get("Retry-After") != "1" {
					t.Error("timeout must advertise Retry-After: 1")
				}
				want := 50 * time.Millisecond
				if parentShorter {
					want = 10 * time.Millisecond
				}
				if elapsed := time.Since(started); elapsed != want {
					t.Errorf("timeout elapsed=%s, want %s", elapsed, want)
				}
				if captured == nil || !errors.Is(captured.Err(), context.DeadlineExceeded) {
					t.Error("dependency did not receive expired derived context")
				}
				checkResponse(t, serve(h, request("POST", "/auth/login", loginJSON)), 200)
				if captured == nil || !errors.Is(captured.Err(), context.Canceled) {
					t.Error("successful request did not cancel its derived context")
				}
			})
		})
	}
}

func TestSourceReceivesRequestContextAndAdmissionIsReleasedOnErrors(t *testing.T) {
	type contextKey struct{}
	parent := context.WithValue(context.Background(), contextKey{}, "request-value")
	var authContext context.Context
	a := &fakeAuth{authenticate: func(ctx context.Context, _ string) (model.Identity, error) { authContext = ctx; return identity(), nil }}
	var calls int
	s := &fakeSource{nodes: func(ctx context.Context, _, _ int64, limit int) (model.NodePage, error) {
		calls++
		if ctx.Value(contextKey{}) != "request-value" {
			t.Error("source lost parent request values")
		}
		deadline, ok := ctx.Deadline()
		authDeadline, authOK := authContext.Deadline()
		if !ok || !authOK || !deadline.Equal(authDeadline) {
			t.Error("authentication and source must share request deadline")
		}
		if calls == 1 {
			return model.NodePage{}, errors.New("private internal failure")
		}
		return model.NodePage{Items: []model.Node{}, Pagination: model.Pagination{PerPage: limit}}, nil
	}}
	h := newHandler(t, a, s, Config{MaxInflight: 1, RequestTimeout: time.Second})
	for _, status := range []int{503, 200} {
		checkResponse(t, serve(h, authorized("GET", "/nodes", "").WithContext(parent)), status)
	}
	if a.logoutCalls.Load() != 0 {
		t.Error("source failure revoked authentication")
	}
}
