package source

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

func TestStalePhoneCannotAuthorizeAfterConcurrentChange(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectLoginMiss(script, "15500000007")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000008", "new-hash", 1))})
	_, err := repo.CustomerByLogin(context.Background(), "15500000007")
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("stale phone authorized current row: %v", err)
	}
}

func TestPhoneDirectoryNeverCachesHashesStatusOrDeletedCustomers(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		hash   string
		status int64
	}{{"old-hash", 1}, {"new-hash", 0}} {
		expectLoginMiss(script, "15500000007")
		script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", state.hash, state.status))})
		got, err := repo.CustomerByLogin(context.Background(), "15500000007")
		if err != nil || got.PasswordHash != state.hash || got.Status != int(state.status) {
			t.Fatalf("stale account data: %+v, %v", got, err)
		}
	}
	expectLoginMiss(script, "15500000007")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows()})
	if _, err := repo.CustomerByLogin(context.Background(), "15500000007"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("deleted customer: %v", err)
	}
}

func TestPhoneMissDoesNotScanOrRefreshOnLogin(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	for i := 0; i < 3; i++ {
		expectLoginMiss(script, "15500000008")
		if _, err := repo.CustomerByLogin(context.Background(), "15500000008"); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("phone miss: %v", err)
		}
	}
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(8), "15500000008"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectLoginMiss(script, "15500000008")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(8)}, rows: customerRows(customerRow(8, "15500000008", "current-hash", 1))})
	got, err := repo.CustomerByLogin(context.Background(), "15500000008")
	if err != nil || got.ID != 8 {
		t.Fatalf("phone did not propagate on refresh: %+v, %v", got, err)
	}
}

func TestPhoneDuplicatesChooseLowestIDAndSkipEmptyPhones(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(phoneBatch(0, phoneRows(
		[]driver.Value{int64(1), nil}, []driver.Value{int64(2), ""},
		[]driver.Value{int64(3), "15500000009"}, []driver.Value{int64(9), "15500000009"},
	)))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectLoginMiss(script, "15500000009")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(3)}, rows: customerRows(customerRow(3, "15500000009", "low-id-hash", 1))})
	got, err := repo.CustomerByLogin(context.Background(), "15500000009")
	if err != nil || got.ID != 3 {
		t.Fatalf("duplicate phone chose wrong customer: %+v, %v", got, err)
	}
}

func TestRefreshPhonesUsesBoundedIDKeysetBatches(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	rows := phoneRows()
	for i := int64(1); i <= 2000; i++ {
		rows.values = append(rows.values, []driver.Value{i, fmt.Sprintf("fixture-phone-%d", i)})
	}
	script.expect(phoneBatch(0, rows), phoneBatch(2000, phoneRows([]driver.Value{int64(8001), "15500008001"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectLoginMiss(script, "15500008001")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(8001)}, rows: customerRows(customerRow(8001, "15500008001", "hash", 1))})
	got, err := repo.CustomerByLogin(context.Background(), "15500008001")
	if err != nil || got.ID != 8001 {
		t.Fatalf("keyset tail missing: %+v, %v", got, err)
	}
}

func TestFailedPhoneRefreshKeepsOldDirectoryAndReturnsError(t *testing.T) {
	for _, failure := range []string{"query", "rows", "scan", "capacity"} {
		t.Run(failure, func(t *testing.T) {
			repo, script := repositoryFixture(t, func(cfg *Config) { cfg.MaxPhoneEntries = 2 })
			script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"})))
			if err := repo.RefreshPhones(context.Background()); err != nil {
				t.Fatal(err)
			}
			step := phoneBatch(0, phoneRows([]driver.Value{int64(8), "15500000008"}))
			want := errors.New("fixture refresh failed")
			switch failure {
			case "query":
				step.err = want
			case "rows":
				step.rows.readErr = want
			case "scan":
				step.rows.values = [][]driver.Value{{"not-an-id", "15500000008"}}
			case "capacity":
				step.rows.values = append(step.rows.values, []driver.Value{int64(9), "15500000009"}, []driver.Value{int64(10), "15500000010"})
			}
			script.expect(step)
			if err := repo.RefreshPhones(context.Background()); err == nil {
				t.Fatal("failed refresh reported success")
			}
			expectLoginMiss(script, "15500000007")
			script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", "fresh-hash", 1))})
			got, err := repo.CustomerByLogin(context.Background(), "15500000007")
			if err != nil || got.ID != 7 {
				t.Fatalf("failed refresh replaced old map: %+v, %v", got, err)
			}
			expectLoginMiss(script, "15500000008")
			if _, err := repo.CustomerByLogin(context.Background(), "15500000008"); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("partial directory published: %v", err)
			}
		})
	}
}

func TestEmptySuccessfulPhoneRefreshRemovesOldMappings(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"})), phoneBatch(0, phoneRows()))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	expectLoginMiss(script, "15500000007")
	if _, err := repo.CustomerByLogin(context.Background(), "15500000007"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("removed mapping survived refresh: %v", err)
	}
}

func TestPhoneDirectoryPublishesAtomicallyWhileLoginsContinue(t *testing.T) {
	repo, script := repositoryFixture(t, func(cfg *Config) { cfg.QueryTimeout = time.Second })
	script.expect(phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"})))
	if err := repo.RefreshPhones(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := phoneRows()
	for i := int64(1); i <= 2000; i++ {
		first.values = append(first.values, []driver.Value{i, fmt.Sprintf("new-phone-%d", i)})
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	tail := phoneBatch(2000, phoneRows())
	tail.entered, tail.block = entered, release
	script.expect(phoneBatch(0, first), tail)
	done := make(chan error, 1)
	go func() { done <- repo.RefreshPhones(context.Background()) }()
	select {
	case <-entered:
	case err := <-done:
		close(release)
		t.Fatalf("refresh exited before second batch: %v", err)
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("refresh never reached second batch")
	}
	expectLoginMiss(script, "15500000007")
	script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", "fresh-hash", 1))})
	got, err := repo.CustomerByLogin(context.Background(), "15500000007")
	close(release)
	refreshErr := <-done
	if err != nil || got.ID != 7 {
		t.Fatalf("in-progress refresh exposed partial map or blocked login: %+v, %v", got, err)
	}
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	expectLoginMiss(script, "15500000007")
	if _, err := repo.CustomerByLogin(context.Background(), "15500000007"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("new complete directory not published: %v", err)
	}
}

func TestRunPhoneRefreshRefreshesAndStopsOnCancellation(t *testing.T) {
	repo, script := repositoryFixture(t, func(cfg *Config) { cfg.PhoneRefreshInterval = 25 * time.Millisecond })
	entered := make(chan struct{}, 1)
	step := phoneBatch(0, phoneRows([]driver.Value{int64(7), "15500000007"}))
	step.entered = entered
	script.expect(step)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { repo.RunPhoneRefresh(ctx); close(done) }()
	select {
	case <-entered:
		cancel()
	case <-done:
		t.Fatal("refresh loop returned without refreshing")
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("refresh loop never refreshed")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh loop ignored cancellation")
	}
}
