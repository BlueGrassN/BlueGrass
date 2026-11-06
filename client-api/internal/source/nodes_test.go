package source

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

// Projection order for the raw JOIN fixture. All availability decisions belong
// to the single SQL statement, while endpoints and nullable credentials are
// mapped by the repository. No real customer data or credentials appear here.
type nodeRecord struct {
	id                                                        int64
	remark, assetName, countryCode, countryName, city         driver.Value
	expires, customHost, planHost, groupCustom, groupOriginal driver.Value
	port, username, password, socksInfo, protocol             driver.Value
}

func validNode(id int64) nodeRecord {
	return nodeRecord{id: id, remark: "Fixture Relay", assetName: "Fixture Asset", countryCode: "JP", countryName: "Japan", city: "Tokyo",
		expires: "2026-10-12 12:30:00", customHost: "custom.example", planHost: "plan.example", groupCustom: "group.example", groupOriginal: "origin.example",
		port: int64(10808), username: "fixture-user", password: "fixture-password", socksInfo: "192.0.2.99:9999:legacy-user:legacy:password", protocol: "socks5"}
}

func nodeRows(records ...nodeRecord) *fixtureRows {
	r := &fixtureRows{columns: []string{"id", "customer_remark", "asset_name", "country_code", "country_name", "city", "expires_at", "custom_host", "display_host", "custom_connect_host", "original_connect_host", "listen_port", "auth_username", "auth_password", "socks5_info", "protocol"}}
	for _, n := range records {
		r.values = append(r.values, []driver.Value{n.id, n.remark, n.assetName, n.countryCode, n.countryName, n.city, n.expires, n.customHost, n.planHost, n.groupCustom, n.groupOriginal, n.port, n.username, n.password, n.socksInfo, n.protocol})
	}
	return r
}

func nodeStep(kind string, customerID, id int64, limit int, rows *fixtureRows) queryStep {
	return queryStep{kind: kind, rows: rows, check: func(q string, args []driver.NamedValue) error {
		wantCount := 3
		if kind == "nodes" {
			wantCount = 4
		}
		// Bind order is customer, subscription cursor/ID, local source Now,
		// and limit+1 for list queries. The SQL WHERE follows that order.
		if len(args) != wantCount || args[0].Value != customerID || args[1].Value != id {
			return fmt.Errorf("wrong ownership/cursor arguments: %v", args)
		}
		if err := assertSourceNow(args[2].Value); err != nil {
			return err
		}
		if kind == "nodes" && args[3].Value != int64(limit+1) {
			return fmt.Errorf("raw candidate budget must be limit+1, got %v", args[3].Value)
		}
		return requireSQL(q, `lower\(coalesce\(nullif\(ip\.protocol,\s*''\),\s*'socks5'\)\) in \('socks',\s*'socks5'\)`)
	}}
}

func TestNodesUsesOneCurrentCustomerJOINAndCorrectDTO(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(nodeStep("nodes", 7, 0, 10, nodeRows(validNode(11))))
	page, err := repo.Nodes(context.Background(), 7, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("want one node, got %+v", page)
	}
	n := page.Items[0]
	if n.ID != "11" || n.Name != "Fixture Asset" || n.Protocol != "socks5" || n.Host != "custom.example" || n.Port != 10808 ||
		n.Username != "fixture-user" || n.Password != "fixture-password" || n.CountryCode != "JP" || n.CountryName != "Japan" || n.City != "Tokyo" || !n.IsForwarded {
		t.Fatalf("incorrect node mapping: %+v", n)
	}
	if n.ExpiresAt == nil || n.ExpiresAt.Location() != time.UTC || n.ExpiresAt.Format(time.RFC3339) != "2026-10-12T04:30:00Z" {
		t.Fatalf("expiry must be source wall time converted to UTC: %v", n.ExpiresAt)
	}
	if page.Pagination.PerPage != 10 || page.Pagination.HasMore || page.Pagination.NextCursor != "" {
		t.Fatalf("incorrect last-page metadata: %+v", page.Pagination)
	}
}

func TestNodesRechecksDisabledOrDeletedCustomerInEmittedJOIN(t *testing.T) {
	for _, state := range []string{"disabled", "deleted"} {
		t.Run(state, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			// Customer was active during authentication; another writer changes
			// it before Nodes. Only the second SELECT is allowed to authorize.
			script.expect(queryStep{kind: "customer", args: []driver.Value{int64(7)}, rows: customerRows(customerRow(7, "15500000007", "hash", 1))})
			if _, err := repo.CustomerByID(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
			script.expect(nodeStep("nodes", 7, 0, 10, nodeRows()))
			page, err := repo.Nodes(context.Background(), 7, 0, 10)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("%s account received a node: %+v, %v", state, page, err)
			}
		})
	}
}

