package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/jsonutil"
	"github.com/jd4n14/dbx/internal/mysql"
)

// DefaultQueryTimeout is the default budget for a single query execution
// when the caller context has no earlier deadline.
const DefaultQueryTimeout = 30 * time.Second

// Run validates SQL, executes it via the injectable DB, scans all rows, and
// returns pretty-printed JSON (2-space indent + trailing newline).
//
// ValidateQuery runs before QueryContext (write barrier). No Open inside —
// pass a real mysql.Open result or a test fake.
//
// Errors are wrapped with context prefixes: query:, scan:, convert:.
func Run(ctx context.Context, database db.DB, sqlText string) ([]byte, error) {
	if err := ValidateQuery(sqlText); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, err := database.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	columns, values, err := scanAll(rows)
	if err != nil {
		return nil, err
	}

	out, err := jsonutil.RowsToPrettyJSON(columns, values)
	if err != nil {
		return nil, fmt.Errorf("convert: %w", err)
	}
	return out, nil
}

// RunConnection validates SQL first (before any network), opens a MySQL
// connection, runs the query, and closes the DB.
//
// Policy-before-Open is intentional so disallowed SQL never touches the server.
func RunConnection(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error) {
	if err := ValidateQuery(sqlText); err != nil {
		return nil, err
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	return Run(ctx, database, sqlText)
}

// scanAll reads every row into [][]any suitable for jsonutil.
//
// Scan holders are *any so the driver (or fake) can populate concrete types
// such as []byte, time.Time, int64, nil — matching parseTime=true MySQL scans.
func scanAll(rows db.Rows) (columns []string, values [][]any, err error) {
	columns, err = rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("scan: columns: %w", err)
	}

	n := len(columns)
	values = make([][]any, 0)

	for rows.Next() {
		// Per-row holders: Scan writes into *any pointers.
		holders := make([]any, n)
		dest := make([]any, n)
		for i := range holders {
			dest[i] = &holders[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("scan: %w", err)
		}
		// Copy cell values (holders may be reused only within this iteration).
		row := make([]any, n)
		copy(row, holders)
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	return columns, values, nil
}
