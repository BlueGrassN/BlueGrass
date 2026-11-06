package source

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// This driver scripts the database boundary, not repository behavior. It never
// opens a socket. In particular, it observes the context passed to database/sql,
// rejects writes/transactions/locks, and detects unclosed and unexpected queries.
// SQL predicates are checked here; these tests do not execute a MySQL engine.
type queryStep struct {
	kind    string
	args    []driver.Value
	rows    *fixtureRows
	err     error
	check   func(string, []driver.NamedValue) error
	block   <-chan struct{}
	entered chan<- struct{}
}

type scriptedDB struct {
	mu       sync.Mutex
	steps    []queryStep
	opened   []*fixtureRows
	problems []error
	timeout  time.Duration
}

type fixtureRows struct {
	columns []string
	values  [][]driver.Value
	index   int
	closed  bool
	readErr error
}

func (r *fixtureRows) Columns() []string { return r.columns }
func (r *fixtureRows) Close() error      { r.closed = true; return nil }
func (r *fixtureRows) Next(dest []driver.Value) error {
	if r.index < len(r.values) {
		copy(dest, r.values[r.index])
		r.index++
		return nil
	}
	if r.readErr != nil {
		return r.readErr
	}
	return io.EOF
}

type fixtureConnector struct{ script *scriptedDB }
type fixtureDriver struct{}
type fixtureConn struct{ script *scriptedDB }

func (c fixtureConnector) Connect(context.Context) (driver.Conn, error) {
	return &fixtureConn{script: c.script}, nil
}
func (fixtureConnector) Driver() driver.Driver { return fixtureDriver{} }
func (fixtureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use fixture connector")
}
func (c *fixtureConn) Close() error { return nil }
func (c *fixtureConn) reject(message string) error {
	err := errors.New(message)
	c.script.mu.Lock()
	c.script.problems = append(c.script.problems, err)
	c.script.mu.Unlock()
	return err
}
func (c *fixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, c.reject("source must use context-aware ordinary SELECT")
}
func (c *fixtureConn) Begin() (driver.Tx, error) {
	return nil, c.reject("source must not begin transactions")
}
func (c *fixtureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, c.reject("source must not execute writes or session changes")
}

func normalizeSQL(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(q, "`", ""))), " ")
}

func requireSQL(q string, expressions ...string) error {
	for _, expression := range expressions {
		if !regexp.MustCompile(expression).MatchString(q) {
			return fmt.Errorf("SQL missing contract predicate %q: %s", expression, q)
		}
	}
	return nil
}

func checkQuery(kind, q string) error {
	if kind == "explain_nodes" {
		const prefix = "explain format=json "
		if !strings.HasPrefix(q, prefix) {
			return errors.New("node diagnostics must use EXPLAIN FORMAT=JSON")
		}
		return checkQuery("nodes", strings.TrimPrefix(q, prefix))
	}
	if !strings.HasPrefix(q, "select ") || strings.ContainsAny(q, ";") ||
		regexp.MustCompile(`\b(for update|for share|lock|insert|update|delete|replace|into|set|count|offset|sleep|benchmark)\b|/\*|--`).MatchString(q) {
		return fmt.Errorf("not a bounded ordinary source SELECT: %s", q)
	}
	switch kind {
	case "resources", "resource_detail", "assets", "asset_detail":
		return checkResourceQuery(kind, q)
	case "username":
		if strings.Contains(q, " or ") {
			return errors.New("username lookup must not scan phone via OR")
		}
		return requireSQL(q, `\bfrom [a-z0-9_]*customers\b`, `\busername\s*=\s*\?`, `\bdeleted_at is null\b`, `\blimit 1\b`)
	case "customer":
		return requireSQL(q, `\bfrom [a-z0-9_]*customers\b`, `\bid\s*=\s*\?`, `\bdeleted_at is null\b`, `\blimit 1\b`)
	case "phones":
		return requireSQL(q, `^select (?:c\.)?id\s*,\s*(?:c\.)?phone from [a-z0-9_]*customers\b`, `\bid\s*>\s*\?`, `\bdeleted_at is null\b`, `\border by (?:c\.)?id(?: asc)? limit \?$`)
	case "owner":
		return requireSQL(q, `^select (?:s\.)?customer_id from [a-z0-9_]*subscriptions\b`, `\bid\s*=\s*\?`, `\blimit 1\b`)
	case "nodes", "detail":
		// These predicates must share the same SELECT as the emitted DTO. A
		// separate CustomerByID call cannot protect against a concurrent disable.
		if strings.Contains(q, "upstream_expires_at") {
			return errors.New("upstream expiry is not customer expiry")
		}
		if strings.Count(q, "select ") != 1 {
			return errors.New("node authorization and latest rule selection must use one SELECT")
		}
		if err := requireSQL(q,
			`\bfrom [a-z0-9_]*subscriptions (?:as )?s\b`,
			`\bjoin [a-z0-9_]*customers (?:as )?c\b`,
			`\bc\.id\s*=\s*s\.customer_id`, `\bc\.status\s*=\s*1\b`, `\bc\.deleted_at is null\b`,
			`\bs\.customer_id\s*=\s*\?`, `\bs\.status\s*=\s*'active'`, `\bs\.has_forward\s*=\s*1\b`,
			`\(s\.expires_at is null or s\.expires_at\s*>\s*\?\)`,
			`\bjoin [a-z0-9_]*proxy_ips (?:as )?ip\b`, `\bip\.id\s*=\s*s\.proxy_ip_id`, `\bip\.deleted_at is null\b`,
			`\bip\.status\s*(?:<>|!=)\s*'disabled'`,
			`\(ip\.released_at is null or coalesce\(s\.last_renewed_at,\s*s\.started_at\)\s*>\s*ip\.released_at\)`,
			`\bjoin [a-z0-9_]*forward_rules (?:as )?fr\b`, `\bfr\.subscription_id\s*=\s*s\.id`,
			`\bfr\.proxy_ip_id\s*=\s*s\.proxy_ip_id`, `\bfr\.status\s*=\s*'active'`,
			// An active rule only loses to a larger ID for this same
			// subscription and proxy IP. Selection precedes the page LIMIT.
			`\bleft join [a-z0-9_]*forward_rules newer_fr on newer_fr\.subscription_id\s*=\s*s\.id and newer_fr\.proxy_ip_id\s*=\s*s\.proxy_ip_id and newer_fr\.status\s*=\s*'active' and newer_fr\.id\s*>\s*fr\.id\b`,
			`\bwhere newer_fr\.id is null and s\.customer_id\s*=\s*\?`,
			`\bjoin [a-z0-9_]*ny_device_groups (?:as )?dg\b`, `\bdg\.id\s*=\s*fr\.ny_device_group_id`, `\bdg\.is_enabled\s*=\s*1\b`,
			`\bleft join [a-z0-9_]*forward_plans (?:as )?fp\b`, `\bfp\.id\s*=\s*fr\.forward_plan_id`,
		); err != nil {
			return err
		}
		if kind == "nodes" {
			return requireSQL(q, `\bs\.id\s*>\s*\?`, `\border by s\.id(?: asc)? limit \?$`)
		}
		return requireSQL(q, `\bs\.id\s*=\s*\?`, `\blimit 1\b`)
	default:
		return fmt.Errorf("unknown query kind %q", kind)
	}
}