func TestNodesReleaseRenewalAndSupplierExpiryStayInSingleJOIN(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		available bool
	}{
		{"historical release before renewal", true},
		{"historical release before started_at without renewal", true},
		{"upstream expiry is stale but subscription is current", true},
		{"release equals latest activation", false},
		{"release after latest activation", false},
		{"release without any activation", false},
		{"explicit disabled IP", false},
		{"deleted IP", false},
		{"expired subscription", false},
		{"inactive subscription", false},
		{"forward belongs to different proxy IP", false},
		{"inactive forward", false},
		{"disabled device group", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			rows := nodeRows()
			if scenario.available {
				rows = nodeRows(validNode(11))
			}
			// The boundary guard requires the strict COALESCE > released_at
			// predicate, all eligibility joins, and no upstream expiry filter.
			script.expect(nodeStep("nodes", 7, 0, 10, rows))
			page, err := repo.Nodes(context.Background(), 7, 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if (len(page.Items) == 1) != scenario.available {
				t.Fatalf("availability: %+v", page)
			}
		})
	}
}

func TestRelayHostPriorityNeverFallsBackToRawIP(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		custom, plan, group, original driver.Value
		want                          string
	}{
		{"subscription", " custom.example ", "plan.example", "group.example", "origin.example", "custom.example"},
		{"plan", nil, "plan.example", "group.example", "origin.example", "plan.example"},
		{"group custom", "", "", "group.example", "origin.example", "group.example"},
		{"group original", nil, nil, nil, "origin.example", "origin.example"},
		{"no relay", nil, nil, nil, nil, ""},
		{"invalid custom cannot silently change relay", "https://bad.example", "plan.example", "group.example", "origin.example", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			n := validNode(11)
			n.customHost, n.planHost, n.groupCustom, n.groupOriginal = tc.custom, tc.plan, tc.group, tc.original
			script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
			page, err := repo.Nodes(context.Background(), 7, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(page.Items) != 0 {
					t.Fatalf("invalid relay leaked fallback: %+v", page.Items)
				}
			} else if len(page.Items) != 1 || page.Items[0].Host != tc.want || page.Items[0].Port != 10808 {
				t.Fatalf("relay priority: %+v", page.Items)
			}
		})
	}
}

func TestNodeCredentialsFallbackOnlyForSQLNullAndSplitAtMostFour(t *testing.T) {
	for _, tc := range []struct {
		name               string
		user, pass, info   driver.Value
		wantUser, wantPass string
	}{
		{"explicit", "current-user", "current-pass", "192.0.2.99:9999:old:old", "current-user", "current-pass"},
		{"null both", nil, nil, "192.0.2.99:9999:legacy-user:part:two:three", "legacy-user", "part:two:three"},
		{"null user only", nil, "current-pass", "192.0.2.99:9999:legacy-user:old", "legacy-user", "current-pass"},
		{"null password only", "current-user", nil, "192.0.2.99:9999:old:legacy:pass", "current-user", "legacy:pass"},
		{"explicit empty", "", "", "192.0.2.99:9999:legacy-user:legacy-pass", "", ""},
		{"missing legacy", nil, nil, nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			n := validNode(11)
			n.username, n.password, n.socksInfo = tc.user, tc.pass, tc.info
			script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
			page, err := repo.Nodes(context.Background(), 7, 0, 1)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("credential mapping: %+v, %v", page, err)
			}
			if page.Items[0].Username != tc.wantUser || page.Items[0].Password != tc.wantPass {
				t.Fatalf("wrong credentials: %+v", page.Items[0])
			}
		})
	}
}

func TestSocksAndLegacyNullOrEmptyProtocolNormalizeToSocks5(t *testing.T) {
	for _, protocol := range []driver.Value{"socks", "SOCKS5", nil, ""} {
		t.Run(fmt.Sprint(protocol), func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			n := validNode(11)
			n.protocol = protocol
			script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
			page, err := repo.Nodes(context.Background(), 7, 0, 1)
			if err != nil || len(page.Items) != 1 || page.Items[0].Protocol != "socks5" {
				t.Fatalf("legacy SOCKS mapping: %+v, %v", page, err)
			}
		})
	}
}

