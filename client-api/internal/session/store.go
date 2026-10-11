package session

import (
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/secure"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Prefix  string
	Key     []byte
	Timeout time.Duration
}
type Store struct {
	db  *sql.DB
	cfg Config
}

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)

func New(db *sql.DB, cfg Config) (*Store, error) {
	if db == nil || !prefixPattern.MatchString(cfg.Prefix) || len(cfg.Key) != 32 || cfg.Timeout <= 0 {
		return nil, errors.New("invalid session store configuration")
	}
	cfg.Key = append([]byte(nil), cfg.Key...)
	return &Store{db, cfg}, nil
}
func (s *Store) table(name string) string { return "`" + s.cfg.Prefix + name + "`" }

func (s *Store) Migrate(ctx context.Context) error {
	// Explicit CLI operation only; never run DDL during HTTP requests/startup.
	queries := []string{
		"CREATE TABLE IF NOT EXISTS " + s.table("sessions") + ` (
          id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
          customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL, device_name VARCHAR(120) NOT NULL,
          credential_fingerprint BINARY(32) NOT NULL, access_hash BINARY(32) NOT NULL,
          access_expires_at DATETIME(6) NOT NULL, idle_expires_at DATETIME(6) NOT NULL,
          generation BIGINT NOT NULL DEFAULT 0, revoked_at DATETIME(6) NULL,
          created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
          UNIQUE KEY(access_hash), KEY(customer_id,device_hash), KEY(idle_expires_at), KEY(revoked_at)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		"CREATE TABLE IF NOT EXISTS " + s.table("refresh_tokens") + ` (
          token_hash BINARY(32) PRIMARY KEY,
          session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
          generation BIGINT NOT NULL, used_at DATETIME(6) NULL, request_hash BINARY(32) NULL,
          retry_cipher BLOB NULL, retry_expires_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL,
          KEY(session_id), KEY(retry_expires_at), KEY(used_at),
          FOREIGN KEY(session_id) REFERENCES ` + s.table("sessions") + `(id) ON DELETE CASCADE
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		"CREATE TABLE IF NOT EXISTS " + s.table("device_sessions") + ` (
          customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL,
          session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL, updated_at DATETIME(6) NOT NULL,
          PRIMARY KEY(customer_id,device_hash), KEY(session_id),
          FOREIGN KEY(session_id) REFERENCES ` + s.table("sessions") + `(id) ON DELETE SET NULL
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, q := range queries {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := s.db.ExecContext(call, q)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// transact retries only errors that are known to roll back the transaction.
// A COMMIT transport failure has an unknown outcome and must be recovered by the
// client's same request_id, never by blindly executing a second transaction.
func (s *Store) transact(ctx context.Context, fn func(context.Context, *sql.Tx) (error, error)) error {
	call, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := s.db.BeginTx(call, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		domain, err := fn(call, tx)
		if err != nil {
			_ = tx.Rollback()
			var e *mysql.MySQLError
			if attempt < 2 && errors.As(err, &e) && (e.Number == 1213 || e.Number == 1205) {
				select {
				case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
					continue
				case <-call.Done():
					return call.Err()
				}
			}
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return domain
	}
	return errors.New("transaction retries exhausted")
}

func (s *Store) Create(ctx context.Context, row model.Session, refresh []byte, now time.Time) error {
	if !secure.UUIDPattern.MatchString(row.ID) || len(refresh) != 32 || len(row.AccessHash) != 32 || len(row.CredentialFingerprint) != 32 || len(row.DeviceHash) != 32 {
		return errors.New("invalid session material")
	}
	return s.transact(ctx, func(ctx context.Context, tx *sql.Tx) (error, error) {
		// The device row serializes only this customer's same-device logins.
		_, err := tx.ExecContext(ctx, "INSERT IGNORE INTO "+s.table("device_sessions")+" (customer_id,device_hash,updated_at) VALUES (?,?,?)", row.CustomerID, row.DeviceHash, now)
		if err != nil {
			return nil, err
		}
		var old sql.NullString
		err = tx.QueryRowContext(ctx, "SELECT session_id FROM "+s.table("device_sessions")+" WHERE customer_id=? AND device_hash=? FOR UPDATE", row.CustomerID, row.DeviceHash).Scan(&old)
		if err != nil {
			return nil, err
		}
		if old.Valid {
			if _, err = tx.ExecContext(ctx, "UPDATE "+s.table("sessions")+" SET revoked_at=?,updated_at=? WHERE id=? AND revoked_at IS NULL", now, now, old.String); err != nil {
				return nil, err
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+s.table("sessions")+" (id,customer_id,device_hash,device_name,credential_fingerprint,access_hash,access_expires_at,idle_expires_at,generation,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,0,?,?)", row.ID, row.CustomerID, row.DeviceHash, row.DeviceName, row.CredentialFingerprint, row.AccessHash, row.AccessExpiresAt, row.IdleExpiresAt, now, now)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+s.table("refresh_tokens")+" (token_hash,session_id,generation,created_at) VALUES (?,?,0,?)", refresh, row.ID, now)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+s.table("device_sessions")+" SET session_id=?,updated_at=? WHERE customer_id=? AND device_hash=?", row.ID, now, row.CustomerID, row.DeviceHash)
		return nil, err
	})
}

func columns(alias string) string {
	items := []string{"id", "customer_id", "device_hash", "device_name", "credential_fingerprint", "access_hash", "access_expires_at", "idle_expires_at", "generation", "revoked_at"}
	for i := range items {
		items[i] = alias + items[i]
	}
	return strings.Join(items, ",")
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (model.Session, error) {
	var s model.Session
	var revoked sql.NullTime
	err := row.Scan(&s.ID, &s.CustomerID, &s.DeviceHash, &s.DeviceName, &s.CredentialFingerprint, &s.AccessHash, &s.AccessExpiresAt, &s.IdleExpiresAt, &s.Generation, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return s, model.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.AccessExpiresAt = s.AccessExpiresAt.UTC()
	s.IdleExpiresAt = s.IdleExpiresAt.UTC()
	if revoked.Valid {
		v := revoked.Time.UTC()
		s.RevokedAt = &v
	}
	return s, nil
}
func (s *Store) Access(ctx context.Context, hash []byte) (model.Session, error) {
	call, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return scan(s.db.QueryRowContext(call, "SELECT "+columns("")+" FROM "+s.table("sessions")+" WHERE access_hash=?", hash))
}
func (s *Store) RefreshOwner(ctx context.Context, hash []byte) (model.Session, error) {
	call, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return scan(s.db.QueryRowContext(call, "SELECT "+columns("s.")+" FROM "+s.table("sessions")+" s JOIN "+s.table("refresh_tokens")+" r ON r.session_id=s.id WHERE r.token_hash=?", hash))
}

func Valid(row model.Session, now time.Time) error {
	if row.RevokedAt != nil {
		return model.Fail("session_revoked", 401, "设备会话已失效")
	}
	if !row.IdleExpiresAt.After(now) {
		return model.Fail("session_expired", 401, "设备会话已过期")
	}
	return nil
}

func (s *Store) Rotate(ctx context.Context, oldHash, requestHash []byte, r model.Rotation) (model.Tokens, error) {
	var result model.Tokens
	if len(oldHash) != 32 || len(requestHash) != 32 {
		return result, model.Fail("invalid_refresh_token", 401, "刷新凭据无效")
	}
	err := s.transact(ctx, func(ctx context.Context, tx *sql.Tx) (error, error) {
		row, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns("")+" FROM "+s.table("sessions")+" WHERE id=? FOR UPDATE", r.Session.ID))
		if errors.Is(err, model.ErrNotFound) {
			return model.Fail("session_revoked", 401, "设备会话已失效"), nil
		}
		if err != nil {
			return nil, err
		}
		if err = Valid(row, r.Now); err != nil {
			return err, nil
		}
		revoke := func(domain error) (error, error) {
			_, e := tx.ExecContext(ctx, "UPDATE "+s.table("sessions")+" SET revoked_at=?,updated_at=? WHERE id=?", r.Now, r.Now, row.ID)
			return domain, e
		}
		if row.CustomerID != r.Session.CustomerID || subtle.ConstantTimeCompare(row.CredentialFingerprint, r.Session.CredentialFingerprint) != 1 {
			return revoke(model.Fail("credentials_changed", 401, "账号凭据已变更，请重新登录"))
		}
		if r.Tokens.Customer.Status != 1 {
			return revoke(model.Fail("account_disabled", 403, "账号已被禁用"))
		}
		var owner string
		var generation int64
		var used, retryExpires sql.NullTime
		var previousRequest, ciphertext []byte
		err = tx.QueryRowContext(ctx, "SELECT session_id,generation,used_at,request_hash,retry_cipher,retry_expires_at FROM "+s.table("refresh_tokens")+" WHERE token_hash=? FOR UPDATE", oldHash).Scan(&owner, &generation, &used, &previousRequest, &ciphertext, &retryExpires)
		if errors.Is(err, sql.ErrNoRows) || owner != "" && owner != row.ID {
			return model.Fail("invalid_refresh_token", 401, "刷新凭据无效"), nil
		}
		if err != nil {
			return nil, err
		}
		aad := append(append([]byte(row.ID), oldHash...), requestHash...)
		if used.Valid {
			if subtle.ConstantTimeCompare(previousRequest, requestHash) != 1 {
				return revoke(model.Fail("refresh_reuse_detected", 401, "刷新凭据重复使用，请重新登录"))
			}
			if row.Generation != generation+1 {
				return model.Fail("stale_refresh", 409, "该续期请求已有更新的结果"), nil
			}
			if !retryExpires.Valid || !retryExpires.Time.After(r.Now) || len(ciphertext) == 0 {
				return model.Fail("refresh_retry_expired", 409, "续期结果恢复窗口已过期"), nil
			}
			plain, e := secure.Open(s.cfg.Key, ciphertext, aad)
			if e != nil {
				return nil, e
			}
			if e = json.Unmarshal(plain, &result); e != nil {
				return nil, e
			}
			return nil, nil
		}
		if generation != row.Generation {
			return revoke(model.Fail("refresh_reuse_detected", 401, "刷新凭据状态不一致"))
		}
		plain, err := json.Marshal(r.Tokens)
		if err != nil {
			return nil, err
		}
		ciphertext, err = secure.Seal(s.cfg.Key, plain, aad)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+s.table("sessions")+" SET access_hash=?,access_expires_at=?,idle_expires_at=?,generation=generation+1,updated_at=? WHERE id=?", r.Session.AccessHash, r.Session.AccessExpiresAt, r.Session.IdleExpiresAt, r.Now, row.ID)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO "+s.table("refresh_tokens")+" (token_hash,session_id,generation,created_at) VALUES (?,?,?,?)", r.RefreshHash, row.ID, row.Generation+1, r.Now)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, "UPDATE "+s.table("refresh_tokens")+" SET used_at=?,request_hash=?,retry_cipher=?,retry_expires_at=? WHERE token_hash=?", r.Now, requestHash, ciphertext, r.RetryExpiresAt, oldHash)
		if err != nil {
			return nil, err
		}
		result = r.Tokens
		return nil, nil
	})
	return result, err
}

func (s *Store) Revoke(ctx context.Context, id string) error {
	call, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	_, err := s.db.ExecContext(call, "UPDATE "+s.table("sessions")+" SET revoked_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND revoked_at IS NULL", id)
	return err
}

func (s *Store) Prune(ctx context.Context, now time.Time) error {
	return s.transact(ctx, func(ctx context.Context, tx *sql.Tx) (error, error) {
		if _, err := tx.ExecContext(ctx, "UPDATE "+s.table("refresh_tokens")+" SET retry_cipher=NULL WHERE retry_expires_at<=? AND retry_cipher IS NOT NULL LIMIT 1000", now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("refresh_tokens")+" WHERE used_at<? AND retry_cipher IS NULL LIMIT 1000", now.Add(-180*24*time.Hour)); err != nil {
			return nil, err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM "+s.table("sessions")+" WHERE idle_expires_at<? OR revoked_at<? LIMIT 1000", now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour))
		return nil, err
	})
}
