package mysql

import (
	"context"
	"database/sql"
	"fmt"

	// Register the MySQL driver with database/sql.
	_ "github.com/go-sql-driver/mysql"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
)

// Open builds a force-safe DSN, opens a MySQL pool, applies CLI pool hygiene,
// and pings. On ping failure the pool is closed.
//
// Pool hygiene (CLI single-shot usage):
//
//	SetMaxOpenConns(1)
//	SetMaxIdleConns(1)
//
// Caller must Close the returned DB.
func Open(ctx context.Context, conn config.Connection) (db.DB, error) {
	dsn, err := BuildDSN(conn)
	if err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return &database{db: sqlDB}, nil
}

// database adapts *sql.DB to db.DB (QueryContext return type conversion).
type database struct {
	db *sql.DB
}

func (d *database) QueryContext(ctx context.Context, query string, args ...any) (db.Rows, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	// *sql.Rows satisfies db.Rows.
	return rows, nil
}

func (d *database) PingContext(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

func (d *database) Close() error {
	return d.db.Close()
}
