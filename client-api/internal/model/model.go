package model

import (
	"context"
	"errors"
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
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Protocol    string     `json:"protocol"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	CountryCode string     `json:"country_code"`
	CountryName string     `json:"country_name"`
	City        string     `json:"city"`
	ExpiresAt   *time.Time `json:"expires_at"`
	IsForwarded bool       `json:"is_forwarded"`
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
