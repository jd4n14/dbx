// Package sqlite opens SQLite databases for offline integration tests.
//
// This connector is intentionally scoped to tests:
//
//   - It registers the CGo-free modernc.org/sqlite driver under name "sqlite".
//   - It only consumes Connection.DSN; host/port/user/password/database are
//     treated as forbidden in config and never read here.
//   - It implements the internal/db.DB interface so query.Run can drive it the
//     same way it drives MySQL.
//
// SQLite is NOT a documented production engine in dbx: DDL (SHOW CREATE TABLE)
// and MySQL-specific semantics stay in internal/mysql. Use this connector only
// for portable, deterministic round-trip tests (see Plan 006).
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	// Pure-Go SQLite driver (registers itself as "sqlite").
	_ "modernc.org/sqlite"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
)

// Open opens a SQLite database using the driver DSN stored in conn.DSN,
// applies CLI pool hygiene (single connection), pings with the supplied
// context, and returns an adapter that satisfies db.DB.
//
// The connection DSN is required; other Connection fields are intentionally
// not consulted. For shared in-memory fixtures use a URI with
// mode=memory&cache=shared (e.g. file:dbx_test?mode=memory&cache=shared) and
// keep the seeding handle open across calls.
func Open(ctx context.Context, conn config.Connection) (db.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("sqlite open: nil context")
	}
	dsn := conn.DSN
	if dsn == "" {
		return nil, fmt.Errorf("sqlite open: connection %q: dsn is required", conn.Name)
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		// Do not include the DSN in the error: it can carry query parameters
		// or paths that the caller did not intend to surface.
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// CLI hygiene: tests run one statement at a time.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &database{db: sqlDB}, nil
}

// database adapts *sql.DB to db.DB. *sql.Rows already satisfies db.Rows.
type database struct {
	db *sql.DB
}

func (d *database) QueryContext(ctx context.Context, query string, args ...any) (db.Rows, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *database) PingContext(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

func (d *database) Close() error {
	return d.db.Close()
}
