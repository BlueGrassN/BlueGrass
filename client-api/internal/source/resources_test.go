package source

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

func resourceRows(values ...[]driver.Value) *fixtureRows {
	return &fixtureRows{columns: []string{"id", "remark", "name", "expires_at", "status"}, values: values}
}

func assertCustomerRemarkJSON(t *testing.T, item any, want string) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	value, exists := fields["customer_remark"]
	if !exists || value != want {
		t.Fatalf("customer_remark = %v (present=%t), want %q", value, exists, want)
	}
}

func resourceReader(t *testing.T, repo *Repository) model.ResourceSource {
	t.Helper()
	r, ok := any(repo).(model.ResourceSource)
	if !ok {
		t.Fatal("repository does not implement read-only resource queries")
	}
	return r
}

func TestResourcesBoundedPaginationExpiryAndNoCredentials(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	r := resourceReader(t, repo)
	script.expect(queryStep{kind: "resources", args: []driver.Value{int64(7), int64(0), int64(3)}, rows: resourceRows(
		[]driver.Value{int64(11), "Expired", "Asset", "2026-10-10 12:00:00", "active"},
		[]driver.Value{int64(12), "Pending customer remark", nil, nil, "provisioning"},
		[]driver.Value{int64(13), nil, "Next", nil, "suspended"},
	)})
	page, err := r.Resources(context.Background(), 7, "subscription", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !page.Pagination.HasMore || page.Pagination.NextCursor != "c3Vic2NyaXB0aW9uOjEy" {
		t.Fatalf("wrong page: %+v", page)
	}
	if page.Items[0].Status != "expired" || page.Items[0].ExpiresAt.Format(time.RFC3339) != "2026-10-10T04:00:00Z" {
		t.Fatal("customer expiry not preserved")
	}
	if page.Items[1].ExpiresAt != nil || page.Items[1].Kind != "subscription" || !page.Items[1].Owned || page.Items[1].Connectable {
		t.Fatal("unknown expiry/connection invented")
	}
	if page.Items[0].Name != "Asset" || page.Items[1].Name != "Resource #12" {
		t.Fatal("resource names must use the asset or a neutral fallback for pending delivery")
	}
	assertCustomerRemarkJSON(t, page.Items[0], "Expired")
	assertCustomerRemarkJSON(t, page.Items[1], "Pending customer remark")
	if page.Coverage.SnapshotComplete || page.Coverage.Consistency != "per_page" || page.Coverage.Ownership != "conservative" {
		t.Fatal("must not claim an authoritative account snapshot")
	}
}

func TestResourceNameUsesAssetNameAndNeutralFallback(t *testing.T) {
	for _, tc := range []struct {
		name             string
		remark, asset    driver.Value
		want, wantRemark string
	}{
		{"conflicting remark", "Customer label", "美国-198.87.96.137", "美国-198.87.96.137", "Customer label"},
		{"null remark", nil, "Asset label", "Asset label", ""},
		{"trimmed asset and remark", " 我的备注 \t", " Asset label \t", "Asset label", "我的备注"},
		{"blank remark", " \t", "Asset label", "Asset label", ""},
		{"existing asset suffix", "Customer label", "Asset #88", "Asset #88", "Customer label"},
		{"null asset ignores remark", "Customer label", nil, "Resource #11", "Customer label"},
		{"empty asset ignores remark", "Customer label", "", "Resource #11", "Customer label"},
		{"blank asset ignores remark", "Customer label", " \t", "Resource #11", "Customer label"},
		{"all null", nil, nil, "Resource #11", ""},
	} {
		for _, kind := range []string{"subscription", "asset"} {
			for _, mode := range []string{"list", "detail"} {
				t.Run(tc.name+"/"+kind+"/"+mode, func(t *testing.T) {
					repo, script := repositoryFixture(t, nil)
					status, queryKind := "active", "resources"
					if kind == "asset" {
						status, queryKind = "assigned", "assets"
					}
					rows := resourceRows([]driver.Value{int64(11), tc.remark, tc.asset, nil, status})
					var got model.Resource
					var err error
					if mode == "list" {
						script.expect(queryStep{kind: queryKind, args: []driver.Value{int64(7), int64(0), int64(2)}, rows: rows})
						var page model.ResourcePage
						page, err = repo.Resources(context.Background(), 7, kind, 0, 1)
						if err != nil || len(page.Items) != 1 {
							t.Fatalf("resource list: %+v, %v", page, err)
						}
						got = page.Items[0]
					} else {
						queryKind = "resource_detail"
						if kind == "asset" {
							queryKind = "asset_detail"
						}
						script.expect(queryStep{kind: queryKind, args: []driver.Value{int64(7), int64(11)}, rows: rows})
						got, err = repo.Resource(context.Background(), 7, kind, 11)
					}
					if err != nil || got.ID != "11" || got.Kind != kind || got.Name != tc.want {
						t.Fatalf("resource name: %q, %v; want %q", got.Name, err, tc.want)
					}
					wantRemark := tc.wantRemark
					if kind == "asset" {
						wantRemark = "" // No subscription: never borrow a remark from another entitlement.
					}
					assertCustomerRemarkJSON(t, got, wantRemark)
				})
			}
		}
	}
}

func TestResourceQuerySelectsOnlySubscriptionCustomerRemark(t *testing.T) {
	repo, _ := repositoryFixture(t, nil)
	for _, kind := range []string{"subscription", "asset"} {
		for _, detail := range []bool{false, true} {
			query := normalizeSQL(repo.resourceQuery(kind, detail))
			if kind == "subscription" && !strings.HasPrefix(query, "select s.id, s.customer_remark,") {
				t.Errorf("detail=%t: subscription query must select the current owner's customer remark", detail)
			}
			if kind == "asset" && strings.Contains(query, "customer_remark") {
				t.Errorf("detail=%t: bare assets must not read subscription remarks", detail)
			}
			if strings.Contains(query, "ip.remark") || strings.Contains(query, "s.remark") {
				t.Errorf("kind=%s detail=%t: internal remarks must not be selected", kind, detail)
			}
		}
	}
}

func TestResourceKindsAndMissingOwnership(t *testing.T) {
	for _, kind := range []string{"subscription", "asset"} {
		t.Run(kind, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			r := resourceReader(t, repo)
			queryKind := "resource_detail"
			if kind == "asset" {
				queryKind = "asset_detail"
			}
			script.expect(queryStep{kind: queryKind, args: []driver.Value{int64(7), int64(11)}, rows: resourceRows()})
			_, err := r.Resource(context.Background(), 7, kind, 11)
			if !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("foreign/released/missing must be indistinguishable: %v", err)
			}
		})
	}
}

