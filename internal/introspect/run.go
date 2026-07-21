package introspect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/ddl"
	"github.com/jd4n14/dbx/internal/mysql"
)

// DefaultTimeout is the budget for SHOW TABLES / SHOW COLUMNS execution.
const DefaultTimeout = 30 * time.Second

// ListTablesConnection validates identifiers (defence in depth against any
// caller that bypasses the CLI's flag validation), opens the connection,
// applies the per-call timeout, and delegates to ListTables.
func ListTablesConnection(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
	schema = strings.TrimSpace(schema)
	if schema != "" {
		if err := ValidateLikeOrSchema(schema); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(like) != "" {
		if err := ValidateLikeOrSchema(like); err != nil {
			return nil, err
		}
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	queryCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return ListTables(queryCtx, database, schema, like)
}

// ListColumnsConnection validates the table name (matching ddl convention
// for the wording) and the LIKE identifier (when supplied), opens the
// connection, applies the per-call timeout, and delegates to ListColumns.
func ListColumnsConnection(ctx context.Context, conn config.Connection, table, like string) ([]Column, error) {
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}
	if strings.TrimSpace(like) != "" {
		if err := ValidateLikeOrSchema(like); err != nil {
			return nil, err
		}
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	queryCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return ListColumns(queryCtx, database, table, like)
}

// ListIndexesConnection validates the table name, opens the connection,
// applies the per-call timeout, and delegates to ListIndexes. Used by
// `dbx indexes` (Plan 012).
func ListIndexesConnection(ctx context.Context, conn config.Connection, table string) ([]Index, error) {
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	queryCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return ListIndexes(queryCtx, database, table)
}

// ListForeignKeysConnection validates the table name, opens the
// connection, applies the per-call timeout, and delegates to
// ListForeignKeys. Used by `dbx fk` (Plan 012).
func ListForeignKeysConnection(ctx context.Context, conn config.Connection, table string) ([]ForeignKey, error) {
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	queryCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return ListForeignKeys(queryCtx, database, table)
}

// TableSizeConnection validates the table name, opens the connection,
// applies the per-call timeout, and delegates to GetTableSize. Used by
// `dbx table-size` (Plan 012).
func TableSizeConnection(ctx context.Context, conn config.Connection, table string) (TableSize, error) {
	if err := ddl.ValidateTableName(table); err != nil {
		return TableSize{}, err
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return TableSize{}, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	queryCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return GetTableSize(queryCtx, database, table)
}
