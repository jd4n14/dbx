// Package db defines tiny database interfaces used by query orchestration.
//
// Concrete *sql.DB / *sql.Rows adapters live in internal/mysql. Hand-rolled
// fakes implement these interfaces in tests (Phase 3b) without live MySQL.
package db

import "context"

// DB is the subset of database operations needed by query execution.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	PingContext(ctx context.Context) error
	Close() error
}

// Rows is the subset of row-iteration operations needed by scan/convert.
type Rows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}