func TestAssetSummariesNeverUseUpstreamExpiry(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	r := resourceReader(t, repo)
	script.expect(queryStep{kind: "assets", args: []driver.Value{int64(7), int64(0), int64(11)}, rows: resourceRows([]driver.Value{int64(14), nil, "Static", nil, "assigned"})})
	page, err := r.Resources(context.Background(), 7, "asset", 0, 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("asset read: %v", err)
	}
	if page.Items[0].ExpiresAt != nil || page.Items[0].Kind != "asset" {
		t.Fatal("unlinked assets have no known customer expiry")
	}
}

func TestOverlappingEntitlementsKeepSeparateIdentityAndExpiry(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(queryStep{kind: "resources", args: []driver.Value{int64(7), int64(0), int64(11)}, rows: resourceRows(
		[]driver.Value{int64(11), nil, "Same asset", "2026-10-10 12:00:00", "expired"},
		[]driver.Value{int64(12), nil, "Same asset", "2027-10-10 12:00:00", "active"},
	)})
	page, err := repo.Resources(context.Background(), 7, "subscription", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "11" || page.Items[1].ID != "12" ||
		page.Items[0].ExpiresAt.Equal(*page.Items[1].ExpiresAt) || page.Coverage.SnapshotComplete {
		t.Fatal("distinct subscription entitlements were collapsed or coverage overstated")
	}
}

func TestResourceValidationDoesNotQuery(t *testing.T) {
	repo, _ := repositoryFixture(t, nil)
	r := resourceReader(t, repo)
	for _, item := range []struct {
		kind  string
		after int64
		limit int
	}{{"other", 0, 2}, {"asset", -1, 2}, {"asset", 0, 101}} {
		if _, err := r.Resources(context.Background(), 7, item.kind, item.after, item.limit); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
}

func TestResourceErrorsNeverReturnSuccessfulPartialCoverage(t *testing.T) {
	for _, stage := range []string{"query", "read", "scan"} {
		t.Run(stage, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			step := queryStep{kind: "resources", rows: resourceRows()}
			switch stage {
			case "query":
				step.err = errors.New("fixture database unavailable")
			case "read":
				step.rows.readErr = errors.New("fixture interrupted stream")
			case "scan":
				step.rows = resourceRows([]driver.Value{int64(11), nil, "Example", "invalid-timestamp", "active"})
			}
			script.expect(step)
			page, err := repo.Resources(context.Background(), 7, "subscription", 0, 10)
			if err == nil || len(page.Items) != 0 {
				t.Fatal("failed source read became a successful page")
			}
		})
	}
}

func TestResourceOwnershipUsesCurrentDeliveryRatherThanNewestID(t *testing.T) {
	repo, _ := repositoryFixture(t, nil)
	for _, kind := range []string{"subscription", "asset"} {
		for _, detail := range []bool{false, true} {
			q := normalizeSQL(repo.resourceQuery(kind, detail))
			if strings.Contains(q, "newer.id > s.id") {
				t.Error("an unrelated or overlapping newer subscription must not suppress current ownership")
			}
			for _, evidence := range []string{"json_extract(s.product_snapshot, '$.upstream_asset')", "json_object(", "spark_instances", "ipipv_instances"} {
				if !strings.Contains(q, evidence) {
					t.Errorf("%s query lacks delivery evidence %s", kind, evidence)
				}
			}
			if kind == "subscription" {
				if !strings.Contains(q, "ip.assigned_customer_id is null") || !strings.Contains(q, "conflict.customer_id <> s.customer_id") {
					t.Error("null assignment must permit unconflicted entitlement, not reject it or grant conflicting customers")
				}
				if !strings.Contains(q, "case when s.status in ('provisioning', 'provision_review') then null else ip.asset_name end") {
					t.Error("pending subscriptions must not expose an unverified bound asset name")
				}
			} else if !strings.Contains(q, "s.customer_id = ip.assigned_customer_id") {
				t.Error("foreign/historical subscriptions must not suppress a directly assigned asset")
			}
			if kind == "asset" && strings.Contains(strings.SplitN(q, "and not exists", 2)[0], "released_at") {
				t.Error("a historical release marker must not override the current bare-asset assignment")
			}
			if strings.Contains(q, "shared_user_count") || strings.Contains(q, "max_shared_users") {
				t.Error("shared counts do not establish customer membership")
			}
		}
	}
}

// This is a SQL-boundary assertion, not MySQL execution. Integration tests
// exercise the same authorization predicates when an isolated DB is available.
func checkResourceQuery(kind, q string) error {
	if strings.Contains(q, "upstream_expires_at") || strings.Contains(q, "auth_password") || strings.Contains(q, "auth_username") || strings.Contains(q, "socks5_info") {
		return errors.New("resource summary read private connection data or upstream expiry")
	}
	if err := requireSQL(q, `\bc\.status\s*=\s*1\b`, `\bc\.deleted_at is null\b`); err != nil {
		return err
	}
	if err := requireSQL(q,
		`ip\.assigned_customer_id = s\.customer_id or \(ip\.assigned_customer_id is null and not exists`,
		`conflict\.customer_id <> s\.customer_id and conflict\.proxy_ip_id = ip\.id`,
		`json_extract\(s\.product_snapshot, '\$\.upstream_asset'\) = json_object\(`,
		`si\.proxy_ip_id = ip\.id and binary si\.instance_id = binary ip\.spark_instance_id`,
		`si_other\.proxy_ip_id = ip\.id and si_other\.id <> si\.id`,
		`ii\.proxy_ip_id = ip\.id and binary ii\.instance_no = binary ip\.ipipv_instance_id`,
		`ii_other\.proxy_ip_id = ip\.id and ii_other\.id <> ii\.id`); err != nil {
		return err
	}
	if kind == "resources" || kind == "resource_detail" {
		if err := requireSQL(q, `\bs\.customer_id\s*=\s*\?`, `\bc\.id\s*=\s*s\.customer_id`,
			`\bip\.deleted_at is null`, `\bip\.assigned_customer_id\s*=\s*s\.customer_id`,
			`\bip\.released_at is null or coalesce\(s\.last_renewed_at,\s*s\.started_at\)\s*>\s*ip\.released_at`,
			`\bs\.status in \('active', 'expired', 'suspended', 'provisioning', 'provision_review'\)`); err != nil {
			return err
		}
		if strings.Contains(q, "s.has_forward") || strings.Contains(q, "s.expires_at >") {
			return errors.New("all-owned query excluded non-connectable/expired resources")
		}
		if kind == "resources" {
			return requireSQL(q, `s\.id > \?`, `order by s\.id asc limit \?$`)
		}
		return requireSQL(q, `s\.id = \?`, `limit 1$`)
	}
	if err := requireSQL(q, `ip\.assigned_customer_id = \?`, `c\.id = ip\.assigned_customer_id`, `ip\.deleted_at is null`, `not exists \(select 1 from [a-z0-9_]*subscriptions s where s\.proxy_ip_id = ip\.id and s\.customer_id = ip\.assigned_customer_id`); err != nil {
		return err
	}
	if kind == "assets" {
		return requireSQL(q, `ip\.id > \?`, `order by ip\.id asc limit \?$`)
	}
	return requireSQL(q, `ip\.id = \?`, `limit 1$`)
}
