package source

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

var _ model.Source = (*Repository)(nil)

func TestNewRejectsUnsafeTablePrefixes(t *testing.T) {
	for _, prefix := range []string{"UPPER_", "with-dash_", "space ", "x;drop", "x`", "汉字", strings.Repeat("a", 25)} {
		t.Run(prefix, func(t *testing.T) {
			db, _ := openFixture(t, time.Second)
			if _, err := New(db, Config{TablePrefix: prefix}); err == nil {
				t.Fatal("unsafe prefix accepted")
			}
		})
	}
}

func TestNewRejectsNilDBAndNegativeBudgets(t *testing.T) {
	if _, err := New(nil, Config{}); err == nil {
		t.Fatal("nil database accepted")
	}
	for _, cfg := range []Config{{QueryTimeout: -1}, {PhoneRefreshInterval: -1}, {MaxPhoneEntries: -1}} {
		db, _ := openFixture(t, time.Second)
		if _, err := New(db, cfg); err == nil {
			t.Errorf("negative budget accepted: %+v", cfg)
		}
	}
}

func TestNewDefaultsAndSafePrefixApplyWithoutOpeningDatabase(t *testing.T) {
	for _, prefix := range []string{"", "tenant_09_", strings.Repeat("a", 24)} {
		t.Run(prefix, func(t *testing.T) {
			db, script := openFixture(t, 5*time.Second)
			repo, err := New(db, Config{TablePrefix: prefix})
			if err != nil {
				t.Fatal(err)
			}
			if db.Stats().OpenConnections != 0 {
				t.Fatal("constructor opened a connection")
			}
			script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", "hash-a", 1)), check: func(q string, _ []driver.NamedValue) error {
				return requireSQL(q, `\bfrom `+prefix+`customers\b`)
			}})
			got, err := repo.CustomerByID(context.Background(), 7)
			if err != nil || got.ID != 7 {
				t.Fatalf("current customer: %+v, %v", got, err)
			}
		})
	}
}

func TestCustomerByLoginUsesIndexedUsernameBeforePhone(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(2), "15500000009"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	script.expect(queryStep{kind: "username", args: []driver.Value{"15500000009"}, rows: customerRows(customerRow(9, "other-phone", "current-hash", 1))})
	got, err := repo.CustomerByLogin(context.Background(), "15500000009")
	if err != nil || got.ID != 9 || got.PasswordHash != "current-hash" {
		t.Fatalf("username must take precedence: %+v, %v", got, err)
	}
}

func TestCustomerByIDReadsCurrentHashStatusAndDeletionEveryTime(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(
		queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", "old-hash", 1))},
		queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000008", "new-hash", 0))},
		queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows()},
	)
	first, err := repo.CustomerByID(context.Background(), 7)
	if err != nil || first.Status != 1 || first.PasswordHash != "old-hash" {
		t.Fatalf("initial read: %+v, %v", first, err)
	}
	second, err := repo.CustomerByID(context.Background(), 7)
	if err != nil || second.Status != 0 || second.PasswordHash != "new-hash" || second.Phone != "15500000008" {
		t.Fatalf("concurrent update was cached: %+v, %v", second, err)
	}
	_, err = repo.CustomerByID(context.Background(), 7)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("deleted customer: %v", err)
	}
}

func TestCustomerNullOptionalFieldsAndDisabledLoginRemainCurrent(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(queryStep{kind: "username", args: []driver.Value{"fixture-user"}, rows: customerRows([]driver.Value{int64(7), "fixture-user", nil, nil, "current-hash", int64(0)})})
	got, err := repo.CustomerByLogin(context.Background(), "fixture-user")
	if err != nil || got.Status != 0 || got.PasswordHash != "current-hash" || got.Phone != "" {
		t.Fatalf("disabled row must reach auth with current status: %+v, %v", got, err)
	}
}

func TestCustomerDatabaseErrorDoesNotBecomeNotFoundOrPhoneFallback(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	want := errors.New("fixture database unavailable")
	script.expect(queryStep{kind: "username", args: []driver.Value{"fixture-user"}, err: want})
	_, err := repo.CustomerByLogin(context.Background(), "fixture-user")
	if !errors.Is(err, want) {
		t.Fatalf("database error lost: %v", err)
	}
}

func TestSourceQueryDeadlineCancelsBlockedRead(t *testing.T) {
	repo, script := repositoryFixture(t, func(cfg *Config) { cfg.QueryTimeout = 15 * time.Millisecond })
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, block: make(chan struct{}), rows: customerRows()})
	_, err := repo.CustomerByID(context.Background(), 7)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query timeout must cancel database read: %v", err)
	}
}

func TestSourcePreservesEarlierCallerDeadline(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, block: make(chan struct{}), rows: customerRows()})
	_, err := repo.CustomerByID(ctx, 7)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}

func assertAPIError(t *testing.T, err error, code string, status int) {
	t.Helper()
	var api *model.APIError
	if !errors.As(err, &api) || api.Code != code || api.Status != status {
		t.Fatalf("want %s/%d, got %v", code, status, err)
	}
}

func assertSourceNow(value any) error {
	switch v := value.(type) {
	case time.Time:
		_, offset := v.Zone()
		if v.Equal(fixtureNow) && v.Hour() == 12 && offset == 8*60*60 {
			return nil
		}
	case string:
		for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
			parsed, err := time.ParseInLocation(layout, v, fixtureLocation)
			if err == nil && parsed.Equal(fixtureNow) {
				return nil
			}
		}
	}
	return fmt.Errorf("expiry query must bind Now in SourceLocation, got %v", value)
}
