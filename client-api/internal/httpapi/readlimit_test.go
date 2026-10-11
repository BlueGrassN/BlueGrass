package httpapi

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"bluegrass/client-api/internal/model"
)

func sessionAuth() *fakeAuth {
	return &fakeAuth{authenticate: func(_ context.Context, token string) (model.Identity, error) {
		id := identity()
		switch token {
		case "first-token", "rotated-first-token":
			id.Session.ID = "first-session"
		case "second-token":
			id.Session.ID = "second-session"
		default:
			return model.Identity{}, model.Fail("invalid_token", 401, "Invalid token")
		}
		return id, nil
	}}
}

func TestProtectedReadsShareSessionBudgetAndExcludeLogout(t *testing.T) {
	for _, tc := range []struct {
		name                string
		configured, allowed int
	}{
		{"default", 0, 120}, {"configured", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s := sessionAuth(), &fakeSource{}
			h := newHandler(t, a, s, Config{ReadPerMinute: tc.configured})
			call := func(method, path, token string) *httptest.ResponseRecorder {
				r := request(method, path, "")
				r.Header.Set("Authorization", "Bearer "+token)
				return serve(h, r)
			}
			paths := []string{"/me", "/nodes", "/nodes/7"}
			for i := 0; i < tc.allowed; i++ {
				checkResponse(t, call("GET", paths[i%len(paths)], "first-token"), 200)
			}
			for _, path := range paths {
				w := call("GET", path, "first-token")
				checkResponse(t, w, 429)
				retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
				if err != nil || retry < 1 || retry > 60 {
					t.Errorf("read throttle Retry-After = %q", w.Header().Get("Retry-After"))
				}
				checkNoSecrets(t, w, "first-token", "first-session")
			}
			// A rotated access token keeps the same authenticated session budget.
			checkResponse(t, call("GET", "/nodes", "rotated-first-token"), 429)
			if s.nodesCalls.Load() != int64(tc.allowed/3) || s.nodeCalls.Load() != int64(tc.allowed/3) {
				t.Error("denied reads reached the source")
			}
			// Same customer and peer, but a distinct valid session has its own quota.
			checkResponse(t, call("GET", "/nodes", "second-token"), 200)
			checkResponse(t, call("POST", "/auth/logout", "first-token"), 200)
			if a.logoutCalls.Load() != 1 || a.loginCalls.Load() != 0 || a.refreshCalls.Load() != 0 {
				t.Error("read throttling mutated authentication or blocked logout")
			}
		})
	}
}

func TestReadSessionMapIsBoundedAndExpiredKeysAreEvicted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, s := sessionAuth(), &fakeSource{}
		h := newHandler(t, a, s, Config{ReadPerMinute: 2, MaxRateKeys: 1})
		call := func(method, path, token string) *httptest.ResponseRecorder {
			r := request(method, path, "")
			r.Header.Set("Authorization", "Bearer "+token)
			return serve(h, r)
		}
		checkResponse(t, call("GET", "/nodes", "invalid-token"), 401)
		checkResponse(t, call("GET", "/nodes", "first-token"), 200)
		checkResponse(t, call("GET", "/nodes", "second-token"), 429)
		checkResponse(t, call("GET", "/nodes", "first-token"), 200)
		checkResponse(t, call("POST", "/auth/logout", "second-token"), 200)
		time.Sleep(61 * time.Second)
		checkResponse(t, call("GET", "/nodes", "second-token"), 200)
		checkResponse(t, call("GET", "/nodes", "first-token"), 429)
		if s.nodesCalls.Load() != 3 || a.logoutCalls.Load() != 1 {
			t.Error("read map capacity/expiry did not gate source calls")
		}
	})
}
