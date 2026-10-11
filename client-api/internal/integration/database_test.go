package integration

import (
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/source"
	"bluegrass/client-api/internal/testdb"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"sync"
	"testing"
	"time"
)

func repository(t *testing.T) (*testdb.Fixture, *source.Repository) {
	t.Helper()
	f := testdb.Open(t)
	f.SourceSchema(t)
	r, err := source.New(f.Read, source.Config{TablePrefix: f.Prefix, QueryTimeout: 2 * time.Second, SourceLocation: f.Location, Now: func() time.Time { return time.Now().UTC() }, PhoneRefreshInterval: 30 * time.Second, MaxPhoneEntries: 10000})
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func seed(t *testing.T, f *testdb.Fixture) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("fixture-password"), 4)
	f.Exec(t, "INSERT INTO `{p}customers` (id,username,phone,password,display_name,status) VALUES (1,'alice','15500000000',?,'Alice',1),(2,'bob','15500000001',?,'Bob',1)", string(hash), string(hash))
	f.Exec(t, "INSERT INTO `{p}proxy_ips` (id,asset_name,ip_address,port,auth_username,auth_password,protocol,country_code,country_name,status) VALUES (1,'Fixture','192.0.2.1',1080,'u0','p0','socks5','JP','Japan','assigned')")
	f.Exec(t, "INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status,has_forward,expires_at,started_at) VALUES (11,1,1,'active',1,'2099-01-01 00:00:00','2026-01-01 00:00:00')")
	f.Exec(t, "INSERT INTO `{p}ny_device_groups` (id,original_connect_host,is_enabled) VALUES (1,'relay.example.com',1)")
	f.Exec(t, "INSERT INTO `{p}forward_plans` (id,display_host) VALUES (1,NULL)")
	f.Exec(t, "INSERT INTO `{p}forward_rules` (id,subscription_id,proxy_ip_id,ny_device_group_id,forward_plan_id,status,listen_port) VALUES (1,11,1,1,1,'active',20001)")
}

func TestBusinessReadUsesMVCCWhileOtherProgramHoldsWriteLock(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	tx, err := f.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE `" + f.Prefix + "subscriptions` SET customer_id=2 WHERE id=11"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	node, err := r.Node(ctx, 1, 11)
	if err != nil || node.ID != "11" {
		t.Fatalf("read blocked by uncommitted writer: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = r.Node(context.Background(), 1, 11)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("old owner still authorized after commit: %v", err)
	}
	if _, err = r.Node(context.Background(), 2, 11); err != nil {
		t.Fatal("new owner not visible:", err)
	}
}

func TestJoinNeverMixesCredentialVersionsDuringConcurrentUpdates(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	fail := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 20; i++ {
			tx, e := f.DB.Begin()
			if e != nil {
				fail <- e
				return
			}
			if _, e = tx.Exec("UPDATE `"+f.Prefix+"proxy_ips` SET auth_username=? WHERE id=1", fmt.Sprintf("u%d", i)); e == nil {
				time.Sleep(time.Millisecond)
				_, e = tx.Exec("UPDATE `"+f.Prefix+"proxy_ips` SET auth_password=? WHERE id=1", fmt.Sprintf("p%d", i))
			}
			if e != nil {
				tx.Rollback()
				fail <- e
				return
			}
			if e = tx.Commit(); e != nil {
				fail <- e
				return
			}
		}
	}()
	for i := 0; i < 80; i++ {
		n, e := r.Node(context.Background(), 1, 11)
		if e != nil {
			t.Error(e)
			break
		}
		if strings.TrimPrefix(n.Username, "u") != strings.TrimPrefix(n.Password, "p") {
			t.Errorf("mixed committed snapshots: %s/%s", n.Username, n.Password)
			break
		}
	}
	<-done
	select {
	case e := <-fail:
		t.Fatal(e)
	default:
	}
	latest, e := r.Node(context.Background(), 1, 11)
	if e != nil || latest.Username != "u20" || latest.Password != "p20" {
		t.Fatal("final committed credentials were not visible", e)
	}
}

