package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"bluegrass/client-api/internal/model"
)

func (h *apiHandler) login(r *http.Request) (any, error) {
	var input model.LoginInput
	if err := decodeBody(r, &input); err != nil {
		return nil, err
	}
	if !validLogin(input) {
		return nil, invalidInput()
	}
	return h.auth.Login(r.Context(), input)
}

func (h *apiHandler) refresh(r *http.Request) (any, error) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
		RequestID    string `json:"request_id"`
	}
	if err := decodeBody(r, &input); err != nil {
		return nil, err
	}
	if !validToken(input.RefreshToken) || !validUUID(input.RequestID) {
		return nil, invalidInput()
	}
	return h.auth.Refresh(r.Context(), input.RefreshToken, input.RequestID)
}

func (h *apiHandler) protected(run func(*http.Request, model.Identity) (any, error)) endpoint {
	return func(r *http.Request) (any, error) {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) > 4096 {
			return nil, model.Fail("unauthorized", http.StatusUnauthorized, "Unauthorized")
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validToken(parts[1]) {
			return nil, model.Fail("unauthorized", http.StatusUnauthorized, "Unauthorized")
		}
		identity, err := h.auth.Authenticate(r.Context(), parts[1])
		if err != nil {
			return nil, err
		}
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		if identity.Customer.ID <= 0 {
			return nil, model.Fail("unauthorized", http.StatusUnauthorized, "Unauthorized")
		}
		return run(r, identity)
	}
}

func (h *apiHandler) read(run func(*http.Request, model.Identity) (any, error)) func(*http.Request, model.Identity) (any, error) {
	return func(r *http.Request, identity model.Identity) (any, error) {
		if identity.Session.ID == "" {
			return nil, model.Fail("unauthorized", http.StatusUnauthorized, "Unauthorized")
		}
		if allowed, _ := h.readLimit.allow(identity.Session.ID); !allowed {
			return nil, model.Fail("rate_limited", http.StatusTooManyRequests, "Too many requests")
		}
		return run(r, identity)
	}
}

func (h *apiHandler) me(r *http.Request, identity model.Identity) (any, error) {
	// Authenticate already loaded and checked the current customer record.
	return identity.Customer, nil
}

func (h *apiHandler) logout(r *http.Request, identity model.Identity) (any, error) {
	if identity.Session.ID == "" {
		return nil, model.Fail("unauthorized", http.StatusUnauthorized, "Unauthorized")
	}
	if err := h.auth.Logout(r.Context(), identity.Session.ID); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

func (h *apiHandler) nodes(r *http.Request, identity model.Identity) (any, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, invalidInput()
	}
	if page, exists := query["page"]; exists && (len(page) != 1 || page[0] != "1") {
		return nil, invalidInput()
	}
	limit := 100
	if values, exists := query["per_page"]; exists {
		if len(values) != 1 {
			return nil, invalidInput()
		}
		perPage, ok := positiveID(values[0])
		if !ok || perPage > 100 {
			return nil, invalidInput()
		}
		limit = int(perPage)
	}
	var afterID int64
	if values, exists := query["cursor"]; exists {
		if len(values) != 1 {
			return nil, invalidInput()
		}
		afterID, err = parseCursor(values[0])
		if err != nil {
			return nil, err
		}
	}
	return h.source.Nodes(r.Context(), identity.Customer.ID, afterID, limit)
}

func (h *apiHandler) node(r *http.Request, identity model.Identity) (any, error) {
	id, ok := positiveID(r.PathValue("id"))
	if !ok {
		return nil, invalidInput()
	}
	return h.source.Node(r.Context(), identity.Customer.ID, id)
}

func parseCursor(cursor string) (int64, error) {
	// The largest signed int64 has 19 decimal digits, or 26 raw base64url bytes.
	if len(cursor) == 0 || len(cursor) > 26 {
		return 0, invalidInput()
	}
	for _, c := range cursor {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return 0, invalidInput()
		}
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return 0, invalidInput()
	}
	id, ok := positiveID(string(decoded))
	if !ok {
		return 0, invalidInput()
	}
	return id, nil
}

func positiveID(value string) (int64, bool) {
	if len(value) == 0 || len(value) > 19 {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}
