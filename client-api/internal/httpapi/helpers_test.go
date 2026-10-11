package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

const (
	api         = "/api/client/v1"
	uuid        = "83b79b4b-f912-4d33-850e-9cfac817e83a"
	loginJSON   = `{"username":"alice","password":"private-password","device_id":"83b79b4b-f912-4d33-850e-9cfac817e83a","device_name":"Laptop"}`
	refreshJSON = `{"refresh_token":"private-refresh-token","request_id":"83b79b4b-f912-4d33-850e-9cfac817e83a"}`
)

type fakeAuth struct {
	login        func(context.Context, model.LoginInput) (model.Tokens, error)
	refresh      func(context.Context, string, string) (model.Tokens, error)
	authenticate func(context.Context, string) (model.Identity, error)
	logout       func(context.Context, string) error
	loginCalls   atomic.Int64
	refreshCalls atomic.Int64
	authCalls    atomic.Int64
	logoutCalls  atomic.Int64
}

var _ model.Authenticator = (*fakeAuth)(nil)

func customer() model.Customer {
	return model.Customer{ID: 41, Username: "alice", DisplayName: "Alice", Phone: "private-phone", PasswordHash: "private-password-hash", Status: 1}
}

func identity() model.Identity {
	return model.Identity{Customer: customer(), Session: model.Session{ID: "authenticated-session", CustomerID: 41}}
}

func tokens() model.Tokens {
	return model.Tokens{TokenType: "Bearer", AccessToken: "issued-access", RefreshToken: "issued-refresh", ExpiresIn: 900, AccessExpiresAt: time.Now().Add(15 * time.Minute), RefreshExpiresAt: time.Now().Add(24 * time.Hour), SessionID: "authenticated-session", Customer: customer()}
}

func (f *fakeAuth) Login(ctx context.Context, in model.LoginInput) (model.Tokens, error) {
	f.loginCalls.Add(1)
	if f.login != nil {
		return f.login(ctx, in)
	}
	return tokens(), nil
}

func (f *fakeAuth) Refresh(ctx context.Context, token, requestID string) (model.Tokens, error) {
	f.refreshCalls.Add(1)
	if f.refresh != nil {
		return f.refresh(ctx, token, requestID)
	}
	return tokens(), nil
}

func (f *fakeAuth) Authenticate(ctx context.Context, token string) (model.Identity, error) {
	f.authCalls.Add(1)
	if f.authenticate != nil {
		return f.authenticate(ctx, token)
	}
	return identity(), nil
}

func (f *fakeAuth) Logout(ctx context.Context, sessionID string) error {
	f.logoutCalls.Add(1)
	if f.logout != nil {
		return f.logout(ctx, sessionID)
	}
	return nil
}

type fakeSource struct {
	byID         func(context.Context, int64) (model.Customer, error)
	nodes        func(context.Context, int64, int64, int) (model.NodePage, error)
	node         func(context.Context, int64, int64) (model.Node, error)
	byLoginCalls atomic.Int64
	byIDCalls    atomic.Int64
	nodesCalls   atomic.Int64
	nodeCalls    atomic.Int64
}

var _ model.Source = (*fakeSource)(nil)

func (f *fakeSource) CustomerByLogin(context.Context, string) (model.Customer, error) {
	f.byLoginCalls.Add(1)
	return customer(), nil
}

func (f *fakeSource) CustomerByID(ctx context.Context, id int64) (model.Customer, error) {
	f.byIDCalls.Add(1)
	if f.byID != nil {
		return f.byID(ctx, id)
	}
	return customer(), nil
}

func (f *fakeSource) Nodes(ctx context.Context, customerID, afterID int64, limit int) (model.NodePage, error) {
	f.nodesCalls.Add(1)
	if f.nodes != nil {
		return f.nodes(ctx, customerID, afterID, limit)
	}
	return model.NodePage{Items: []model.Node{}, Pagination: model.Pagination{PerPage: limit}}, nil
}

func (f *fakeSource) Node(ctx context.Context, customerID, nodeID int64) (model.Node, error) {
	f.nodeCalls.Add(1)
	if f.node != nil {
		return f.node(ctx, customerID, nodeID)
	}
	return model.Node{ID: "7", Name: "Example", Protocol: "socks5", Host: "node.example.invalid", Port: 1080}, nil
}

func newHandler(t *testing.T, auth *fakeAuth, source *fakeSource, cfg Config) http.Handler {
	t.Helper()
	if auth == nil {
		auth = &fakeAuth{}
	}
	if source == nil {
		source = &fakeSource{}
	}
	h, err := New(auth, source, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h == nil {
		t.Fatal("New returned a nil handler")
	}
	return h
}

func request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, api+path, strings.NewReader(body))
	r.RemoteAddr = "198.51.100.10:4567"
	r.Header.Set("Content-Type", "application/json")
	return r
}

func authorized(method, path, body string) *http.Request {
	r := request(method, path, body)
	r.Header.Set("Authorization", "Bearer private-access-token")
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type envelope struct {
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ServerTime json.RawMessage `json:"server_time"`
}

func checkHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Error("response must have Cache-Control: no-store")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("response must have X-Content-Type-Options: nosniff")
	}
}

func checkResponse(t *testing.T, w *httptest.ResponseRecorder, status int) envelope {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP status = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	checkHeaders(t, w)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Error("response must be JSON")
	}
	var got envelope
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON envelope: %v", err)
	}
	if got.Success == nil {
		t.Fatal("envelope is missing success")
	}
	if len(got.ServerTime) == 0 || string(got.ServerTime) == "null" || string(got.ServerTime) == `""` {
		t.Error("envelope is missing server_time")
	}
	if status >= 200 && status < 300 {
		if !*got.Success || got.Error != nil || len(got.Data) == 0 {
			t.Errorf("invalid success envelope: %s", w.Body.String())
		}
	} else {
		if *got.Success || got.Error == nil || got.Error.Code == "" || got.Error.Message == "" {
			t.Errorf("invalid error envelope: %s", w.Body.String())
		}
		if len(got.Data) != 0 && string(got.Data) != "null" {
			t.Error("error response must not include data")
		}
	}
	return got
}

func checkNoSecrets(t *testing.T, w *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("response leaked secret %q", secret)
		}
		for _, values := range w.Header() {
			for _, value := range values {
				if strings.Contains(value, secret) {
					t.Errorf("response header leaked secret %q", secret)
				}
			}
		}
	}
}