func TestExternalDisableAndHistoricalReleaseAfterRenewal(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	f.Exec(t, "UPDATE `{p}proxy_ips` SET released_at='2026-02-01 00:00:00',upstream_expires_at='2020-01-01 00:00:00' WHERE id=1")
	f.Exec(t, "UPDATE `{p}subscriptions` SET last_renewed_at='2026-03-01 00:00:00' WHERE id=11")
	if _, e := r.Node(context.Background(), 1, 11); e != nil {
		t.Fatal("renewed resource rejected by historical release/upstream expiry:", e)
	}
	f.Exec(t, "UPDATE `{p}customers` SET status=0 WHERE id=1")
	page, e := r.Nodes(context.Background(), 1, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 0 {
		t.Fatal("disabled customer received node credentials")
	}
}

func TestPhoneDirectoryRevalidatesCurrentDatabaseRow(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	if e := r.RefreshPhones(context.Background()); e != nil {
		t.Fatal(e)
	}
	c, e := r.CustomerByLogin(context.Background(), "15500000000")
	if e != nil || c.ID != 1 {
		t.Fatal("phone lookup failed", e)
	}
	f.Exec(t, "UPDATE `{p}customers` SET phone='15500000009' WHERE id=1")
	if _, e = r.CustomerByLogin(context.Background(), "15500000000"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("stale alias authorized", e)
	}
	if e = r.RefreshPhones(context.Background()); e != nil {
		t.Fatal(e)
	}
	if c, e = r.CustomerByLogin(context.Background(), "15500000009"); e != nil || c.ID != 1 {
		t.Fatal("phone update not reflected", e)
	}
}

func TestReadOnlyPoolEnforcedForEveryConnectionAndRecoversAfterQueueTimeout(t *testing.T) {
	f, _ := repository(t)
	seed(t, f)
	ctx := context.Background()
	connections := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		c, e := f.Read.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		connections = append(connections, c)
		var mode int
		if e = c.QueryRowContext(ctx, "SELECT @@session.transaction_read_only").Scan(&mode); e != nil || mode != 1 {
			t.Fatal("source connection is writable", e)
		}
	}
	defer func() {
		for _, c := range connections {
			c.Close()
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	var one int
	if e := f.Read.QueryRowContext(deadline, "SELECT 1").Scan(&one); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("pool wait did not respect deadline", e)
	}
	stats := f.Read.Stats()
	if stats.OpenConnections > 4 || stats.WaitCount < 1 {
		t.Fatal("pool budget not enforced", stats)
	}
	for _, c := range connections {
		c.Close()
	}
	connections = nil
	if e := f.Read.QueryRowContext(ctx, "SELECT 1").Scan(&one); e != nil || one != 1 {
		t.Fatal("pool failed to recover", e)
	}
	if _, e := f.Read.ExecContext(ctx, "UPDATE `"+f.Prefix+"customers` SET status=0 WHERE id=1"); e == nil {
		t.Fatal("read-only source accepted write")
	}
	var status int
	if e := f.DB.QueryRow("SELECT status FROM `" + f.Prefix + "customers` WHERE id=1").Scan(&status); e != nil || status != 1 {
		t.Fatal("business fixture was mutated", e)
	}
}

func TestParallelReadsStayWithinPoolBudget(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	var wg sync.WaitGroup
	fail := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, e := r.Nodes(context.Background(), 1, 0, 100)
			if e == nil && len(page.Items) != 1 {
				e = fmt.Errorf("missing expected node")
			}
			fail <- e
		}()
	}
	wg.Wait()
	close(fail)
	for e := range fail {
		if e != nil {
			t.Error(e)
		}
	}
	if stats := f.Read.Stats(); stats.OpenConnections > 4 || stats.InUse != 0 {
		t.Fatal("source connection cap/leak", stats)
	}
}
