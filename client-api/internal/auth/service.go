package auth

import (
	"bluegrass/client-api/internal/model"
	"bluegrass/client-api/internal/secure"
	"bluegrass/client-api/internal/session"
	"context"
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Key         []byte
	AccessTTL   time.Duration
	RefreshIdle time.Duration
	RetryWindow time.Duration
	HashWait    time.Duration
	HashWorkers int
	Now         func() time.Time
	Verify      func(string, string) (bool, error)
}
type attempt struct {
	count int
	until time.Time
}
type Service struct {
	source    model.Source
	store     *session.Store
	cfg       Config
	hashSlots chan struct{}
	mu        sync.Mutex
	attempts  map[string]attempt
}

const dummyHash = "$2y$12$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2uheWG/igi."

func New(source model.Source, store *session.Store, cfg Config) (*Service, error) {
	if source == nil || store == nil || len(cfg.Key) != 32 || cfg.HashWorkers < 1 || cfg.HashWorkers > 32 || cfg.AccessTTL <= 0 || cfg.RefreshIdle <= cfg.AccessTTL || cfg.RetryWindow <= 0 || cfg.HashWait <= 0 {
		return nil, errors.New("invalid auth configuration")
	}
	cfg.Key = append([]byte(nil), cfg.Key...)
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Verify == nil {
		cfg.Verify = secure.VerifyPassword
	}
	return &Service{source: source, store: store, cfg: cfg, hashSlots: make(chan struct{}, cfg.HashWorkers), attempts: make(map[string]attempt)}, nil
}

func (s *Service) takeAttempt(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	a, exists := s.attempts[key]
	if exists && !now.Before(a.until) {
		delete(s.attempts, key)
		exists = false
	}
	if !exists {
		if len(s.attempts) >= 10000 {
			for k, v := range s.attempts {
				if !now.Before(v.until) {
					delete(s.attempts, k)
				}
			}
		}
		if len(s.attempts) >= 10000 {
			return false
		}
		a = attempt{until: now.Add(time.Minute)}
	}
	if a.count >= 5 {
		return false
	}
	a.count++
	s.attempts[key] = a
	return true
}
func (s *Service) clearAttempt(key string) { s.mu.Lock(); delete(s.attempts, key); s.mu.Unlock() }

func (s *Service) Login(ctx context.Context, in model.LoginInput) (model.Tokens, error) {
	var empty model.Tokens
	if in.Username == "" || len(in.Username) > 255 || in.Password == "" || len(in.Password) > 1024 || !secure.UUIDPattern.MatchString(in.DeviceID) || len([]rune(in.DeviceName)) > 120 {
		return empty, model.Fail("validation_failed", 422, "请求参数不正确")
	}
	wait, cancel := context.WithTimeout(ctx, s.cfg.HashWait)
	defer cancel()
	select {
	case s.hashSlots <- struct{}{}:
	case <-wait.Done():
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		return empty, model.Fail("service_busy", 503, "登录繁忙，请稍后重试")
	}
	held := true
	defer func() {
		if held {
			<-s.hashSlots
		}
	}()
	customer, err := s.source.CustomerByLogin(ctx, in.Username)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return empty, err
	}
	key := "id:" + strconv.FormatInt(customer.ID, 10)
	if customer.ID == 0 {
		key = "missing:" + string(secure.Digest(strings.ToLower(in.Username)))
	}
	if !s.takeAttempt(key) {
		return empty, model.Fail("rate_limited", 429, "登录尝试过于频繁")
	}
	encoded := customer.PasswordHash
	if encoded == "" {
		encoded = dummyHash
	}
	verified, err := s.cfg.Verify(encoded, in.Password)
	<-s.hashSlots
	held = false
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if !verified || customer.ID == 0 || customer.PasswordHash == "" {
		return empty, model.Fail("invalid_credentials", 401, "账号或密码错误")
	}
	if customer.Status != 1 {
		return empty, model.Fail("account_disabled", 403, "账号已被禁用")
	}
	// Another application can change the account while bcrypt/Argon2 runs.
	// Re-read current committed state by primary key, without a business-row lock.
	current, err := s.source.CustomerByID(ctx, customer.ID)
	if errors.Is(err, model.ErrNotFound) {
		return empty, model.Fail("account_unavailable", 401, "账号已不可用")
	}
	if err != nil {
		return empty, err
	}
	if current.Status != 1 {
		return empty, model.Fail("account_disabled", 403, "账号已被禁用")
	}
	if current.PasswordHash != customer.PasswordHash || current.Username != customer.Username || current.Phone != customer.Phone {
		return empty, model.Fail("credentials_changed", 401, "登录过程中账号发生变更，请重试")
	}
	s.clearAttempt(key)
	id, err := secure.UUID()
	if err != nil {
		return empty, err
	}
	r, err := s.material(id, current)
	if err != nil {
		return empty, err
	}
	r.Session.DeviceHash = secure.Digest(strings.ToLower(in.DeviceID))
	r.Session.DeviceName = in.DeviceName
	if err = s.store.Create(ctx, r.Session, r.RefreshHash, r.Now); err != nil {
		return empty, err
	}
	return r.Tokens, nil
}

