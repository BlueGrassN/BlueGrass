package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"path"
	"strconv"
	"time"

	"bluegrass/client-api/internal/model"
)

type Config struct {
	MaxInflight       int
	RequestTimeout    time.Duration
	RequireHTTPS      bool
	TrustedProxyCIDRs []string
	LoginPerMinute    int
	RefreshPerMinute  int
	ReadPerMinute     int
	MaxRateKeys       int
}

const apiPrefix = "/api/client/v1"

type apiHandler struct {
	auth         model.Authenticator
	source       model.Source
	mux          *http.ServeMux
	slots        chan struct{}
	timeout      time.Duration
	requireHTTPS bool
	trusted      []netip.Prefix
	loginLimit   *rateLimiter
	refreshLimit *rateLimiter
	readLimit    *rateLimiter
}

type endpoint func(*http.Request) (any, error)

// New constructs a handler without opening listeners or starting goroutines.
// Zero numeric settings select bounded defaults; proxy trust is explicit.
func New(auth model.Authenticator, source model.Source, cfg Config) (http.Handler, error) {
	if auth == nil || source == nil {
		return nil, errors.New("httpapi: authenticator and source are required")
	}
	if cfg.MaxInflight < 0 || cfg.RequestTimeout < 0 || cfg.LoginPerMinute < 0 || cfg.RefreshPerMinute < 0 || cfg.ReadPerMinute < 0 || cfg.MaxRateKeys < 0 || cfg.MaxRateKeys > 10000 {
		return nil, errors.New("httpapi: invalid request or rate limit configuration")
	}
	if cfg.MaxInflight == 0 {
		cfg.MaxInflight = 64
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	if cfg.LoginPerMinute == 0 {
		cfg.LoginPerMinute = 120
	}
	if cfg.RefreshPerMinute == 0 {
		cfg.RefreshPerMinute = 240
	}
	if cfg.ReadPerMinute == 0 {
		cfg.ReadPerMinute = 120
	}
	if cfg.MaxRateKeys == 0 {
		cfg.MaxRateKeys = 10000
	}
	trusted := make([]netip.Prefix, 0, len(cfg.TrustedProxyCIDRs))
	for _, cidr := range cfg.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, errors.New("httpapi: invalid trusted proxy CIDR")
		}
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		trusted = append(trusted, prefix.Masked())
	}
	h := &apiHandler{
		auth: auth, source: source, mux: http.NewServeMux(),
		slots: make(chan struct{}, cfg.MaxInflight), timeout: cfg.RequestTimeout,
		requireHTTPS: cfg.RequireHTTPS, trusted: trusted,
		loginLimit:   newRateLimiter(cfg.LoginPerMinute, cfg.MaxRateKeys),
		refreshLimit: newRateLimiter(cfg.RefreshPerMinute, cfg.MaxRateKeys),
		readLimit:    newRateLimiter(cfg.ReadPerMinute, cfg.MaxRateKeys),
	}
	h.route("GET", "/health", h.respond(func(*http.Request) (any, error) {
		return struct {
			Status string `json:"status"`
		}{Status: "ok"}, nil
	}, nil))
	h.route("POST", "/auth/login", h.respond(h.login, h.loginLimit))
	h.route("POST", "/auth/refresh", h.respond(h.refresh, h.refreshLimit))
	h.route("GET", "/me", h.respond(h.protected(h.read(h.me)), nil))
	h.route("POST", "/auth/logout", h.respond(h.protected(h.logout), nil))
	h.route("GET", "/nodes", h.respond(h.protected(h.read(h.nodes)), nil))
	h.route("GET", "/nodes/{id}", h.respond(h.protected(h.read(h.node)), nil))
	h.route("GET", "/capabilities", h.respond(h.protected(h.read(h.capabilities)), nil))
	h.route("GET", "/resources", h.respond(h.protected(h.read(h.resources)), nil))
	h.route("GET", "/resources/expiry", h.respond(h.protected(h.read(h.resources)), nil))
	h.route("GET", "/resources/{kind}/{id}", h.respond(h.protected(h.read(h.resource)), nil))
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, http.StatusNotFound, "not_found")
	})
	return h, nil
}

func (h *apiHandler) route(method, path string, handler http.HandlerFunc) {
	h.mux.HandleFunc(method+" "+apiPrefix+path, handler)
	// A methodless fallback preserves JSON errors and Allow while the more
	// specific method pattern remains the sole entry to the endpoint.
	h.mux.HandleFunc(apiPrefix+path, func(w http.ResponseWriter, r *http.Request) {
		allow := method
		if method == http.MethodGet {
			allow = "GET, HEAD"
		}
		w.Header().Set("Allow", allow)
		writeFailure(w, http.StatusMethodNotAllowed, "method_not_allowed")
	})
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w = headWriter{ResponseWriter: w}
	}
	if h.requireHTTPS && !h.isHTTPS(r) {
		writeFailure(w, http.StatusForbidden, "https_required")
		return
	}
	health := r.URL.Path == apiPrefix+"/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead)
	if !health {
		select {
		case h.slots <- struct{}{}:
			defer func() { <-h.slots }()
		default:
			writeFailure(w, http.StatusServiceUnavailable, "service_busy")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		writeError(w, err)
		return
	}
	// ServeMux redirects unclean paths using the original query string. Reject
	// them with a JSON error so credentials in an invalid URL are never echoed.
	clean := path.Clean(r.URL.Path)
	if r.URL.Path != clean && r.URL.Path != clean+"/" {
		writeFailure(w, http.StatusUnprocessableEntity, "validation_failed")
		return
	}
	h.mux.ServeHTTP(w, r.WithContext(ctx))
}

func (h *apiHandler) respond(run endpoint, limiter *rateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if limiter != nil {
			if allowed, retry := limiter.allow(h.clientIP(r).String()); !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				writeFailure(w, http.StatusTooManyRequests, "rate_limited")
				return
			}
		}
		data, err := run(r)
		if canceled := r.Context().Err(); canceled != nil {
			err = canceled
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

type headWriter struct{ http.ResponseWriter }

func (w headWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
