package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

// These tests only run through testdb's explicitly configured isolated fixture.
// They are intentionally skipped when DB writes are not authorized.
func TestOwnedResourcesMVCCExpiryReleaseAndTransfer(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	f.Exec(t, "UPDATE `{p}proxy_ips` SET assigned_customer_id=1 WHERE id=1")
	f.Exec(t, "UPDATE `{p}subscriptions` SET expires_at='2020-01-01 00:00:00',has_forward=0 WHERE id=11")
	page, err := r.Resources(context.Background(), 1, "subscription", 0, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != "expired" || page.Items[0].Connectable {
		t.Fatalf("expired, unforwarded owned resource: %+v, %v", page, err)
	}
	if _, err := r.Resource(context.Background(), 2, "subscription", 11); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("foreign detail leaked")
	}
	tx, err := f.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE `" + f.Prefix + "proxy_ips` SET assigned_customer_id=2 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := r.Resource(ctx, 1, "subscription", 11); err != nil {
		t.Fatalf("MVCC read blocked by writer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resource(context.Background(), 1, "subscription", 11); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("committed asset reassignment ignored")
	}
	f.Exec(t, "UPDATE `{p}proxy_ips` SET assigned_customer_id=1,released_at='2026-06-01 00:00:00' WHERE id=1")
	if _, err := r.Resource(context.Background(), 1, "subscription", 11); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("released resource included")
	}
	f.Exec(t, "UPDATE `{p}subscriptions` SET last_renewed_at='2026-07-01 00:00:00' WHERE id=11")
	if _, err := r.Resource(context.Background(), 1, "subscription", 11); err != nil {
		t.Fatalf("renewed ownership lost to old release marker: %v", err)
	}
	f.Exec(t, "UPDATE `{p}customers` SET status=0 WHERE id=1")
	if _, err := r.Resource(context.Background(), 1, "subscription", 11); !errors.Is(err, model.ErrNotFound) {
		t.Fatal("disabled account got resource")
	}
}

func TestOwnedResourcesPendingUnlinkedAssetsAndHistoricalSubscriptions(t *testing.T) {
	f, r := repository(t)
	seed(t, f)
	f.Exec(t, "UPDATE `{p}proxy_ips` SET assigned_customer_id=1 WHERE id=1")
	f.Exec(t, "INSERT INTO `{p}subscriptions` (id,customer_id,status) VALUES (12,1,'provisioning'),(13,1,'provision_review'),(14,1,'cancelled'),(15,1,'refunded'),(16,1,'provision_failed')")
	f.Exec(t, "INSERT INTO `{p}proxy_ips` (id,assigned_customer_id,asset_name,status,upstream_expires_at) VALUES (2,1,'Unlinked','assigned','2099-01-01'),(3,2,'Foreign','assigned','2099-01-01'),(4,1,'Released','released','2099-01-01'),(5,1,'Disabled','disabled','2099-01-01')")
	page, err := r.Resources(context.Background(), 1, "subscription", 0, 2)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "11" || page.Items[1].ID != "12" || !page.Pagination.HasMore {
		t.Fatalf("first bounded page: %+v, %v", page, err)
	}
	page, err = r.Resources(context.Background(), 1, "subscription", 12, 2)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "13" || page.Items[0].ExpiresAt != nil || page.Pagination.HasMore {
		t.Fatalf("pending/final page: %+v, %v", page, err)
	}
	assets, err := r.Resources(context.Background(), 1, "asset", 0, 10)
	if err != nil || len(assets.Items) != 2 || assets.Items[0].ID != "2" || assets.Items[1].ID != "5" || assets.Items[0].ExpiresAt != nil {
		t.Fatalf("raw asset authorization/expiry: %+v, %v", assets, err)
	}
	f.Exec(t, "INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status,started_at) VALUES (17,2,1,'expired','2026-02-01')")
	if _, err := r.Resource(context.Background(), 1, "subscription", 11); err != nil {
		t.Fatalf("newer foreign historical subscription hid the current assigned entitlement: %v", err)
	}
}

