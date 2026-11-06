package source

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bluegrass/client-api/internal/model"
)

var _ model.ResourceSource = (*Repository)(nil)

const retainedResourceStates = "('active', 'expired', 'suspended', 'provisioning', 'provision_review')"

// Delivery snapshots are created by ProvisionSubscriptionService::bind. Only
// that small identity object is compared inside SQL; product/credential JSON is
// never projected. Missing snapshots remain supported for legacy assignments.
func resourceDelivery(sub string) string {
	return sub + `.proxy_ip_id = ip.id AND ` + sub + `.status IN ` + retainedResourceStates + `
		AND (ip.released_at IS NULL OR COALESCE(` + sub + `.last_renewed_at, ` + sub + `.started_at) > ip.released_at)
		AND (JSON_EXTRACT(` + sub + `.product_snapshot, '$.upstream_asset') IS NULL
			OR JSON_TYPE(JSON_EXTRACT(` + sub + `.product_snapshot, '$.upstream_asset')) = 'NULL'
			OR JSON_EXTRACT(` + sub + `.product_snapshot, '$.upstream_asset') = JSON_OBJECT(
				'proxy_ip_id', ip.id, 'spark_instance_id', ip.spark_instance_id, 'ipipv_instance_id', ip.ipipv_instance_id))`
}

// Read-only identity checks mirror the evidence used by IpReclaimGuardService,
// not its destructive-cleanup permissions or locking. Multiple entitlements for
// one customer are not a reason to hide any of them. Counts/shared flags never
// manufacture membership for another customer.
func (r *Repository) resourceBoundOwnership() string {
	return `ip.deleted_at IS NULL AND ip.status IN ('assigned', 'expired', 'disabled')
		AND ` + resourceDelivery("s") + `
		AND (ip.assigned_customer_id = s.customer_id
			OR (ip.assigned_customer_id IS NULL AND NOT EXISTS (
				SELECT 1 FROM ` + r.table("subscriptions") + ` conflict
				WHERE conflict.customer_id <> s.customer_id AND ` + resourceDelivery("conflict") + `)))
		AND (NULLIF(ip.spark_instance_id, '') IS NULL OR NULLIF(ip.ipipv_instance_id, '') IS NULL)
		AND (NULLIF(ip.spark_instance_id, '') IS NULL OR EXISTS (
			SELECT 1 FROM ` + r.table("spark_instances") + ` si
			WHERE si.proxy_ip_id = ip.id AND BINARY si.instance_id = BINARY ip.spark_instance_id
				AND NOT EXISTS (SELECT 1 FROM ` + r.table("spark_instances") + ` si_other
					WHERE si_other.proxy_ip_id = ip.id AND si_other.id <> si.id)))
		AND (NULLIF(ip.ipipv_instance_id, '') IS NULL OR EXISTS (
			SELECT 1 FROM ` + r.table("ipipv_instances") + ` ii
			WHERE ii.proxy_ip_id = ip.id AND BINARY ii.instance_no = BINARY ip.ipipv_instance_id
				AND NOT EXISTS (SELECT 1 FROM ` + r.table("ipipv_instances") + ` ii_other
					WHERE ii_other.proxy_ip_id = ip.id AND ii_other.id <> ii.id)))`
}

// Each kind has its own indexed keyset. No OFFSET, COUNT, credentials, write,
// transaction or account-wide snapshot is needed. All authorization predicates
// share the SELECT which emits the DTO. Ambiguous historical ownership is
// intentionally omitted; coverage advertises that limitation to callers.
func (r *Repository) resourceQuery(kind string, detail bool) string {
	comparison, tail := ">", " ORDER BY s.id ASC LIMIT ?"
	if detail {
		comparison, tail = "=", " LIMIT 1"
	}
	if kind == "asset" {
		if !detail {
			tail = " ORDER BY ip.id ASC LIMIT ?"
		}
		return `SELECT ip.id, NULL, ip.asset_name, NULL, ip.status
			FROM ` + r.table("proxy_ips") + ` ip
			JOIN ` + r.table("customers") + ` c ON c.id = ip.assigned_customer_id AND c.status = 1 AND c.deleted_at IS NULL
			WHERE ip.assigned_customer_id = ? AND ip.id ` + comparison + ` ?
				AND ip.deleted_at IS NULL
				AND ip.status IN ('assigned', 'expired', 'disabled')
				AND NOT EXISTS (SELECT 1 FROM ` + r.table("subscriptions") + ` s WHERE s.proxy_ip_id = ip.id
					AND s.customer_id = ip.assigned_customer_id AND ` + r.resourceBoundOwnership() + `)` + tail
	}
	// The authenticated customer's subscription remark is independent of the
	// asset name, including pending deliveries whose asset name is withheld.
	return `SELECT s.id, s.customer_remark,
		CASE WHEN s.status IN ('provisioning', 'provision_review') THEN NULL ELSE ip.asset_name END,
		s.expires_at, s.status
		FROM ` + r.table("subscriptions") + ` s
		JOIN ` + r.table("customers") + ` c ON c.id = s.customer_id AND c.status = 1 AND c.deleted_at IS NULL
		LEFT JOIN ` + r.table("proxy_ips") + ` ip ON ip.id = s.proxy_ip_id AND ip.deleted_at IS NULL
		WHERE s.customer_id = ? AND s.id ` + comparison + ` ?
			AND s.status IN ` + retainedResourceStates + `
			AND (s.status IN ('provisioning', 'provision_review')
				OR (` + r.resourceBoundOwnership() + `))` + tail
}

