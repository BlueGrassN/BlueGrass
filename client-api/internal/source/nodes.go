package source

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"bluegrass/client-api/internal/model"
)

// nodeQuery authorizes and selects every emitted field in one MVCC statement.
// No account read performed earlier in a request can authorize this snapshot.
// The historical release marker blocks access only when no later activation
// exists. Upstream expiry is deliberately absent: customer expiry is s.expires_at.
// The anti-join keeps only the largest matching active forward-rule ID before
// endpoint eligibility and keyset pagination; duplicate active rules are legal.
func (r *Repository) nodeQuery(detail bool) string {
	comparison, tail := ">", " ORDER BY s.id ASC LIMIT ?"
	if detail {
		comparison, tail = "=", " LIMIT 1"
	}
	// Customer-authored remarks are returned separately from the asset name.
	return `SELECT s.id, s.customer_remark, ip.asset_name,
		ip.country_code, ip.country_name, ip.city, s.expires_at,
		s.custom_host, fp.display_host, dg.custom_connect_host, dg.original_connect_host,
		fr.listen_port, ip.auth_username, ip.auth_password, ip.socks5_info, ip.protocol
		FROM ` + r.table("subscriptions") + ` s
		JOIN ` + r.table("customers") + ` c
			ON c.id = s.customer_id AND c.status = 1 AND c.deleted_at IS NULL
		JOIN ` + r.table("proxy_ips") + ` ip
			ON ip.id = s.proxy_ip_id AND ip.deleted_at IS NULL
		JOIN ` + r.table("forward_rules") + ` fr
			ON fr.subscription_id = s.id AND fr.proxy_ip_id = s.proxy_ip_id AND fr.status = 'active'
		LEFT JOIN ` + r.table("forward_rules") + ` newer_fr
			ON newer_fr.subscription_id = s.id AND newer_fr.proxy_ip_id = s.proxy_ip_id
				AND newer_fr.status = 'active' AND newer_fr.id > fr.id
		JOIN ` + r.table("ny_device_groups") + ` dg
			ON dg.id = fr.ny_device_group_id AND dg.is_enabled = 1
		LEFT JOIN ` + r.table("forward_plans") + ` fp ON fp.id = fr.forward_plan_id
		WHERE newer_fr.id IS NULL AND s.customer_id = ? AND s.id ` + comparison + ` ?
			AND s.status = 'active' AND s.has_forward = 1
			AND (s.expires_at IS NULL OR s.expires_at > ?)
			AND (ip.status IS NULL OR ip.status <> 'disabled')
			AND (ip.released_at IS NULL OR COALESCE(s.last_renewed_at, s.started_at) > ip.released_at)
			AND LOWER(COALESCE(NULLIF(ip.protocol, ''), 'socks5')) IN ('socks', 'socks5')` + tail
}

func (r *Repository) sourceNow() string {
	return r.cfg.Now().In(r.cfg.SourceLocation).Format("2006-01-02 15:04:05.999999")
}

// Nodes scans at most limit candidates plus one lookahead row. Invalid endpoints
// can produce short or empty pages; the cursor still advances over those rows.
func (r *Repository) Nodes(ctx context.Context, customerID, after int64, limit int) (model.NodePage, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return model.NodePage{}, model.Fail("validation_error", 422, "invalid node pagination")
	}
	if customerID <= 0 {
		return model.NodePage{}, model.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, r.nodeQuery(false), customerID, after, r.sourceNow(), limit+1)
	if err != nil {
		return model.NodePage{}, fmt.Errorf("source: query nodes: %w", err)
	}
	defer rows.Close()
	page := model.NodePage{Items: make([]model.Node, 0, limit), Pagination: model.Pagination{PerPage: limit}}
	last, scanned := after, 0
	for rows.Next() {
		if scanned == limit {
			// Presence alone determines has_more. Do not scan/map this row or
			// advance the cursor past it; it belongs to the next request.
			page.Pagination.HasMore = true
			break
		}
		raw := nodeRow{expires: sourceTime{location: r.cfg.SourceLocation}}
		if err := raw.scan(rows); err != nil {
			return model.NodePage{}, fmt.Errorf("source: scan node: %w", err)
		}
		last, scanned = raw.id, scanned+1
		if node, valid := raw.node(); valid {
			page.Items = append(page.Items, node)
		}
	}
	if err := rows.Err(); err != nil {
		return model.NodePage{}, fmt.Errorf("source: read nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return model.NodePage{}, fmt.Errorf("source: close node rows: %w", err)
	}
	if page.Pagination.HasMore {
		page.Pagination.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last, 10)))
	}
	return page, nil
}

