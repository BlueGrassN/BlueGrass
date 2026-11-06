package model

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

var ErrNotFound = errors.New("not found")

type Customer struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Phone        string `json:"-"`
	PasswordHash string `json:"-"`
	Status       int    `json:"-"`
}

type Node struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	CustomerRemark string     `json:"customer_remark"`
	Protocol       string     `json:"protocol"`
	Host           string     `json:"host"`
	Port           int        `json:"port"`
	Username       string     `json:"username"`
	Password       string     `json:"password"`
	CountryCode    string     `json:"country_code"`
	CountryName    string     `json:"country_name"`
	City           string     `json:"city"`
	ExpiresAt      *time.Time `json:"expires_at"`
	IsForwarded    bool       `json:"is_forwarded"`
	ExpectedExitIP *string    `json:"expected_exit_ip"`
}

// Resource summaries deliberately contain no connection credentials. Current
// business evidence cannot provide a verified expected egress, so connectable
// remains false even for a subscription that can supply a legacy node DTO.
type Resource struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Name           string     `json:"name"`
	CustomerRemark string     `json:"customer_remark"`
	Status         string     `json:"status"`
	Owned          bool       `json:"owned"`
	Connectable    bool       `json:"connectable"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

type ResourceCoverage struct {
	Kind             string `json:"kind"`
	Consistency      string `json:"consistency"`
	Ownership        string `json:"ownership"`
	SnapshotComplete bool   `json:"snapshot_complete"`
}

type ResourcePage struct {
	Items      []Resource       `json:"items"`
	Pagination Pagination       `json:"pagination"`
	Coverage   ResourceCoverage `json:"coverage"`
}

// Optional extension keeps existing Source implementations source compatible.
type ResourceSource interface {
	Resources(context.Context, int64, string, int64, int) (ResourcePage, error)
	Resource(context.Context, int64, string, int64) (Resource, error)
}

type Pagination struct {
	PerPage    int    `json:"per_page"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type NodePage struct {
	Items      []Node     `json:"items"`
	Pagination Pagination `json:"pagination"`
}

type Source interface {
	CustomerByLogin(context.Context, string) (Customer, error)
	CustomerByID(context.Context, int64) (Customer, error)
	Nodes(context.Context, int64, int64, int) (NodePage, error)
	Node(context.Context, int64, int64) (Node, error)
}

type APIError struct {
	Code    string
	Status  int
	Message string
}

func (e *APIError) Error() string                        { return e.Code }
func Fail(code string, status int, message string) error { return &APIError{code, status, message} }

type Tokens struct {
	TokenType        string    `json:"token_type"`
	AccessToken      string    `json:"access_token"`
	ExpiresIn        int       `json:"expires_in"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	SessionID        string    `json:"session_id"`
	Customer         Customer  `json:"customer"`
}

type Session struct {
	ID                    string
	CustomerID            int64
	DeviceHash            []byte
	DeviceName            string
	LoginIP               string `json:"-"`
	CredentialFingerprint []byte
	AccessHash            []byte
	AccessExpiresAt       time.Time
	IdleExpiresAt         time.Time
	Generation            int64
	RevokedAt             *time.Time
}

type Rotation struct {
	Tokens         Tokens
	Session        Session
	RefreshHash    []byte
	Now            time.Time
	RetryExpiresAt time.Time
}

type LoginInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name,omitempty"`
	// Set by the HTTP boundary, never accepted from request JSON.
	ClientIP netip.Addr `json:"-"`
}
type Identity struct {
	Session  Session
	Customer Customer
}
type Authenticator interface {
	Login(context.Context, LoginInput) (Tokens, error)
	Refresh(context.Context, string, string) (Tokens, error)
	Authenticate(context.Context, string) (Identity, error)
	Logout(context.Context, string) error
}
