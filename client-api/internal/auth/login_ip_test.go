package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bluegrass/client-api/internal/httpapi"
	"bluegrass/client-api/internal/secure"
	"bluegrass/client-api/internal/session"
)

// Observe SQL bindings through database/sql without opening any network socket.
// This is not a MySQL engine; engine/locking tests remain in the testdb suite.
type loginDB struct {
	row       map[string]driver.Value
	commits   int
	rollbacks int
	failWrite bool
}

type loginConnector struct{ db *loginDB }
type loginDriver struct{}
type loginConn struct{ db *loginDB }
type loginTx struct{ db *loginDB }
type loginRows struct {
	columns []string
	values  []driver.Value
	done    bool
}

func (c loginConnector) Connect(context.Context) (driver.Conn, error) { return &loginConn{c.db}, nil }
func (loginConnector) Driver() driver.Driver                          { return loginDriver{} }
func (loginDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use the in-memory connector")
}
func (*loginConn) Close() error { return nil }
func (*loginConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *loginConn) Begin() (driver.Tx, error) { return &loginTx{c.db}, nil }
func (tx *loginTx) Commit() error              { tx.db.commits++; return nil }
func (tx *loginTx) Rollback() error            { tx.db.rollbacks++; return nil }
func (c *loginConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (r *loginRows) Columns() []string { return r.columns }
func (*loginRows) Close() error        { return nil }
func (r *loginRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.values)
	return nil
}
func (c *loginConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO `bg_test_sessions`") {
		if c.db.failWrite {
			return nil, errors.New("fixture write failure")
		}
		// Decode column/placeholder bindings so this test does not assume where
		// login_ip is placed in the INSERT or how its arguments are ordered.
		parts := strings.SplitN(query, " VALUES (", 2)
		columns := strings.Split(strings.TrimSuffix(strings.SplitN(parts[0], " (", 2)[1], ")"), ",")
		values := strings.Split(strings.TrimSuffix(parts[1], ")"), ",")
		c.db.row = make(map[string]driver.Value)
		arg := 0
		for i, col := range columns {
			if values[i] == "?" {
				c.db.row[col] = args[arg].Value
				arg++
			} else if values[i] == "0" {
				c.db.row[col] = int64(0)
			} else {
				return nil, errors.New("unexpected INSERT literal")
			}
		}
	} else if !strings.HasPrefix(query, "INSERT IGNORE INTO `bg_test_device_sessions`") &&
		!strings.HasPrefix(query, "INSERT INTO `bg_test_refresh_tokens`") &&
		!strings.HasPrefix(query, "UPDATE `bg_test_device_sessions`") {
		return nil, errors.New("unexpected statement")
	}
	return driver.RowsAffected(1), nil
}
func (c *loginConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(query, "SELECT session_id FROM `bg_test_device_sessions`") {
		return &loginRows{columns: []string{"session_id"}, values: []driver.Value{nil}}, nil
	}
	if strings.Contains(query, " FROM `bg_test_sessions`") && c.db.row != nil {
		columns := strings.Split(strings.TrimPrefix(strings.SplitN(query, " FROM ", 2)[0], "SELECT "), ",")
		values := make([]driver.Value, len(columns))
		for i, col := range columns {
			values[i] = c.db.row[strings.TrimPrefix(col, "s.")]
		}
		return &loginRows{columns: columns, values: values}, nil
	}
	return nil, errors.New("unexpected query")
}

