package auth

import (
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/secure"
	"bluegrass/client-api/internal/session"
	"bluegrass/client-api/internal/testdb"
	"context"
	"database/sql"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"sync"
	"testing"
	"time"
)

type memorySource struct {
	mu          sync.RWMutex
	customer    model.Customer
	unavailable bool
}

func (m *memorySource) CustomerByID(ctx context.Context, id int64) (model.Customer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavailable {
		return model.Customer{}, errors.New("fixture unavailable")
	}
	if m.customer.ID != id {
		return model.Customer{}, model.ErrNotFound
	}
	return m.customer, nil
}
func (m *memorySource) CustomerByLogin(ctx context.Context, name string) (model.Customer, error) {
	return m.CustomerByID(ctx, 1)
}
func (m *memorySource) Nodes(context.Context, int64, int64, int) (model.NodePage, error) {
	return model.NodePage{}, nil
}
func (m *memorySource) Node(context.Context, int64, int64) (model.Node, error) {
	return model.Node{}, model.ErrNotFound
}
func (m *memorySource) change(fn func(*model.Customer)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.customer)
}
func config() Config {
	return Config{Key: secure.Digest("fixture-key"), AccessTTL: 30 * time.Minute, RefreshIdle: 180 * 24 * time.Hour, RetryWindow: 120 * time.Second, HashWait: time.Second, HashWorkers: 2, Now: func() time.Time { return time.Now().UTC() }}
}
func sourceFixture() *memorySource {
	hash, _ := bcrypt.GenerateFromPassword([]byte("fixture-password"), 4)
	return &memorySource{customer: model.Customer{ID: 1, Username: "alice", Phone: "15500000000", DisplayName: "Alice", PasswordHash: string(hash), Status: 1}}
}
func input() model.LoginInput {
	return model.LoginInput{Username: "alice", Password: "fixture-password", DeviceID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", DeviceName: "test"}
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var api *model.APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func serviceFixture(t *testing.T) (*Service, *memorySource, *session.Store, *time.Time) {
	t.Helper()
	f := testdb.Open(t)
	cfg := config()
	now := time.Now().UTC().Truncate(time.Second)
	cfg.Now = func() time.Time { return now }
	store, err := session.New(f.DB, session.Config{Prefix: f.Prefix, Key: cfg.Key, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := sourceFixture()
	svc, err := New(src, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc, src, store, &now
}

func TestActiveSessionRefreshesBeyondOneYearAndLogoutRevokes(t *testing.T) {
	svc, _, _, now := serviceFixture(t)
	tokens, err := svc.Login(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		*now = now.Add(150 * 24 * time.Hour)
		id, _ := secure.UUID()
		tokens, err = svc.Refresh(context.Background(), tokens.RefreshToken, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	identity, err := svc.Authenticate(context.Background(), tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Logout(context.Background(), identity.Session.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Authenticate(context.Background(), tokens.AccessToken)
	assertCode(t, err, "session_revoked")
}

func TestExpiredAccessCanRefreshButIdleSessionExpires(t *testing.T) {
	svc, _, _, now := serviceFixture(t)
	tokens, err := svc.Login(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Minute)
	_, err = svc.Authenticate(context.Background(), tokens.AccessToken)
	assertCode(t, err, "access_token_expired")
	id, _ := secure.UUID()
	tokens, err = svc.Refresh(context.Background(), tokens.RefreshToken, id)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(180 * 24 * time.Hour)
	id, _ = secure.UUID()
	_, err = svc.Refresh(context.Background(), tokens.RefreshToken, id)
	assertCode(t, err, "session_expired")
}

func TestExternalPasswordOrStatusChangeInvalidatesExistingTokens(t *testing.T) {
	svc, src, _, _ := serviceFixture(t)
	tokens, err := svc.Login(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	src.change(func(c *model.Customer) { c.PasswordHash = "changed by another application" })
	_, err = svc.Authenticate(context.Background(), tokens.AccessToken)
	assertCode(t, err, "credentials_changed")
	id, _ := secure.UUID()
	_, err = svc.Refresh(context.Background(), tokens.RefreshToken, id)
	assertCode(t, err, "session_revoked")
}

func TestDatabaseOutageDoesNotConsumeRefreshToken(t *testing.T) {
	svc, src, _, _ := serviceFixture(t)
	tokens, err := svc.Login(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.unavailable = true
	src.mu.Unlock()
	id, _ := secure.UUID()
	if _, err = svc.Refresh(context.Background(), tokens.RefreshToken, id); err == nil {
		t.Fatal("outage accepted")
	}
	src.mu.Lock()
	src.unavailable = false
	src.mu.Unlock()
	if _, err = svc.Refresh(context.Background(), tokens.RefreshToken, id); err != nil {
		t.Fatal("outage destroyed session:", err)
	}
}

func TestConcurrentExternalPasswordChangeDuringLoginIsRechecked(t *testing.T) {
	src := sourceFixture()
	cfg := config()
	cfg.Verify = func(hash, pass string) (bool, error) {
		src.change(func(c *model.Customer) { c.PasswordHash = "external-password-update" })
		return true, nil
	}
	db, _ := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	defer db.Close()
	store, _ := session.New(db, session.Config{Prefix: "bg_test_", Key: cfg.Key, Timeout: time.Second})
	svc, err := New(src, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Login(context.Background(), input())
	assertCode(t, err, "credentials_changed")
}

func TestPasswordAdmissionIsBoundedAndCancellationRecovers(t *testing.T) {
	src := sourceFixture()
	cfg := config()
	cfg.HashWorkers = 1
	cfg.HashWait = 30 * time.Millisecond
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	cfg.Verify = func(hash, pass string) (bool, error) { entered <- struct{}{}; <-release; return false, nil }
	db, _ := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	defer db.Close()
	store, _ := session.New(db, session.Config{Prefix: "bg_test_", Key: cfg.Key, Timeout: time.Second})
	svc, err := New(src, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { _, e := svc.Login(context.Background(), input()); first <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("password worker did not start")
	}
	_, err = svc.Login(context.Background(), input())
	assertCode(t, err, "service_busy")
	close(release)
	assertCode(t, <-first, "invalid_credentials")
	_, err = svc.Login(context.Background(), input())
	assertCode(t, err, "invalid_credentials")
}

func TestDisabledAccountIsRecheckedAfterPasswordVerification(t *testing.T) {
	src := sourceFixture()
	cfg := config()
	cfg.Verify = func(hash, pass string) (bool, error) {
		src.change(func(c *model.Customer) { c.Status = 0 })
		return true, nil
	}
	db, _ := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	defer db.Close()
	store, _ := session.New(db, session.Config{Prefix: "bg_test_", Key: cfg.Key, Timeout: time.Second})
	svc, err := New(src, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Login(context.Background(), input())
	assertCode(t, err, "account_disabled")
}

func TestAccountWithoutPasswordCannotUseDummyComparisonResult(t *testing.T) {
	src := sourceFixture()
	src.change(func(c *model.Customer) { c.PasswordHash = "" })
	cfg := config()
	cfg.Verify = func(string, string) (bool, error) { return true, nil }
	db, _ := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	defer db.Close()
	store, _ := session.New(db, session.Config{Prefix: "bg_test_", Key: cfg.Key, Timeout: time.Second})
	svc, err := New(src, store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Login(context.Background(), input())
	assertCode(t, err, "invalid_credentials")
}
