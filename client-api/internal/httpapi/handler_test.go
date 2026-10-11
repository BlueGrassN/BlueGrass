package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"bluegrass/client-api/internal/model"
)

func TestHealthAndMethodAwareRoutes(t *testing.T) {
	a, s := &fakeAuth{}, &fakeSource{}
	h := newHandler(t, a, s, Config{})
	checkResponse(t, serve(h, request("GET", "/health", "")), 200)
	if a.authCalls.Load() != 0 || s.byIDCalls.Load() != 0 {
		t.Fatal("health must not authenticate or query the source")
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/health", 405}, {"GET", "/auth/login", 405}, {"GET", "/auth/refresh", 405},
		{"POST", "/me", 405}, {"GET", "/auth/logout", 405}, {"POST", "/nodes", 405},
		{"POST", "/nodes/7", 405}, {"GET", "/missing", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) { checkResponse(t, serve(h, request(tc.method, tc.path, "")), tc.status) })
	}
	w := serve(h, request("HEAD", "/health", ""))
	if w.Code != 200 {
		t.Errorf("HEAD health status = %d", w.Code)
	}
	checkHeaders(t, w)
}

func TestLoginAndRefreshDelegateValidatedInputs(t *testing.T) {
	a := &fakeAuth{
		login: func(ctx context.Context, in model.LoginInput) (model.Tokens, error) {
			if in.Username != "alice" || in.Password != "private-password" || in.DeviceID != uuid || in.DeviceName != "Laptop" {
				t.Errorf("wrong login input: username=%q device=%q name=%q", in.Username, in.DeviceID, in.DeviceName)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("login context has no deadline")
			}
			return tokens(), nil
		},
		refresh: func(ctx context.Context, token, id string) (model.Tokens, error) {
			if token != "private-refresh-token" || id != uuid {
				t.Error("wrong refresh arguments")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("refresh context has no deadline")
			}
			return tokens(), nil
		},
	}
	h := newHandler(t, a, nil, Config{})
	for _, tc := range []struct{ path, body string }{{"/auth/login", loginJSON}, {"/auth/refresh", refreshJSON}} {
		w := serve(h, request("POST", tc.path, tc.body))
		got := checkResponse(t, w, 200)
		var data model.Tokens
		if err := json.Unmarshal(got.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.AccessToken != "issued-access" || data.RefreshToken != "issued-refresh" || data.Customer.ID != 41 || data.TokenType != "Bearer" {
			t.Error("token response was lost or altered")
		}
		checkNoSecrets(t, w, "private-password", "private-refresh-token", "private-phone", "password_hash")
	}
	if a.loginCalls.Load() != 1 || a.refreshCalls.Load() != 1 || a.authCalls.Load() != 0 {
		t.Error("public auth routes delegated incorrectly")
	}
}

func TestProtectedEndpointsUseAuthenticatedIdentityOnly(t *testing.T) {
	a := &fakeAuth{
		authenticate: func(ctx context.Context, token string) (model.Identity, error) {
			if token != "private-access-token" {
				t.Error("wrong bearer token")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("authentication context has no deadline")
			}
			return identity(), nil
		},
		logout: func(_ context.Context, id string) error {
			if id != "authenticated-session" {
				t.Errorf("logout session = %q", id)
			}
			return nil
		},
	}
	s := &fakeSource{
		byID: func(_ context.Context, id int64) (model.Customer, error) {
			if id != 41 {
				t.Errorf("me customer ID = %d", id)
			}
			return customer(), nil
		},
		nodes: func(ctx context.Context, id, after int64, limit int) (model.NodePage, error) {
			if id != 41 || after != 0 || limit != 100 {
				t.Errorf("nodes arguments = %d,%d,%d", id, after, limit)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("source context has no deadline")
			}
			return model.NodePage{Items: []model.Node{{ID: "7", Name: "Owned node"}}, Pagination: model.Pagination{PerPage: 100}}, nil
		},
		node: func(_ context.Context, id, nodeID int64) (model.Node, error) {
			if id != 41 || nodeID != 7 {
				t.Errorf("node arguments = %d,%d", id, nodeID)
			}
			return model.Node{ID: "7", Name: "Owned node"}, nil
		},
	}
	h := newHandler(t, a, s, Config{})
	for _, tc := range []struct{ method, path string }{
		{"GET", "/me?customer_id=999"}, {"GET", "/nodes?customer_id=999"},
		{"GET", "/nodes/7?customer_id=999"}, {"POST", "/auth/logout?customer_id=999&session_id=foreign-session"},
	} {
		w := serve(h, authorized(tc.method, tc.path, ""))
		got := checkResponse(t, w, 200)
		checkNoSecrets(t, w, "private-access-token", "private-phone", "private-password-hash", "foreign-session")
		if strings.HasPrefix(tc.path, "/me") {
			var c model.Customer
			if err := json.Unmarshal(got.Data, &c); err != nil {
				t.Fatal(err)
			}
			if c.ID != 41 || c.Username != "alice" {
				t.Error("me did not return authenticated customer")
			}
		}
	}
	if a.authCalls.Load() != 4 || a.logoutCalls.Load() != 1 || s.nodesCalls.Load() != 1 || s.nodeCalls.Load() != 1 {
		t.Error("wrong protected endpoint delegation")
	}
	if s.byLoginCalls.Load() != 0 || a.loginCalls.Load() != 0 || a.refreshCalls.Load() != 0 {
		t.Error("protected reads must not trigger login or refresh")
	}
}

func TestOnlyAuthorizationBearerCanAuthenticate(t *testing.T) {
	for _, header := range []string{"", "Basic private-access-token", "Bearer", "Bearer ", "Bearer one two"} {
		for _, tc := range []struct{ method, path string }{{"GET", "/me"}, {"GET", "/nodes"}, {"GET", "/nodes/7"}, {"POST", "/auth/logout"}} {
			t.Run(tc.path+"/"+header, func(t *testing.T) {
				a, s := &fakeAuth{}, &fakeSource{}
				r := request(tc.method, tc.path+"?access_token=private-access-token&token=private-access-token", "")
				r.Header.Set("Authorization", header)
				r.Header.Set("Cookie", "access_token=private-access-token")
				w := serve(newHandler(t, a, s, Config{}), r)
				checkResponse(t, w, 401)
				checkNoSecrets(t, w, "private-access-token")
				if a.authCalls.Load() != 0 || a.logoutCalls.Load() != 0 || s.nodesCalls.Load() != 0 || s.nodeCalls.Load() != 0 || s.byIDCalls.Load() != 0 {
					t.Error("invalid bearer reached a dependency")
				}
			})
		}
	}
}

func TestKeysetPaginationAndDetailValidation(t *testing.T) {
	for _, tc := range []struct {
		query string
		after int64
		limit int
	}{
		{"", 0, 100}, {"?page=1", 0, 100}, {"?per_page=1&cursor=NDI", 42, 1},
		{"?per_page=100&cursor=OTIyMzM3MjAzNjg1NDc3NTgwNw", 9223372036854775807, 100},
	} {
		t.Run(tc.query, func(t *testing.T) {
			s := &fakeSource{nodes: func(_ context.Context, id, after int64, limit int) (model.NodePage, error) {
				if id != 41 || after != tc.after || limit != tc.limit {
					t.Errorf("keyset arguments = %d,%d,%d", id, after, limit)
				}
				return model.NodePage{Items: []model.Node{{ID: "43"}}, Pagination: model.Pagination{PerPage: limit, HasMore: true, NextCursor: "NDM"}}, nil
			}}
			got := checkResponse(t, serve(newHandler(t, nil, s, Config{}), authorized("GET", "/nodes"+tc.query, "")), 200)
			var page model.NodePage
			if err := json.Unmarshal(got.Data, &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != "43" || !page.Pagination.HasMore || page.Pagination.NextCursor != "NDM" || page.Pagination.PerPage != tc.limit {
				t.Error("lost keyset pagination response")
			}
			if s.nodesCalls.Load() != 1 {
				t.Error("nodes must query source exactly once")
			}
		})
	}
	for _, path := range []string{
		"/nodes?page=2", "/nodes?page=0", "/nodes?page=-1", "/nodes?page=no",
		"/nodes?per_page=0", "/nodes?per_page=-1", "/nodes?per_page=101", "/nodes?per_page=x",
		"/nodes?cursor=%%%", "/nodes?cursor=YWJj", "/nodes?cursor=LTE", "/nodes?cursor=OTIyMzM3MjAzNjg1NDc3NTgwOA",
		"/nodes/0", "/nodes/-1", "/nodes/no", "/nodes/9223372036854775808",
	} {
		t.Run(path, func(t *testing.T) {
			s := &fakeSource{}
			checkResponse(t, serve(newHandler(t, nil, s, Config{}), authorized("GET", path, "")), 422)
			if s.nodesCalls.Load() != 0 || s.nodeCalls.Load() != 0 {
				t.Error("invalid pagination/detail reached source")
			}
		})
	}
}

type transientError struct{}

func (transientError) Error() string {
	return "internal-db-address:3306 private-password private-refresh-token private-access-token"
}
func (transientError) Timeout() bool   { return true }
func (transientError) Temporary() bool { return true }

func TestErrorMappingAndNoImplicitAuthMutations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"api", model.Fail("CUSTOM_ERROR", 409, "Request conflict"), 409, "CUSTOM_ERROR"},
		{"api-private-message", model.Fail("CUSTOM_ERROR", 409, "internal-db-address private-password private-refresh-token private-access-token"), 409, "CUSTOM_ERROR"},
		{"expired", model.Fail("TOKEN_EXPIRED", 401, "Token expired"), 401, "TOKEN_EXPIRED"},
		{"wrapped-api", fmt.Errorf("internal wrapper: %w", model.Fail("ACCOUNT_DISABLED", 403, "Account disabled")), 403, "ACCOUNT_DISABLED"},
		{"not-found", fmt.Errorf("internal wrapper: %w", model.ErrNotFound), 404, ""},
		{"canceled", context.Canceled, 503, ""}, {"deadline", context.DeadlineExceeded, 503, ""},
		{"wrapped-deadline", fmt.Errorf("internal wrapper: %w", context.DeadlineExceeded), 503, ""},
		{"transient", transientError{}, 503, ""},
		{"unexpected", errors.New("internal-db-address:3306 private-password private-refresh-token private-access-token"), 503, ""},
	} {
		for _, stage := range []string{"login", "refresh", "authenticate", "nodes", "node", "logout"} {
			t.Run(tc.name+"/"+stage, func(t *testing.T) {
				a, s := &fakeAuth{}, &fakeSource{}
				method, path, body := "GET", "/me", ""
				switch stage {
				case "login":
					method, path, body = "POST", "/auth/login", loginJSON
					a.login = func(context.Context, model.LoginInput) (model.Tokens, error) { return model.Tokens{}, tc.err }
				case "refresh":
					method, path, body = "POST", "/auth/refresh", refreshJSON
					a.refresh = func(context.Context, string, string) (model.Tokens, error) { return model.Tokens{}, tc.err }
				case "authenticate":
					a.authenticate = func(context.Context, string) (model.Identity, error) { return model.Identity{}, tc.err }
				case "nodes":
					path = "/nodes"
					s.nodes = func(context.Context, int64, int64, int) (model.NodePage, error) { return model.NodePage{}, tc.err }
				case "node":
					path = "/nodes/7"
					s.node = func(context.Context, int64, int64) (model.Node, error) { return model.Node{}, tc.err }
				case "logout":
					method, path = "POST", "/auth/logout"
					a.logout = func(context.Context, string) error { return tc.err }
				}
				w := serve(newHandler(t, a, s, Config{}), authorized(method, path, body))
				got := checkResponse(t, w, tc.status)
				if tc.code != "" && got.Error != nil && got.Error.Code != tc.code {
					t.Errorf("error code = %q, want %q", got.Error.Code, tc.code)
				}
				if tc.status == 503 && w.Header().Get("Retry-After") != "1" {
					t.Error("503 must advertise Retry-After: 1")
				}
				checkNoSecrets(t, w, "internal-db-address", "internal wrapper", "private-password", "private-refresh-token", "private-access-token")
				if stage != "logout" && a.logoutCalls.Load() != 0 {
					t.Error("failure implicitly logged out the session")
				}
				if stage != "login" && a.loginCalls.Load() != 0 {
					t.Error("failure implicitly logged in")
				}
				if stage != "refresh" && a.refreshCalls.Load() != 0 {
					t.Error("failure implicitly refreshed a token")
				}
				if stage == "authenticate" && (s.byIDCalls.Load() != 0 || s.nodesCalls.Load() != 0 || s.nodeCalls.Load() != 0) {
					t.Error("failed authentication reached the source")
				}
			})
		}
	}
}

func TestInvalidTrustedProxyCIDRRejectedAtConstruction(t *testing.T) {
	for _, cidr := range []string{"not-a-cidr", "127.0.0.1", "10.0.0.0/99"} {
		if _, err := New(&fakeAuth{}, &fakeSource{}, Config{TrustedProxyCIDRs: []string{cidr}}); err == nil {
			t.Errorf("New accepted invalid trusted proxy CIDR %q", cidr)
		}
	}
}

func TestNoncanonicalPathsReturnJSONWithoutReflectingQueryTokens(t *testing.T) {
	h := newHandler(t, nil, nil, Config{})
	for _, path := range []string{"/nodes//7", "/nodes/../me", "/nodes/%2e%2e/me"} {
		w := serve(h, authorized("GET", path+"?access_token=private-access-token", ""))
		checkResponse(t, w, 422)
		checkNoSecrets(t, w, "private-access-token")
		if w.Header().Get("Location") != "" {
			t.Error("API must not redirect and reflect query credentials")
		}
	}
}