func TestInvalidEndpointsAreFilteredWithoutRefillingPage(t *testing.T) {
	for _, tc := range []struct {
		host  string
		port  int64
		valid bool
	}{
		{"relay.example", 1, true}, {"relay.example", 65535, true}, {"192.0.2.1", 1080, true}, {"2001:db8::1", 1080, true},
		{"relay.example", 0, false}, {"relay.example", 65536, false}, {"relay.example", -1, false},
		{"https://relay.example", 1080, false}, {"user@relay.example", 1080, false}, {"relay.example/path", 1080, false},
		{"bad host.example", 1080, false}, {"-bad.example", 1080, false}, {"bad..example", 1080, false}, {"relay.example:1080", 1080, false},
	} {
		t.Run(fmt.Sprintf("%s:%d", tc.host, tc.port), func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			n := validNode(11)
			n.customHost, n.port = tc.host, tc.port
			script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
			page, err := repo.Nodes(context.Background(), 7, 0, 1)
			if err != nil || (len(page.Items) == 1) != tc.valid {
				t.Fatalf("endpoint filtering: %+v, %v", page, err)
			}
		})
	}
}

func TestNodeCursorUsesLastScannedRowAndDoesNotSkipLookahead(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	invalid := validNode(11)
	invalid.customHost = "invalid host"
	script.expect(nodeStep("nodes", 7, 9, 2, nodeRows(validNode(10), invalid, validNode(12))))
	page, err := repo.Nodes(context.Background(), 7, 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "10" || !page.Pagination.HasMore || page.Pagination.NextCursor != "MTE" || page.Pagination.PerPage != 2 {
		t.Fatalf("cursor must encode scanned ID 11, not emitted ID 10 or lookahead 12: %+v", page)
	}
	script.expect(nodeStep("nodes", 7, 11, 2, nodeRows(validNode(12))))
	next, err := repo.Nodes(context.Background(), 7, 11, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "12" || next.Pagination.HasMore || next.Pagination.NextCursor != "" {
		t.Fatalf("lookahead row lost: %+v, %v", next, err)
	}
}

func TestEmptyFilteredPageStillAdvancesCursor(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	invalid := validNode(11)
	invalid.port = int64(0)
	script.expect(nodeStep("nodes", 7, 10, 1, nodeRows(invalid, validNode(12))))
	page, err := repo.Nodes(context.Background(), 7, 10, 1)
	if err != nil || len(page.Items) != 0 || !page.Pagination.HasMore || page.Pagination.NextCursor != "MTE" {
		t.Fatalf("filtered page cannot get stuck: %+v, %v", page, err)
	}
}

func TestNodePaginationBoundsAreRejectedBeforeSQL(t *testing.T) {
	for _, tc := range []struct {
		after int64
		limit int
	}{{0, 0}, {0, -1}, {0, 101}, {-1, 10}} {
		repo, _ := repositoryFixture(t, nil)
		if _, err := repo.Nodes(context.Background(), 7, tc.after, tc.limit); err == nil {
			t.Errorf("invalid pagination accepted: %+v", tc)
		}
	}
}

func TestNodePaginationAcceptsMaximumIDAndLimitWithoutOverflow(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	script.expect(nodeStep("nodes", 7, math.MaxInt64, 100, nodeRows()))
	page, err := repo.Nodes(context.Background(), 7, math.MaxInt64, 100)
	if err != nil || len(page.Items) != 0 || page.Pagination.HasMore || page.Pagination.PerPage != 100 {
		t.Fatalf("maximum cursor/limit: %+v, %v", page, err)
	}
}

func TestNodeExpiryAcceptsDriverTimeBytesAndNullAndReturnsUTC(t *testing.T) {
	for _, expires := range []driver.Value{time.Date(2026, 10, 12, 12, 30, 0, 0, fixtureLocation), []byte("2026-10-12 12:30:00"), nil} {
		repo, script := repositoryFixture(t, nil)
		n := validNode(11)
		n.expires = expires
		script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
		page, err := repo.Nodes(context.Background(), 7, 0, 1)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("expiry scan: %+v, %v", page, err)
		}
		got := page.Items[0].ExpiresAt
		if expires == nil {
			if got != nil {
				t.Fatalf("null expiry fabricated: %v", got)
			}
		} else if got == nil || got.Location() != time.UTC || got.Format(time.RFC3339) != "2026-10-12T04:30:00Z" {
			t.Fatalf("expiry is not UTC: %v", got)
		}
	}
}

func TestNodeDetailsDifferentiateForeignMissingAndOwnUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		owner        driver.Value
		wantNotFound bool
	}{{"missing", nil, true}, {"foreign", int64(8), true}, {"own unavailable", int64(7), false}} {
		t.Run(tc.name, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			owner := &fixtureRows{columns: []string{"customer_id"}}
			if tc.owner != nil {
				owner.values = [][]driver.Value{{tc.owner}}
			}
			script.expect(queryStep{kind: "owner", args: []driver.Value{int64(11)}, rows: owner})
			if !tc.wantNotFound {
				script.expect(nodeStep("detail", 7, 11, 0, nodeRows()))
			}
			_, err := repo.Node(context.Background(), 7, 11)
			if tc.wantNotFound {
				if !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("foreign/missing detail: %v", err)
				}
			} else {
				assertAPIError(t, err, "node_unavailable", 409)
			}
		})
	}
}