func TestOwnedResourceLifecycleFixtures(t *testing.T) {
	// All values are synthetic. Test execution remains gated by the existing
	// isolated-MySQL fixture and is skipped during this no-DB-write task.
	for _, tc := range []struct {
		name    string
		changes []string
		owned   bool
	}{
		{"null_assignment_uncontested", nil, true},
		{"null_same_customer_overlap", []string{"INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,1,1,'active')"}, true},
		{"null_foreign_current_conflict", []string{"INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,2,1,'active')"}, false},
		{"null_foreign_expired_same_delivery_ambiguous", []string{"INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,2,1,'expired')"}, false},
		{"null_foreign_cancelled_history", []string{"INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,2,1,'cancelled')"}, true},
		{"null_foreign_old_delivery", []string{`INSERT INTO ` + "`{p}subscriptions`" + ` (id,customer_id,proxy_ip_id,status,product_snapshot) VALUES (12,2,1,'active','{"upstream_asset":{"proxy_ip_id":999,"spark_instance_id":null,"ipipv_instance_id":null}}')`}, true},
		{"assigned_same_customer_shared_overlap", []string{"UPDATE `{p}proxy_ips` SET assigned_customer_id=1,access_type='shared',shared_user_count=2 WHERE id=1", "INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,1,1,'active')"}, true},
		{"shared_counts_do_not_override_foreign_assignment", []string{"UPDATE `{p}proxy_ips` SET assigned_customer_id=2,access_type='shared',shared_user_count=2 WHERE id=1"}, false},
		{"shared_null_cross_customer_ambiguous", []string{"UPDATE `{p}proxy_ips` SET access_type='shared',shared_user_count=2 WHERE id=1", "INSERT INTO `{p}subscriptions` (id,customer_id,proxy_ip_id,status) VALUES (12,2,1,'active')"}, false},
		{"matching_snapshot", []string{`UPDATE ` + "`{p}subscriptions`" + ` SET product_snapshot='{"upstream_asset":{"proxy_ip_id":1,"spark_instance_id":null,"ipipv_instance_id":null}}' WHERE id=11`}, true},
		{"snapshot_json_null_is_legacy", []string{`UPDATE ` + "`{p}subscriptions`" + ` SET product_snapshot='{"upstream_asset":null}' WHERE id=11`}, true},
		{"different_delivery_snapshot", []string{`UPDATE ` + "`{p}subscriptions`" + ` SET product_snapshot='{"upstream_asset":{"proxy_ip_id":2,"spark_instance_id":null,"ipipv_instance_id":null}}' WHERE id=11`}, false},
		{"incomplete_snapshot_not_current_proof", []string{`UPDATE ` + "`{p}subscriptions`" + ` SET product_snapshot='{"upstream_asset":{"proxy_ip_id":1}}' WHERE id=11`}, false},
		{"legacy_unique_spark_binding", []string{"UPDATE `{p}proxy_ips` SET spark_instance_id='instance-A' WHERE id=1", "INSERT INTO `{p}spark_instances` (id,proxy_ip_id,instance_id) VALUES (1,1,'instance-A')"}, true},
		{"missing_spark_binding", []string{"UPDATE `{p}proxy_ips` SET spark_instance_id='instance-A' WHERE id=1"}, false},
		{"duplicate_spark_binding", []string{"UPDATE `{p}proxy_ips` SET spark_instance_id='instance-A' WHERE id=1", "INSERT INTO `{p}spark_instances` (id,proxy_ip_id,instance_id) VALUES (1,1,'instance-A'),(2,1,'instance-A')"}, false},
		{"case_sensitive_supplier_identity", []string{"UPDATE `{p}proxy_ips` SET spark_instance_id='instance-A' WHERE id=1", "INSERT INTO `{p}spark_instances` (id,proxy_ip_id,instance_id) VALUES (1,1,'instance-a')"}, false},
		{"legacy_unique_ipipv_binding", []string{"UPDATE `{p}proxy_ips` SET ipipv_instance_id='instance-B' WHERE id=1", "INSERT INTO `{p}ipipv_instances` (id,proxy_ip_id,instance_no) VALUES (1,1,'instance-B')"}, true},
		{"two_suppliers_ambiguous", []string{"UPDATE `{p}proxy_ips` SET spark_instance_id='instance-A',ipipv_instance_id='instance-B' WHERE id=1", "INSERT INTO `{p}spark_instances` (id,proxy_ip_id,instance_id) VALUES (1,1,'instance-A')", "INSERT INTO `{p}ipipv_instances` (id,proxy_ip_id,instance_no) VALUES (1,1,'instance-B')"}, false},
		{"released_before_renewal", []string{"UPDATE `{p}proxy_ips` SET released_at='2026-06-01' WHERE id=1", "UPDATE `{p}subscriptions` SET last_renewed_at='2026-07-01' WHERE id=11"}, true},
		{"released_after_activation", []string{"UPDATE `{p}proxy_ips` SET released_at='2026-06-01' WHERE id=1"}, false},
		{"deleted_binding", []string{"UPDATE `{p}proxy_ips` SET deleted_at='2026-06-01' WHERE id=1"}, false},
		{"unassigned_inventory", []string{"UPDATE `{p}proxy_ips` SET status='available' WHERE id=1"}, false},
		{"pending_foreign_binding_is_order_only", []string{"UPDATE `{p}subscriptions` SET status='provisioning',expires_at=NULL WHERE id=11", "UPDATE `{p}proxy_ips` SET assigned_customer_id=2,asset_name='Foreign asset must stay hidden' WHERE id=1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := repository(t)
			seed(t, f)
			for _, change := range tc.changes {
				f.Exec(t, change)
			}
			item, err := r.Resource(context.Background(), 1, "subscription", 11)
			if tc.owned && err != nil || !tc.owned && !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("detail owned=%v: %v", tc.owned, err)
			}
			page, err := r.Resources(context.Background(), 1, "subscription", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, value := range page.Items {
				if value.ID == "11" {
					found = true
				}
			}
			if found != tc.owned || page.Coverage.SnapshotComplete {
				t.Fatal("list/detail ownership or snapshot contract diverged")
			}
			if (tc.name == "null_same_customer_overlap" || tc.name == "assigned_same_customer_shared_overlap") && len(page.Items) != 2 {
				t.Fatal("same-customer overlapping entitlements were collapsed")
			}
			if tc.name == "pending_foreign_binding_is_order_only" && item.Name != "Resource #11" {
				t.Fatal("pending summary leaked another asset's name")
			}
			if _, err := r.Resource(context.Background(), 2, "subscription", 11); !errors.Is(err, model.ErrNotFound) {
				t.Fatal("foreign subscription detail leaked")
			}
		})
	}
}

