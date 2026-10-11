package source

import (
	"context"
	"fmt"

	"bluegrass/client-api/internal/model"
)

// ExplainNodes returns MySQL's JSON plan for the exact bounded list query.
// It uses EXPLAIN FORMAT=JSON, not EXPLAIN ANALYZE, and preserves the plan text.
func (r *Repository) ExplainNodes(ctx context.Context, customerID, after int64, limit int) (string, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return "", model.Fail("validation_error", 422, "invalid node pagination")
	}
	if customerID <= 0 {
		return "", model.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	var plan string
	// Scan closes the single-row result set, including on scan failure.
	err := r.db.QueryRowContext(ctx, "EXPLAIN FORMAT=JSON "+r.nodeQuery(false),
		customerID, after, r.sourceNow(), limit+1).Scan(&plan)
	if err != nil {
		return "", fmt.Errorf("source: explain nodes: %w", err)
	}
	return plan, nil
}