func TestNodeDetailsEmitFreshCoherentJOINAndRejectInvalidEndpoint(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		repo, script := repositoryFixture(t, nil)
		n := validNode(11)
		n.customHost, n.password = "updated.example", "updated-fixture-password"
		if invalid {
			n.port = int64(0)
		}
		script.expect(queryStep{kind: "owner", args: []driver.Value{int64(11)}, rows: &fixtureRows{columns: []string{"customer_id"}, values: [][]driver.Value{{int64(7)}}}}, nodeStep("detail", 7, 11, 0, nodeRows(n)))
		got, err := repo.Node(context.Background(), 7, 11)
		if invalid {
			assertAPIError(t, err, "node_unavailable", 409)
			continue
		}
		if err != nil || got.Host != "updated.example" || got.Password != "updated-fixture-password" || got.ID != "11" {
			t.Fatalf("detail did not use current JOIN row: %+v, %v", got, err)
		}
	}
}

func TestNodeReadErrorsAreReportedAndRowsClosed(t *testing.T) {
	for _, failure := range []string{"query", "rows", "scan"} {
		t.Run(failure, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			step := nodeStep("nodes", 7, 0, 10, nodeRows(validNode(11)))
			want := errors.New("fixture node read failed")
			switch failure {
			case "query":
				step.err = want
			case "rows":
				step.rows.readErr = want
			case "scan":
				step.rows.values[0][0] = "bad-id"
			}
			script.expect(step)
			if _, err := repo.Nodes(context.Background(), 7, 0, 10); err == nil {
				t.Fatal("database failure returned a partial successful response")
			}
		})
	}
}

func TestTablePrefixAppliesToAllJoinedTables(t *testing.T) {
	repo, script := repositoryFixture(t, func(cfg *Config) { cfg.TablePrefix = "tenant_" })
	step := nodeStep("nodes", 7, 0, 10, nodeRows())
	baseCheck := step.check
	step.check = func(q string, args []driver.NamedValue) error {
		if err := baseCheck(q, args); err != nil {
			return err
		}
		for _, table := range []string{"customers", "subscriptions", "proxy_ips", "forward_rules", "ny_device_groups", "forward_plans"} {
			if err := requireSQL(q, `\b(?:from|join) tenant_`+table+`\b`); err != nil {
				return err
			}
		}
		return requireSQL(q, `\bleft join tenant_forward_rules newer_fr\b`)
	}
	script.expect(step)
	if _, err := repo.Nodes(context.Background(), 7, 0, 10); err != nil {
		t.Fatal(err)
	}
}