func TestBareAssetSuppressionUsesOnlyRepresentedCurrentEntitlements(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes []string
		bare    bool
	}{
		{"represented", nil, false},
		{"foreign_history", []string{"UPDATE `{p}subscriptions` SET customer_id=2,status='expired' WHERE id=11"}, true},
		{"cancelled_history", []string{"UPDATE `{p}subscriptions` SET status='cancelled' WHERE id=11"}, true},
		{"old_delivery", []string{`UPDATE ` + "`{p}subscriptions`" + ` SET product_snapshot='{"upstream_asset":{"proxy_ip_id":999,"spark_instance_id":null,"ipipv_instance_id":null}}' WHERE id=11`}, true},
		{"current_assignment_after_historical_release", []string{"UPDATE `{p}proxy_ips` SET released_at='2026-06-01' WHERE id=1"}, true},
		{"renewal_represents_reactivated_asset", []string{"UPDATE `{p}proxy_ips` SET released_at='2026-06-01' WHERE id=1", "UPDATE `{p}subscriptions` SET last_renewed_at='2026-07-01' WHERE id=11"}, false},
		{"unassigned", []string{"UPDATE `{p}proxy_ips` SET assigned_customer_id=NULL WHERE id=1"}, false},
		{"released", []string{"UPDATE `{p}proxy_ips` SET status='released',assigned_customer_id=NULL WHERE id=1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := repository(t)
			seed(t, f)
			f.Exec(t, "UPDATE `{p}proxy_ips` SET assigned_customer_id=1 WHERE id=1")
			for _, change := range tc.changes {
				f.Exec(t, change)
			}
			item, err := r.Resource(context.Background(), 1, "asset", 1)
			if tc.bare && err != nil || !tc.bare && !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("bare=%v: %v", tc.bare, err)
			}
			if tc.bare && item.ExpiresAt != nil {
				t.Fatal("historical subscription expiry attached to bare asset")
			}
			page, err := r.Resources(context.Background(), 1, "asset", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if (len(page.Items) == 1) != tc.bare {
				t.Fatal("bare list/detail disagree")
			}
		})
	}
}
