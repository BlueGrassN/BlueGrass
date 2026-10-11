package dbpool

import (
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"net"
	"strconv"
	"time"
)

type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	TLS      string `json:"tls"`
	MaxOpen  int    `json:"max_open"`
	MaxIdle  int    `json:"max_idle"`
}

func Open(c Config, readOnly bool, location *time.Location) (*sql.DB, error) {
	if c.Host == "" || c.Port < 1 || c.Port > 65535 || c.Database == "" || c.User == "" || c.MaxOpen < 1 || c.MaxOpen > 128 || c.MaxIdle < 0 || c.MaxIdle > c.MaxOpen || location == nil {
		return nil, errors.New("invalid database connection or pool budget")
	}
	if c.TLS != "" && c.TLS != "false" && c.TLS != "true" {
		return nil, errors.New("TLS must be false for loopback or true with certificate verification")
	}
	if c.TLS != "true" {
		ip := net.ParseIP(c.Host)
		if c.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("remote database connections require verified TLS")
		}
	}
	cfg := mysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.DBName = c.Database
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.ParseTime = true
	cfg.Loc = location
	cfg.Timeout = 2 * time.Second
	cfg.ReadTimeout = 3 * time.Second
	cfg.WriteTimeout = 3 * time.Second
	cfg.TLSConfig = c.TLS
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.MultiStatements = false
	_, offset := time.Now().In(location).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	zone := sign + twoDigits(offset/3600) + ":" + twoDigits(offset%3600/60)
	cfg.Params = map[string]string{"charset": "utf8mb4", "time_zone": "'" + zone + "'", "transaction_isolation": "'READ-COMMITTED'"}
	if readOnly {
		cfg.Params["transaction_read_only"] = "1"
	}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("invalid MySQL configuration")
	}
	db.SetMaxOpenConns(c.MaxOpen)
	db.SetMaxIdleConns(c.MaxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
