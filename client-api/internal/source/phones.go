package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const phoneBatchSize = 2000

// Only phone numbers and IDs are retained. A published directory is immutable.
type phoneDirectory struct {
	byPhone map[string]int64
}

// RefreshPhones builds a replacement directory without holding database locks.
// Each batch has a new SELECT snapshot, so concurrent changes can delay a phone
// addition until the next refresh. Login always rechecks the current row by PK.
// A failed or cancelled refresh leaves the previous complete directory intact.
func (r *Repository) RefreshPhones(ctx context.Context) error {
	// Serialize refreshes to prevent an older refresh overwriting a newer one.
	// Waiting callers remain cancellable, and readers never take this gate.
	select {
	case r.refreshGate <- struct{}{}:
		defer func() { <-r.refreshGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	next := &phoneDirectory{byPhone: make(map[string]int64)}
	var after int64
	for {
		last, count, err := r.readPhoneBatch(ctx, after, next)
		if err != nil {
			return err
		}
		if count < phoneBatchSize {
			break
		}
		after = last
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.phones.Store(next)
	return nil
}

func (r *Repository) readPhoneBatch(ctx context.Context, after int64, next *phoneDirectory) (int64, int, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	query := "SELECT id, phone FROM " + r.table("customers") +
		" WHERE id > ? AND deleted_at IS NULL ORDER BY id ASC LIMIT ?"
	rows, err := r.db.QueryContext(ctx, query, after, phoneBatchSize)
	if err != nil {
		return after, 0, fmt.Errorf("source: query phone directory: %w", err)
	}
	defer rows.Close()
	last, count := after, 0
	for rows.Next() {
		var id int64
		var phone sql.NullString
		if err := rows.Scan(&id, &phone); err != nil {
			return after, count, fmt.Errorf("source: scan phone directory: %w", err)
		}
		if id <= last {
			return after, count, errors.New("source: phone keyset did not advance")
		}
		last, count = id, count+1
		if !phone.Valid || phone.String == "" {
			continue
		}
		if _, exists := next.byPhone[phone.String]; exists {
			// Ascending IDs make the first nondeleted duplicate deterministic.
			continue
		}
		if len(next.byPhone) >= r.cfg.MaxPhoneEntries {
			return after, count, errors.New("source: phone directory capacity exceeded; previous directory retained")
		}
		next.byPhone[phone.String] = id
	}
	if err := rows.Err(); err != nil {
		return after, count, fmt.Errorf("source: read phone directory: %w", err)
	}
	if err := rows.Close(); err != nil {
		return after, count, fmt.Errorf("source: close phone directory rows: %w", err)
	}
	return last, count, nil
}

// RunPhoneRefresh refreshes immediately, then waits an interval after each
// attempt. It does not launch another goroutine or overlap its own refreshes.
func (r *Repository) RunPhoneRefresh(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.RefreshPhones(ctx); err != nil && ctx.Err() == nil {
			// Avoid logging driver errors that may embed source data. Direct
			// RefreshPhones callers receive the wrapped error for diagnosis.
			slog.WarnContext(ctx, "source phone directory refresh failed; previous directory retained")
		}
		timer := time.NewTimer(r.cfg.PhoneRefreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
