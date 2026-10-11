package integration

import (
	"bluegrass/client-api/internal/model"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKeysetReadsWith20000Subscriptions(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	f.Exec(t, "DELETE FROM `{p}forward_rules`")
	f.Exec(t, "DELETE FROM `{p}subscriptions`")
	// Reproduce production indexes, not extra indexes that make only tests fast.
	// There are 20,000 source subscriptions, of which customer 1 owns 200.
	for start := 1; start <= 20000; start += 500 {
		values := make([]string, 0, 500)
		args := make([]any, 0, 1000)
		rules := make([]string, 0, 500)
		ruleArgs := make([]any, 0, 1000)
		for id := start; id < start+500; id++ {
			owner := 2
			if id%100 == 0 {
				owner = 1
			}
			values = append(values, "(?,?,1,'active',1,'2099-01-01 00:00:00','2026-01-01 00:00:00')")
			args = append(args, id, owner)
			rules = append(rules, "(?,?,1,1,1,'active',20001)")
			ruleArgs = append(ruleArgs, id, id)
		}
		f.Exec(t, "INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status,has_forward,expires_at,started_at) VALUES "+strings.Join(values, ","), args...)
		f.Exec(t, "INSERT INTO `{p}forward_rules` (id,subscription_id,proxy_ip_id,ny_device_group_id,forward_plan_id,status,listen_port) VALUES "+strings.Join(rules, ","), ruleArgs...)
	}
	// Bulk-created fixture tables start with stale cardinality (the whole dataset
	// was just inserted). Refresh statistics only on these isolated test tables.
	f.Exec(t, "ANALYZE TABLE `{p}subscriptions`, `{p}forward_rules`")
	plan, explainErr := r.ExplainNodes(context.Background(), 1, 0, 100)
	if explainErr != nil {
		t.Fatal(explainErr)
	}
	var decoded any
	if e := json.Unmarshal([]byte(plan), &decoded); e != nil {
		t.Fatal(e)
	}
	t.Logf("full node JOIN plan: %s", summarizePlan(decoded))
	begin := time.Now()
	first, e := r.Nodes(context.Background(), 1, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(first.Items) != 100 || !first.Pagination.HasMore {
		t.Fatalf("first keyset page: items=%d more=%v", len(first.Items), first.Pagination.HasMore)
	}
	raw, e := base64.RawURLEncoding.DecodeString(first.Pagination.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	cursor, e := strconv.ParseInt(string(raw), 10, 64)
	if e != nil || cursor != 10000 {
		t.Fatal("invalid scan cursor", e)
	}
	second, e := r.Nodes(context.Background(), 1, cursor, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(second.Items) != 100 || second.Pagination.HasMore {
		t.Fatal("second keyset page is incomplete")
	}
	seen := make(map[string]bool)
	for _, page := range [][]string{ids(first.Items), ids(second.Items)} {
		for _, id := range page {
			n, e := strconv.Atoi(id)
			if e != nil || n%100 != 0 || seen[id] {
				t.Fatal("foreign/duplicate subscription returned", id)
			}
			seen[id] = true
		}
	}
	t.Logf("20000 source subscriptions, 200 owned: two bounded pages returned 200 unique authorized nodes in %s", time.Since(begin).Round(time.Millisecond))
	var key string
	rows, e := f.Read.Query("EXPLAIN SELECT id FROM `"+f.Prefix+"subscriptions` WHERE customer_id=? AND id>? ORDER BY id LIMIT 101", 1, 10000)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	values := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if e = rows.Scan(dest...); e != nil {
			t.Fatal(e)
		}
		for i, col := range cols {
			if col == "key" {
				switch v := values[i].(type) {
				case []byte:
					key = string(v)
				case string:
					key = v
				}
			}
		}
	}
	if key == "" {
		t.Fatal("keyset ownership lookup does not use an index")
	}
	t.Log(fmt.Sprintf("keyset base lookup uses index %s", key))
}

func ids(nodes []model.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func summarizePlan(value any) string {
	parts := []string{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if name, ok := x["table_name"]; ok {
				parts = append(parts, fmt.Sprintf("%v access=%v key=%v rows=%v", name, x["access_type"], x["key"], x["rows_examined_per_scan"]))
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(value)
	return strings.Join(parts, "; ")
}