func TestHTTPLoginPersistsObservedIPWithSession(t *testing.T) {
	for _, tc := range []struct {
		name, peer string
		real       []string
		want       driver.Value
	}{
		{"direct-v4", "198.51.100.10:4567", nil, "198.51.100.10"},
		{"direct-v6", "[2001:0db8:0:0::1]:4567", nil, "2001:db8::1"},
		{"mapped-v4", "[::ffff:198.51.100.10]:4567", nil, "198.51.100.10"},
		{"untrusted-spoof", "198.51.100.10:4567", []string{"192.0.2.99"}, "198.51.100.10"},
		{"trusted-v4", "127.0.0.1:4567", []string{"192.0.2.11"}, "192.0.2.11"},
		{"trusted-v6", "[::1]:4567", []string{"2001:0db8::2"}, "2001:db8::2"},
		{"trusted-mapped-v4", "127.0.0.1:4567", []string{"::ffff:192.0.2.11"}, "192.0.2.11"},
		{"invalid-header-fallback", "127.0.0.1:4567", []string{"not-an-ip"}, "127.0.0.1"},
		{"multiple-header-fallback", "127.0.0.1:4567", []string{"192.0.2.11", "192.0.2.12"}, "127.0.0.1"},
		{"chain-fallback", "127.0.0.1:4567", []string{"192.0.2.11, 192.0.2.12"}, "127.0.0.1"},
		{"zone-fallback", "127.0.0.1:4567", []string{"fe80::1%eth0"}, "127.0.0.1"},
		{"xff-not-trusted", "127.0.0.1:4567", nil, "127.0.0.1"},
		{"unknown-peer", "invalid-peer", []string{"192.0.2.99"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &loginDB{}
			db := sql.OpenDB(loginConnector{script})
			defer db.Close()
			cfg := config()
			store, err := session.New(db, session.Config{Prefix: "bg_test_", Key: cfg.Key, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			src := sourceFixture()
			svc, err := New(src, store, cfg)
			if err != nil {
				t.Fatal(err)
			}
			h, err := httpapi.New(svc, src, httpapi.Config{TrustedProxyCIDRs: []string{"127.0.0.1/32", "::1/128"}})
			if err != nil {
				t.Fatal(err)
			}
			body := `{"username":"alice","password":"fixture-password","device_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`
			request := func(body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/api/client/v1/auth/login", strings.NewReader(body))
				r.RemoteAddr = tc.peer
				r.Header["X-Real-Ip"] = tc.real
				r.Header.Set("X-Forwarded-For", "192.0.2.250")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			w := request(body)
			if w.Code != 200 {
				t.Fatalf("login status = %d", w.Code)
			}
			got, exists := script.row["login_ip"]
			if !exists || got != tc.want {
				t.Fatalf("session INSERT login_ip = %v (present=%v), want %v", got, exists, tc.want)
			}
			if script.commits != 1 || script.rollbacks != 0 {
				t.Fatal("login IP and session must be committed together")
			}
			if strings.Contains(w.Body.String(), "login_ip") || strings.Contains(w.Body.String(), "client_ip") {
				t.Fatal("internal IP metadata leaked into the token response")
			}
			// The reader must accept both new IP values and pre-upgrade NULLs.
			for _, value := range []driver.Value{tc.want, nil} {
				script.row["login_ip"] = value
				stored, err := store.Access(context.Background(), secure.Digest("unused"))
				if err != nil {
					t.Fatalf("read stored session: %v", err)
				}
				want, _ := value.(string)
				if stored.LoginIP != want {
					t.Fatalf("read login IP = %q, want %q", stored.LoginIP, want)
				}
				owner, err := store.RefreshOwner(context.Background(), secure.Digest("unused"))
				if err != nil || owner.LoginIP != want {
					t.Fatalf("refresh owner did not preserve login IP: %v", err)
				}
			}
			for _, field := range []string{"client_ip", "login_ip"} {
				forged := strings.TrimSuffix(body, "}") + `,"` + field + `":"192.0.2.99"}`
				if w := request(forged); w.Code != 422 {
					t.Fatalf("client-supplied %s accepted: %d", field, w.Code)
				}
			}
			if w := request(strings.Replace(body, "fixture-password", "wrong-password", 1)); w.Code != 401 {
				t.Fatalf("bad password status = %d", w.Code)
			}
			if script.commits != 1 {
				t.Fatal("invalid login persisted a session/IP")
			}
			script.failWrite = true
			if w := request(body); w.Code != 503 || script.rollbacks != 1 || script.commits != 1 {
				t.Fatal("failed session/IP write must roll back and reject login")
			}
		})
	}
}
