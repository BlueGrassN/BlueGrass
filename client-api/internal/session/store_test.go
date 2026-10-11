package session

import (
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/testdb"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"
)

func digest(v string) []byte { h := sha256.Sum256([]byte(v)); return h[:] }
func fixture(t *testing.T) (*Store, *testdb.Fixture, model.Session, []byte) {
	t.Helper()
	f := testdb.Open(t)
	s, err := New(f.DB, Config{f.Prefix, digest("fixture-encryption-key"), 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	row := model.Session{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CustomerID: 1, DeviceHash: digest("device"), DeviceName: "fixture", CredentialFingerprint: digest("password"), AccessHash: digest("access0"), AccessExpiresAt: now.Add(30 * time.Minute), IdleExpiresAt: now.Add(180 * 24 * time.Hour)}
	refresh := digest("refresh0")
	if err = s.Create(context.Background(), row, refresh, now); err != nil {
		t.Fatal(err)
	}
	return s, f, row, refresh
}
func rotation(row model.Session, n int64) model.Rotation {
	now := time.Now().UTC().Truncate(time.Second)
	row.Generation = n
	row.AccessHash = digest("new-access")
	row.AccessExpiresAt = now.Add(30 * time.Minute)
	row.IdleExpiresAt = now.Add(180 * 24 * time.Hour)
	return model.Rotation{Session: row, RefreshHash: digest("new-refresh"), Now: now, RetryExpiresAt: now.Add(120 * time.Second), Tokens: model.Tokens{TokenType: "Bearer", SessionID: row.ID, AccessToken: "new-access", RefreshToken: "new-refresh", Customer: model.Customer{ID: 1, Username: "alice", Status: 1}}}
}

func TestConcurrentSameRefreshReturnsIdenticalResult(t *testing.T) {
	s, _, row, old := fixture(t)
	r := rotation(row, 1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	other, err := New(s.db, s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 20)
	tokens := make(chan model.Tokens, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		store := s
		if i%2 == 1 {
			store = other
		}
		go func(store *Store) {
			defer wg.Done()
			<-start
			v, e := store.Rotate(context.Background(), old, digest("same-request"), r)
			errs <- e
			tokens <- v
		}(store)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(tokens)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	for tok := range tokens {
		if tok.AccessToken != "new-access" || tok.RefreshToken != "new-refresh" {
			t.Fatal("refresh response diverged")
		}
	}
	if s.db.Stats().OpenConnections > 4 {
		t.Fatal("pool cap exceeded")
	}
}

func TestRefreshReplayRevocationSurvivesTransactionCommit(t *testing.T) {
	s, _, row, old := fixture(t)
	r := rotation(row, 1)
	if _, err := s.Rotate(context.Background(), old, digest("request-one"), r); err != nil {
		t.Fatal(err)
	}
	_, err := s.Rotate(context.Background(), old, digest("request-two"), r)
	var api *model.APIError
	if !errors.As(err, &api) || api.Code != "refresh_reuse_detected" {
		t.Fatalf("expected replay error, got %v", err)
	}
	stored, err := s.Access(context.Background(), r.Session.AccessHash)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RevokedAt == nil {
		t.Fatal("revocation was rolled back")
	}
}

func TestConcurrentSameDeviceLoginLeavesOneActiveSession(t *testing.T) {
	s, f, row, _ := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := row
			copy.ID = stringID(i)
			copy.AccessHash = digest(copy.ID)
			errs <- s.Create(context.Background(), copy, digest("refresh-"+copy.ID), time.Now().UTC())
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	var count int
	if err := f.DB.QueryRow("SELECT COUNT(*) FROM `" + f.Prefix + "sessions` WHERE revoked_at IS NULL").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%d active sessions for the same device", count)
	}
}

func stringID(i int) string { return "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb" + string(rune('0'+i)) }

func TestUnrelatedSessionsDoNotShareARowLock(t *testing.T) {
	s, f, row, _ := fixture(t)
	other := row
	other.ID = stringID(1)
	other.DeviceHash = digest("other-device")
	other.AccessHash = digest("other-access")
	if err := s.Create(context.Background(), other, digest("other-refresh"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	tx, err := f.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRow("SELECT id FROM `"+f.Prefix+"sessions` WHERE id=? FOR UPDATE", row.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = s.Rotate(ctx, digest("other-refresh"), digest("request"), rotation(other, 1)); err != nil {
		t.Fatal("unrelated session blocked:", err)
	}
}

func TestStoreDeadlineBoundsBlockedRowLock(t *testing.T) {
	s, f, row, old := fixture(t)
	s.cfg.Timeout = 100 * time.Millisecond
	tx, err := f.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRow("SELECT id FROM `"+f.Prefix+"sessions` WHERE id=? FOR UPDATE", row.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = s.Rotate(context.Background(), old, digest("request"), rotation(row, 1))
	if err == nil {
		t.Fatal("locked row unexpectedly refreshed")
	}
	if time.Since(start) > 700*time.Millisecond {
		t.Fatal("transaction deadline did not cancel the row-lock query")
	}
}
