// Package testdb creates isolated, uniquely named test tables only in Hytron's suntest DB.
package testdb

import (
	"bluegrass/client-api/internal/dbpool"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
)

type Fixture struct {
	DB       *sql.DB
	Read     *sql.DB
	Prefix   string
	Location *time.Location
}

func Open(t *testing.T) *Fixture {
	t.Helper()
	path := os.Getenv("GO_API_TEST_CONFIG")
	if path == "" {
		t.Skip("set GO_API_TEST_CONFIG for isolated MySQL integration tests")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg dbpool.Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal("invalid test config")
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 3306 || cfg.Database != "suntest" {
		t.Fatal("integration tests are restricted to Hytron loopback:3306 suntest")
	}
	cfg.MaxOpen = 4
	cfg.MaxIdle = 2
	loc, _ := time.LoadLocation("Asia/Shanghai")
	db, err := dbpool.Open(cfg, false, loc)
	if err != nil {
		t.Fatal(err)
	}
	read, err := dbpool.Open(cfg, true, loc)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	var b [6]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	f := &Fixture{DB: db, Read: read, Prefix: "bg_it_" + hex.EncodeToString(b[:]) + "_", Location: loc}
	t.Cleanup(func() {
		read.Close()
		db.Close()
		// Test-only DDL has different I/O costs from bounded application queries.
		// Do not weaken the application's 2-3s query/transaction deadlines to
		// accommodate fsync-heavy DROP TABLE during parallel race tests.
		cleanupConfig := mysql.NewConfig()
		cleanupConfig.User = cfg.User
		cleanupConfig.Passwd = cfg.Password
		cleanupConfig.DBName = cfg.Database
		cleanupConfig.Net = "tcp"
		cleanupConfig.Addr = "127.0.0.1:3306"
		cleanupConfig.Timeout = 3 * time.Second
		cleanupConfig.ReadTimeout = 20 * time.Second
		cleanupConfig.WriteTimeout = 20 * time.Second
		cleanup, err := sql.Open("mysql", cleanupConfig.FormatDSN())
		if err != nil {
			t.Error("cannot open fixture cleanup pool")
			return
		}
		defer cleanup.Close()
		cleanup.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// Only tables with this invocation's unguessable prefix can be dropped.
		if !strings.HasPrefix(f.Prefix, "bg_it_") || len(f.Prefix) != 19 {
			t.Error("invalid cleanup prefix")
			return
		}
		for _, name := range []string{"device_sessions", "refresh_tokens", "sessions", "forward_rules", "ny_device_groups", "forward_plans", "spark_instances", "ipipv_instances", "subscriptions", "proxy_ips", "customers"} {
			if _, err := cleanup.ExecContext(ctx, "DROP TABLE IF EXISTS `"+f.Prefix+name+"`"); err != nil {
				t.Errorf("fixture cleanup %s%s failed: %v", f.Prefix, name, err)
			}
		}
	})
	return f
}

func (f *Fixture) SourceSchema(t *testing.T) {
	t.Helper()
	tables := map[string]string{
		"customers":        "id BIGINT PRIMARY KEY, username VARCHAR(255) NOT NULL, phone VARCHAR(32), password VARCHAR(255), display_name VARCHAR(255), customer_name VARCHAR(255), status INT NOT NULL DEFAULT 1, deleted_at DATETIME NULL, UNIQUE KEY(username)",
		"proxy_ips":        "id BIGINT PRIMARY KEY, asset_name VARCHAR(255), ip_address VARCHAR(64), port INT, auth_username VARCHAR(255), auth_password VARCHAR(255), socks5_info VARCHAR(1000), protocol VARCHAR(32), country_code VARCHAR(8), country_name VARCHAR(255), city VARCHAR(255), status VARCHAR(32), assigned_customer_id BIGINT, access_type VARCHAR(20) DEFAULT 'dedicated', shared_user_count INT DEFAULT 0, max_shared_users INT DEFAULT 1, spark_instance_id VARCHAR(100), ipipv_instance_id VARCHAR(100), deleted_at DATETIME NULL, released_at DATETIME NULL, upstream_expires_at DATETIME NULL, KEY(assigned_customer_id)",
		"subscriptions":    "id BIGINT PRIMARY KEY, customer_id BIGINT NOT NULL, proxy_ip_id BIGINT, status VARCHAR(32), product_snapshot JSON, expires_at DATETIME NULL, started_at DATETIME NULL, last_renewed_at DATETIME NULL, has_forward BOOL, customer_remark VARCHAR(255), custom_host VARCHAR(255), KEY(customer_id), KEY(proxy_ip_id)",
		"spark_instances":  "id BIGINT PRIMARY KEY, proxy_ip_id BIGINT, instance_id VARCHAR(100), KEY(proxy_ip_id)",
		"ipipv_instances":  "id BIGINT PRIMARY KEY, proxy_ip_id BIGINT, instance_no VARCHAR(100), KEY(proxy_ip_id)",
		"forward_plans":    "id BIGINT PRIMARY KEY, display_host VARCHAR(255)",
		"ny_device_groups": "id BIGINT PRIMARY KEY, custom_connect_host VARCHAR(255), original_connect_host VARCHAR(255), is_enabled INT",
		"forward_rules":    "id BIGINT PRIMARY KEY, subscription_id BIGINT, proxy_ip_id BIGINT, ny_device_group_id BIGINT, forward_plan_id BIGINT, status VARCHAR(32), listen_port INT, KEY(subscription_id,status)",
	}
	for table, columns := range tables {
		if _, err := f.DB.Exec("CREATE TABLE `" + f.Prefix + table + "` (" + columns + ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='bluegrass-client-api test fixture'"); err != nil {
			t.Fatalf("create fixture %s: %v", table, err)
		}
	}
}

func (f *Fixture) Exec(t *testing.T, query string, args ...any) {
	t.Helper()
	query = strings.ReplaceAll(query, "{p}", f.Prefix)
	if _, err := f.DB.Exec(query, args...); err != nil {
		t.Fatalf("fixture operation failed: %v", err)
	}
}