func TestLatestMatchingActiveForwardIsSelectedBeforeNodePagination(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	// The scripted result is the SQL boundary's projection: subscription 10's
	// older rule (port 11001) loses to its latest active matching rule (21005).
	// The driver guard requires all anti-join predicates, so removing selection,
	// choosing the oldest ID, or letting inactive/foreign-IP rules win fails.
	first, second, lookahead := validNode(10), validNode(11), validNode(12)
	first.planHost, first.customHost, first.port = "latest-plan.example", nil, int64(21005)
	second.port, lookahead.port = int64(21007), int64(21012)
	script.expect(nodeStep("nodes", 7, 0, 2, nodeRows(first, second, lookahead)))
	page, err := repo.Nodes(context.Background(), 7, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "10" || page.Items[1].ID != "11" ||
		page.Items[0].Port != 21005 || page.Items[0].Host != "latest-plan.example" ||
		!page.Pagination.HasMore || page.Pagination.NextCursor != "MTE" {
		t.Fatalf("latest rule must produce one candidate per subscription before LIMIT: %+v", page)
	}
	script.expect(nodeStep("nodes", 7, 11, 2, nodeRows(lookahead)))
	next, err := repo.Nodes(context.Background(), 7, 11, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "12" || next.Items[0].Port != 21012 || next.Pagination.HasMore {
		t.Fatalf("deduplicated keyset lost its lookahead subscription: %+v, %v", next, err)
	}
}

func TestNodeDetailUsesLatestMatchingActiveForward(t *testing.T) {
	repo, script := repositoryFixture(t, nil)
	latest := validNode(10)
	latest.port, latest.customHost, latest.planHost = int64(21005), nil, "latest-plan.example"
	script.expect(
		queryStep{kind: "owner", args: []driver.Value{int64(10)}, rows: &fixtureRows{columns: []string{"customer_id"}, values: [][]driver.Value{{int64(7)}}}},
		nodeStep("detail", 7, 10, 0, nodeRows(latest)),
	)
	got, err := repo.Node(context.Background(), 7, 10)
	if err != nil || got.Port != 21005 || got.Host != "latest-plan.example" {
		t.Fatalf("detail must use the same latest-rule selection as the list: %+v, %v", got, err)
	}
}

func TestNodeNameUsesAssetNameAndNeutralFallback(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		remark, asset, country driver.Value
		want, wantRemark       string
	}{
		{"conflicting remark", "Customer label", "美国-198.87.96.137", "美国", "美国-198.87.96.137", "Customer label"},
		{"null remark", nil, "Asset label", "Japan", "Asset label", ""},
		{"trimmed asset and remark", " 我的备注 \t", " Asset label \t", "Japan", "Asset label", "我的备注"},
		{"blank remark", " \t", "Asset label", "Japan", "Asset label", ""},
		{"existing asset suffix", "Customer label", "Asset #88", "Japan", "Asset #88", "Customer label"},
		{"null asset ignores remark", "Customer label", nil, " Japan ", "Japan #11", "Customer label"},
		{"empty asset ignores remark", "Customer label", "", "Japan", "Japan #11", "Customer label"},
		{"blank asset and country", "Customer label", " \t", " ", "Node #11", "Customer label"},
		{"all null", nil, nil, nil, "Node #11", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"list", "detail"} {
				t.Run(mode, func(t *testing.T) {
					repo, script := repositoryFixture(t, nil)
					n := validNode(11)
					n.remark, n.assetName, n.countryName = tc.remark, tc.asset, tc.country
					// Neither the relay IP nor the legacy SOCKS endpoint is a name fallback.
					n.customHost = "198.51.100.10"
					var got model.Node
					var err error
					if mode == "list" {
						script.expect(nodeStep("nodes", 7, 0, 1, nodeRows(n)))
						var page model.NodePage
						page, err = repo.Nodes(context.Background(), 7, 0, 1)
						if err != nil || len(page.Items) != 1 {
							t.Fatalf("node list: %+v, %v", page, err)
						}
						got = page.Items[0]
					} else {
						script.expect(
							queryStep{kind: "owner", args: []driver.Value{int64(11)}, rows: &fixtureRows{columns: []string{"customer_id"}, values: [][]driver.Value{{int64(7)}}}},
							nodeStep("detail", 7, 11, 0, nodeRows(n)),
						)
						got, err = repo.Node(context.Background(), 7, 11)
					}
					if err != nil || got.ID != "11" || got.Name != tc.want {
						t.Fatalf("node name: %q, %v; want %q", got.Name, err, tc.want)
					}
					assertCustomerRemarkJSON(t, got, tc.wantRemark)
				})
			}
		})
	}
}

func TestNodeQuerySelectsCustomerRemarkSeparately(t *testing.T) {
	repo, _ := repositoryFixture(t, nil)
	for _, detail := range []bool{false, true} {
		query := normalizeSQL(repo.nodeQuery(detail))
		if !strings.HasPrefix(query, "select s.id, s.customer_remark, ip.asset_name,") {
			t.Errorf("detail=%t: node query must select the customer's subscription remark separately from the asset name", detail)
		}
		if strings.Contains(query, "ip.remark") || strings.Contains(query, "s.remark") {
			t.Errorf("detail=%t: internal remarks must not be selected", detail)
		}
	}
}