func (r *Repository) Node(ctx context.Context, customerID, subscriptionID int64) (model.Node, error) {
	if customerID <= 0 || subscriptionID <= 0 {
		return model.Node{}, model.ErrNotFound
	}
	// This separate lookup classifies unavailable vs foreign/missing only.
	// No DTO fields or authorization decisions are reused from this snapshot.
	owner, err := r.subscriptionOwner(ctx, subscriptionID)
	if err != nil {
		return model.Node{}, err
	}
	if owner != customerID {
		return model.Node{}, model.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	row := r.db.QueryRowContext(ctx, r.nodeQuery(true), customerID, subscriptionID, r.sourceNow())
	raw := nodeRow{expires: sourceTime{location: r.cfg.SourceLocation}}
	err = raw.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Node{}, unavailableNode()
	}
	if err != nil {
		return model.Node{}, fmt.Errorf("source: read node: %w", err)
	}
	node, valid := raw.node()
	if !valid {
		return model.Node{}, unavailableNode()
	}
	return node, nil
}

func (r *Repository) subscriptionOwner(ctx context.Context, id int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	var owner int64
	err := r.db.QueryRowContext(ctx, "SELECT customer_id FROM "+r.table("subscriptions")+" WHERE id = ? LIMIT 1", id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, model.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("source: read subscription owner: %w", err)
	}
	return owner, nil
}

func unavailableNode() error {
	return model.Fail("node_unavailable", 409, "node is currently unavailable")
}

type nodeRow struct {
	id                                                int64
	remark, assetName, countryCode, countryName, city sql.NullString
	customHost, planHost, groupCustom, groupOriginal  sql.NullString
	username, password, socksInfo, protocol           sql.NullString
	port                                              sql.NullInt64
	expires                                           sourceTime
}

type rowScanner interface {
	Scan(...any) error
}

func (n *nodeRow) scan(row rowScanner) error {
	return row.Scan(&n.id, &n.remark, &n.assetName, &n.countryCode, &n.countryName, &n.city, &n.expires,
		&n.customHost, &n.planHost, &n.groupCustom, &n.groupOriginal, &n.port,
		&n.username, &n.password, &n.socksInfo, &n.protocol)
}

func (n *nodeRow) node() (model.Node, bool) {
	if n.protocol.Valid && n.protocol.String != "" {
		protocol := strings.ToLower(n.protocol.String)
		if protocol != "socks" && protocol != "socks5" {
			return model.Node{}, false
		}
	}
	// Choose by configured priority before validation. A malformed override
	// must not silently connect through a different relay or the raw IP.
	host := ""
	for _, candidate := range []sql.NullString{n.customHost, n.planHost, n.groupCustom, n.groupOriginal} {
		if candidate.Valid && candidate.String != "" {
			host = strings.TrimSpace(candidate.String)
			break
		}
	}
	if !validHost(host) || !n.port.Valid || n.port.Int64 < 1 || n.port.Int64 > 65535 {
		return model.Node{}, false
	}
	username, password := n.username.String, n.password.String
	if !n.username.Valid || !n.password.Valid {
		// Production keeps at most four parts so colons in passwords survive.
		parts := strings.SplitN(n.socksInfo.String, ":", 4)
		if !n.username.Valid && len(parts) > 2 {
			username = parts[2]
		}
		if !n.password.Valid && len(parts) > 3 {
			password = parts[3]
		}
	}
	id := strconv.FormatInt(n.id, 10)
	label := strings.TrimSpace(n.assetName.String)
	if label == "" {
		label = strings.TrimSpace(n.countryName.String)
		if label == "" {
			label = "Node"
		}
		label += " #" + id
	}
	node := model.Node{
		ID: id, Name: label, Protocol: "socks5",
		CustomerRemark: strings.TrimSpace(n.remark.String),
		Host:           host, Port: int(n.port.Int64), Username: username, Password: password,
		CountryCode: n.countryCode.String, CountryName: n.countryName.String,
		City: n.city.String, IsForwarded: true,
	}
	if n.expires.valid {
		expires := n.expires.time
		node.ExpiresAt = &expires
	}
	return node, true
}

// validHost accepts literal IPv4/IPv6 or ASCII DNS hostnames without DNS lookups.
// URLs, credentials, ports, scoped IPv6 and malformed DNS labels are rejected.
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	name := strings.TrimSuffix(host, ".")
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