func (c *fixtureConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > c.script.timeout+25*time.Millisecond {
		return nil, c.reject("each source SELECT needs a bounded query deadline")
	}
	c.script.mu.Lock()
	if len(c.script.steps) == 0 {
		c.script.mu.Unlock()
		return nil, c.reject("unexpected query: " + normalizeSQL(query))
	}
	step := c.script.steps[0]
	c.script.steps = c.script.steps[1:]
	c.script.mu.Unlock()
	q := normalizeSQL(query)
	if err := checkQuery(step.kind, q); err != nil {
		return nil, c.reject(err.Error())
	}
	if step.args != nil {
		got := make([]driver.Value, len(args))
		for i := range args {
			got[i] = args[i].Value
		}
		if !reflect.DeepEqual(got, step.args) {
			return nil, c.reject(fmt.Sprintf("query arguments: got %v, want %v", got, step.args))
		}
	}
	if step.check != nil {
		if err := step.check(q, args); err != nil {
			return nil, c.reject(err.Error())
		}
	}
	if step.entered != nil {
		step.entered <- struct{}{}
	}
	if step.block != nil {
		select {
		case <-step.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if step.err != nil {
		return nil, step.err
	}
	if step.rows == nil {
		return nil, c.reject("fixture requires rows or error")
	}
	c.script.mu.Lock()
	c.script.opened = append(c.script.opened, step.rows)
	c.script.mu.Unlock()
	return step.rows, nil
}

func openFixture(t *testing.T, timeout time.Duration) (*sql.DB, *scriptedDB) {
	t.Helper()
	script := &scriptedDB{timeout: timeout}
	db := sql.OpenDB(fixtureConnector{script: script})
	t.Cleanup(func() {
		db.Close()
		script.mu.Lock()
		defer script.mu.Unlock()
		for _, err := range script.problems {
			t.Error(err)
		}
		if len(script.steps) != 0 {
			t.Errorf("%d expected source queries not performed", len(script.steps))
		}
		for _, rows := range script.opened {
			if !rows.closed {
				t.Error("source rows were not closed")
			}
		}
	})
	return db, script
}

func (s *scriptedDB) expect(steps ...queryStep) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, steps...)
}

var fixtureNow = time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC)
var fixtureLocation = time.FixedZone("source+08", 8*60*60)

func repositoryFixture(t *testing.T, change func(*Config)) (*Repository, *scriptedDB) {
	t.Helper()
	cfg := Config{QueryTimeout: 200 * time.Millisecond, SourceLocation: fixtureLocation,
		Now: func() time.Time { return fixtureNow }, PhoneRefreshInterval: time.Hour, MaxPhoneEntries: 10000}
	if change != nil {
		change(&cfg)
	}
	db, script := openFixture(t, cfg.QueryTimeout)
	repo, err := New(db, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if repo == nil {
		t.Fatal("New returned nil repository")
	}
	return repo, script
}

func customerRows(values ...[]driver.Value) *fixtureRows {
	return &fixtureRows{columns: []string{"id", "username", "display_name", "phone", "password", "status"}, values: values}
}

func phoneRows(values ...[]driver.Value) *fixtureRows {
	return &fixtureRows{columns: []string{"id", "phone"}, values: values}
}

func customerRow(id int64, phone, hash string, status int64) []driver.Value {
	return []driver.Value{id, "fixture-user", "Fixture User", phone, hash, status}
}

func phoneBatch(after int64, rows *fixtureRows) queryStep {
	return queryStep{kind: "phones", rows: rows, check: func(_ string, args []driver.NamedValue) error {
		if len(args) != 2 || args[0].Value != after {
			return fmt.Errorf("phone keyset must start after %d", after)
		}
		n, ok := args[1].Value.(int64)
		if !ok || n < 1 || n > 2000 {
			return errors.New("phone batch must have limit in [1,2000]")
		}
		return nil
	}}
}

func expectLoginMiss(script *scriptedDB, login string) {
	script.expect(queryStep{kind: "username", args: []driver.Value{login}, rows: customerRows()})
}