func (s *Service) material(id string, c model.Customer) (model.Rotation, error) {
	now := s.cfg.Now().UTC()
	access, err := secure.Token("bga_")
	if err != nil {
		return model.Rotation{}, err
	}
	refresh, err := secure.Token("bgr_")
	if err != nil {
		return model.Rotation{}, err
	}
	row := model.Session{ID: id, CustomerID: c.ID, CredentialFingerprint: secure.Fingerprint(s.cfg.Key, c.PasswordHash), AccessHash: secure.Digest(access), AccessExpiresAt: now.Add(s.cfg.AccessTTL), IdleExpiresAt: now.Add(s.cfg.RefreshIdle)}
	tokens := model.Tokens{TokenType: "Bearer", AccessToken: access, ExpiresIn: int(s.cfg.AccessTTL.Seconds()), AccessExpiresAt: row.AccessExpiresAt, RefreshToken: refresh, RefreshExpiresAt: row.IdleExpiresAt, SessionID: id, Customer: c}
	return model.Rotation{Tokens: tokens, Session: row, RefreshHash: secure.Digest(refresh), Now: now, RetryExpiresAt: now.Add(s.cfg.RetryWindow)}, nil
}

func (s *Service) currentCustomer(ctx context.Context, row model.Session) (model.Customer, error) {
	c, err := s.source.CustomerByID(ctx, row.CustomerID)
	var denied error
	if errors.Is(err, model.ErrNotFound) {
		denied = model.Fail("account_unavailable", 401, "账号已不可用")
	} else if err != nil {
		return c, err
	} else if c.Status != 1 {
		denied = model.Fail("account_disabled", 403, "账号已被禁用")
	} else if subtle.ConstantTimeCompare(row.CredentialFingerprint, secure.Fingerprint(s.cfg.Key, c.PasswordHash)) != 1 {
		denied = model.Fail("credentials_changed", 401, "账号凭据已变更，请重新登录")
	}
	if denied != nil {
		if err = s.store.Revoke(ctx, row.ID); err != nil {
			return c, err
		}
		return c, denied
	}
	return c, nil
}

func (s *Service) Refresh(ctx context.Context, token, requestID string) (model.Tokens, error) {
	if !strings.HasPrefix(token, "bgr_") || len(token) != 68 || !secure.UUIDPattern.MatchString(requestID) {
		return model.Tokens{}, model.Fail("invalid_refresh_token", 401, "刷新凭据无效")
	}
	hash := secure.Digest(token)
	row, err := s.store.RefreshOwner(ctx, hash)
	if errors.Is(err, model.ErrNotFound) {
		return model.Tokens{}, model.Fail("invalid_refresh_token", 401, "刷新凭据无效")
	}
	if err != nil {
		return model.Tokens{}, err
	}
	if err = session.Valid(row, s.cfg.Now()); err != nil {
		return model.Tokens{}, err
	}
	// No transaction/row lock is held while reading the independent business DB.
	c, err := s.currentCustomer(ctx, row)
	if err != nil {
		return model.Tokens{}, err
	}
	material, err := s.material(row.ID, c)
	if err != nil {
		return model.Tokens{}, err
	}
	return s.store.Rotate(ctx, hash, secure.Digest(strings.ToLower(requestID)), material)
}

func (s *Service) Authenticate(ctx context.Context, token string) (model.Identity, error) {
	var empty model.Identity
	if !strings.HasPrefix(token, "bga_") || len(token) != 68 {
		return empty, model.Fail("invalid_token", 401, "登录凭据无效")
	}
	row, err := s.store.Access(ctx, secure.Digest(token))
	if errors.Is(err, model.ErrNotFound) {
		return empty, model.Fail("invalid_token", 401, "登录凭据无效")
	}
	if err != nil {
		return empty, err
	}
	if err = session.Valid(row, s.cfg.Now()); err != nil {
		return empty, err
	}
	c, err := s.currentCustomer(ctx, row)
	if err != nil {
		return empty, err
	}
	if !row.AccessExpiresAt.After(s.cfg.Now()) {
		return empty, model.Fail("access_token_expired", 401, "访问凭据已过期，请自动续期")
	}
	return model.Identity{Session: row, Customer: c}, nil
}

func (s *Service) Logout(ctx context.Context, id string) error { return s.store.Revoke(ctx, id) }
