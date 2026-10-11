// Package source reads the existing business database without mutating it.
package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"bluegrass/client-api/internal/model"
)

type Config struct {
	TablePrefix          string
	QueryTimeout         time.Duration
	SourceLocation       *time.Location
	Now                  func() time.Time
	PhoneRefreshInterval time.Duration
	MaxPhoneEntries      int
}

// Repository reads current business rows through ordinary, nonlocking SELECTs.
// It owns neither the DB pool nor its connections. Call RefreshPhones at startup
// if phone login must be ready before serving, and run RunPhoneRefresh until the
// application context is cancelled. Customer state and nodes are never cached.
type Repository struct {
	db          *sql.DB
	cfg         Config
	phones      atomic.Pointer[phoneDirectory]
	refreshGate chan struct{}
}

var _ model.Source = (*Repository)(nil)

// New validates configuration without issuing SQL or starting goroutines. Zero
// budgets default to a 3s query timeout, 1m phone refresh and 100,000 phone keys.
// MySQL's source timestamps are wall times in UTC+8 unless overridden explicitly.
func New(db *sql.DB, cfg Config) (*Repository, error) {
	if db == nil {
		return nil, errors.New("source: nil database")
	}
	if len(cfg.TablePrefix) > 24 {
		return nil, errors.New("source: table prefix exceeds 24 characters")
	}
	for _, c := range cfg.TablePrefix {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return nil, errors.New("source: table prefix must contain only lowercase ASCII letters, digits and underscores")
		}
	}
	if cfg.QueryTimeout < 0 || cfg.PhoneRefreshInterval < 0 || cfg.MaxPhoneEntries < 0 {
		return nil, errors.New("source: budgets must not be negative")
	}
	if cfg.QueryTimeout == 0 {
		cfg.QueryTimeout = 3 * time.Second
	}
	if cfg.PhoneRefreshInterval == 0 {
		cfg.PhoneRefreshInterval = time.Minute
	}
	if cfg.MaxPhoneEntries == 0 {
		cfg.MaxPhoneEntries = 100000
	}
	if cfg.SourceLocation == nil {
		cfg.SourceLocation = time.FixedZone("source+08", 8*60*60)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &Repository{db: db, cfg: cfg, refreshGate: make(chan struct{}, 1)}
	r.phones.Store(&phoneDirectory{byPhone: make(map[string]int64)})
	return r, nil
}

// table only receives package constants, and the prefix was validated by New.
func (r *Repository) table(name string) string {
	return "`" + r.cfg.TablePrefix + name + "`"
}

// CustomerByLogin tries the indexed username first. Phone misses do not trigger
// a scan; additions can take one successful refresh interval to become visible.
func (r *Repository) CustomerByLogin(ctx context.Context, login string) (model.Customer, error) {
	customer, err := r.readCustomer(ctx, "username", login)
	if !errors.Is(err, model.ErrNotFound) {
		return customer, err
	}
	id, ok := r.phones.Load().byPhone[login]
	if !ok {
		return model.Customer{}, model.ErrNotFound
	}
	// Other programs can change a phone, password, status or deletion flag at
	// any time. The directory is an ID hint, never an authentication snapshot.
	customer, err = r.CustomerByID(ctx, id)
	if err != nil {
		return model.Customer{}, err
	}
	if customer.Phone != login {
		return model.Customer{}, model.ErrNotFound
	}
	return customer, nil
}

func (r *Repository) CustomerByID(ctx context.Context, id int64) (model.Customer, error) {
	if id <= 0 {
		return model.Customer{}, model.ErrNotFound
	}
	return r.readCustomer(ctx, "id", id)
}

func (r *Repository) readCustomer(ctx context.Context, column string, value any) (model.Customer, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.QueryTimeout)
	defer cancel()
	// column is one of the two package literals above, never caller input.
	query := "SELECT id, username, display_name, phone, password, status FROM " + r.table("customers") +
		" WHERE " + column + " = ? AND deleted_at IS NULL LIMIT 1"
	var customer model.Customer
	var username, displayName, phone, hash sql.NullString
	var status sql.NullInt64
	// QueryRowContext.Scan closes its result set, including on scan failure.
	err := r.db.QueryRowContext(ctx, query, value).Scan(&customer.ID, &username, &displayName, &phone, &hash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Customer{}, model.ErrNotFound
	}
	if err != nil {
		return model.Customer{}, fmt.Errorf("source: read customer: %w", err)
	}
	customer.Username = username.String
	customer.DisplayName = displayName.String
	if customer.DisplayName == "" {
		customer.DisplayName = customer.Username
	}
	customer.Phone = phone.String
	customer.PasswordHash = hash.String
	customer.Status = int(status.Int64)
	return customer, nil
}