func resourceKind(kind string) bool { return kind == "subscription" || kind == "asset" }

func (r *Repository) Resources(ctx context.Context, customerID int64, kind string, after int64, limit int) (model.ResourcePage, error) {
	if !resourceKind(kind) || after < 0 || limit < 1 || limit > 100 {
		return model.ResourcePage{}, model.Fail("validation_error", 422, "invalid resource pagination")
	}
	if customerID <= 0 {
		return model.ResourcePage{}, model.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	now := r.cfg.Now()
	rows, err := r.db.QueryContext(ctx, r.resourceQuery(kind, false), customerID, after, limit+1)
	if err != nil {
		return model.ResourcePage{}, fmt.Errorf("source: query resources: %w", err)
	}
	defer rows.Close()
	page := model.ResourcePage{Items: make([]model.Resource, 0, limit), Pagination: model.Pagination{PerPage: limit}, Coverage: model.ResourceCoverage{Kind: kind, Consistency: "per_page", Ownership: "conservative"}}
	var last string
	for rows.Next() {
		if len(page.Items) == limit {
			page.Pagination.HasMore = true
			break
		}
		item, err := r.scanResource(rows, kind, now)
		if err != nil {
			return model.ResourcePage{}, fmt.Errorf("source: scan resource: %w", err)
		}
		page.Items = append(page.Items, item)
		last = item.ID
	}
	if err := rows.Err(); err != nil {
		return model.ResourcePage{}, fmt.Errorf("source: read resources: %w", err)
	}
	if err := rows.Close(); err != nil {
		return model.ResourcePage{}, fmt.Errorf("source: close resources: %w", err)
	}
	if page.Pagination.HasMore {
		page.Pagination.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(kind + ":" + last))
	}
	return page, nil
}

func (r *Repository) Resource(ctx context.Context, customerID int64, kind string, id int64) (model.Resource, error) {
	if !resourceKind(kind) {
		return model.Resource{}, model.Fail("validation_error", 422, "invalid resource kind")
	}
	if customerID <= 0 || id <= 0 {
		return model.Resource{}, model.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	row := r.db.QueryRowContext(ctx, r.resourceQuery(kind, true), customerID, id)
	item, err := r.scanResource(row, kind, r.cfg.Now())
	if errors.Is(err, sql.ErrNoRows) {
		return model.Resource{}, model.ErrNotFound
	}
	if err != nil {
		return model.Resource{}, fmt.Errorf("source: read resource: %w", err)
	}
	return item, nil
}

func (r *Repository) scanResource(row rowScanner, kind string, now time.Time) (model.Resource, error) {
	var id int64
	var remark, name, status sql.NullString
	expires := sourceTime{location: r.cfg.SourceLocation}
	if err := row.Scan(&id, &remark, &name, &expires, &status); err != nil {
		return model.Resource{}, err
	}
	item := model.Resource{ID: strconv.FormatInt(id, 10), Kind: kind, Status: status.String, Owned: true}
	if kind == "subscription" {
		item.CustomerRemark = strings.TrimSpace(remark.String)
	}
	label := strings.TrimSpace(name.String)
	if label == "" {
		label = "Resource #" + item.ID
	}
	item.Name = label
	if expires.valid {
		item.ExpiresAt = &expires.time
		if item.Status == "active" && !expires.time.After(now) {
			item.Status = "expired"
		}
	}
	return item, nil
}
