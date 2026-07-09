// Package mysql builds force-safe MySQL DSNs and opens connections.
//
// DSN safety pipeline (field-built and raw dsn — plan §4.1.A):
//
//	base (fields via Config, or raw string)
//	  → mysql.ParseDSN / NewConfig
//	  → force MultiStatements=false, ParseTime=true, Loc=UTC, timeouts
//	  → FormatDSN()
//	  → sql.Open
//
// Never pass an unchecked raw DSN string to sql.Open.
//
// Forced parameters:
//
//	multiStatements = false   (cannot be re-enabled via raw DSN)
//	parseTime       = true    (time.Time scan + RFC3339 JSON)
//	loc             = UTC     (README …Z timestamps)
//	timeout         = 10s     (dial)
//	readTimeout     = 30s
//	writeTimeout    = 30s
//
// Field-built connections also set charset=utf8mb4. Raw DSN charset is left
// as provided (unless empty after parse).
package mysql

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/jd4n14/dbx/internal/config"
)

// Forced dial / I/O timeouts (MVP predictability; plan §4.1.A).
const (
	DialTimeout  = 10 * time.Second
	ReadTimeout  = 30 * time.Second
	WriteTimeout = 30 * time.Second
)

// BuildDSN produces a final force-safe MySQL DSN from a resolved connection.
//
// If conn.DSN is non-empty it is parsed as the base; otherwise host/port/user/
// password/database fields are used. Safety params are always forced afterward.
// Invalid raw DSN returns a clear error (no passthrough to sql.Open).
func BuildDSN(conn config.Connection) (string, error) {
	cfg, err := configFromConnection(conn)
	if err != nil {
		return "", err
	}
	forceSafe(cfg)
	return cfg.FormatDSN(), nil
}

// configFromConnection builds a driver Config from fields or raw DSN without
// applying force-safe overrides (caller must forceSafe before FormatDSN).
func configFromConnection(conn config.Connection) (*mysql.Config, error) {
	raw := strings.TrimSpace(conn.DSN)
	if raw != "" {
		cfg, err := mysql.ParseDSN(raw)
		if err != nil {
			name := conn.Name
			if name == "" {
				return nil, fmt.Errorf("invalid dsn: %w", err)
			}
			return nil, fmt.Errorf("connection %q: invalid dsn: %w", name, err)
		}
		return cfg, nil
	}

	cfg := mysql.NewConfig()
	cfg.User = conn.User
	cfg.Passwd = conn.Password
	cfg.Net = "tcp"
	port := conn.Port
	if port == 0 {
		port = config.DefaultMySQLPort
	}
	cfg.Addr = fmt.Sprintf("%s:%d", conn.Host, port)
	cfg.DBName = conn.Database
	// Prefer utf8mb4 for field-built connections.
	if err := cfg.Apply(mysql.Charset("utf8mb4", "")); err != nil {
		return nil, fmt.Errorf("connection %q: set charset: %w", conn.Name, err)
	}
	return cfg, nil
}

// forceSafe mutates cfg in place with the authoritative safety policy.
// Always overwrites multiStatements, parseTime, loc, and timeouts — even when
// a raw DSN attempted to set other values (e.g. multiStatements=true).
func forceSafe(cfg *mysql.Config) {
	cfg.MultiStatements = false
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = DialTimeout
	cfg.ReadTimeout = ReadTimeout
	cfg.WriteTimeout = WriteTimeout
}
