package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/jsonutil"
	"github.com/jd4n14/dbx/internal/mysql"
	"github.com/jd4n14/dbx/internal/sqlite"
)

// DefaultQueryTimeout is the default budget for a single query execution
// when the caller context has no earlier deadline.
const DefaultQueryTimeout = 30 * time.Second

// queryEnvelopeType is the on-disk `type` marker emitted by RunWithLimit when
// a row limit is applied. It is intentionally distinct from snapshot /
// last_result so consumers can branch on the shape.
const queryEnvelopeType = "query"

// RunResult is the structured output of RunWithLimit. The Data field is the
// JSON payload that should reach the caller verbatim — a bare pretty array
// when MaxRows == 0 (the legacy / unlimited contract) or a pretty envelope
// containing the array when MaxRows > 0.
//
// Truncated and RowCount are only meaningful when MaxRows > 0. When
// MaxRows == 0 they are zero-valued.
type RunResult struct {
	Data      []byte // JSON payload (bare array or envelope) ready for stdout
	Truncated bool   // true iff more rows were available than MaxRows
	RowCount  int    // length of the kept data array (<= MaxRows)
	MaxRows   int    // requested cap (0 = unlimited)
}

// Run validates SQL, executes it via the injectable DB, scans all rows, and
// returns pretty-printed JSON (2-space indent + trailing newline).
//
// ValidateQuery runs before QueryContext (write barrier). No Open inside —
// pass a real mysql.Open result or a test fake.
//
// Errors are wrapped with context prefixes: query:, scan:, convert:.
//
// This entry point is unlimited: it preserves the byte-for-byte contract
// every consumer (snapshot diff, Neovim render, .dbx/last.json) was built
// against. Callers that want a row cap should use RunWithLimit instead.
func Run(ctx context.Context, database db.DB, sqlText string) ([]byte, error) {
	res, err := RunWithLimit(ctx, database, sqlText, 0)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// RunWithLimit behaves like Run but caps the returned row count to maxRows.
//
// When maxRows == 0 the result is byte-for-byte identical to Run's output:
// a pretty JSON array with no envelope wrapper, no metadata, no
// transformation of the existing consumer contract.
//
// When maxRows > 0 the SQL is rewritten to add ` LIMIT maxRows+1` after
// stripping a trailing `;`; the +1 lets the executor detect truncation
// without losing row N. The returned Data is a pretty envelope:
//
//	{"type":"query","truncated":<bool>,"row_count":<int>,
// "max_rows":<int|null>, "data":[ ... ]}
//
// MaxRows < 0 is rejected as an error before any DB call. Multi-statement
// SQL is rejected upstream by ValidateQuery, so LIMIT only lands on the
// single statement.
//
// LIMIT injection is intentionally naive: a query that already contains a
// LIMIT clause will produce a syntax error. Per Plan 011 this is acceptable
// for the v1 contract; revisit if it becomes a common case.
func RunWithLimit(ctx context.Context, database db.DB, sqlText string, maxRows int) (RunResult, error) {
	if maxRows < 0 {
		return RunResult{}, fmt.Errorf("max-rows must be > 0")
	}

	if err := ValidateQuery(sqlText); err != nil {
		return RunResult{}, err
	}

	executionSQL := sqlText
	if maxRows > 0 {
		executionSQL = strings.TrimRight(strings.TrimSpace(sqlText), ";")
		executionSQL = strings.TrimRight(executionSQL, " \t\n\r")
		executionSQL = executionSQL + fmt.Sprintf(" LIMIT %d", maxRows+1)
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultQueryTimeout)
	defer cancel()

	rows, err := database.QueryContext(ctx, executionSQL)
	if err != nil {
		return RunResult{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	columns, values, err := scanAll(rows)
	if err != nil {
		return RunResult{}, err
	}

	res := RunResult{MaxRows: maxRows}

	if maxRows == 0 {
		out, err := jsonutil.RowsToPrettyJSON(columns, values)
		if err != nil {
			return RunResult{}, fmt.Errorf("convert: %w", err)
		}
		res.Data = out
		return res, nil
	}

	truncated := len(values) > maxRows
	if truncated {
		values = values[:maxRows]
	}

	objs, err := jsonutil.RowsToObjects(columns, values)
	if err != nil {
		return RunResult{}, fmt.Errorf("convert: %w", err)
	}
	dataBytes, err := json.Marshal(objs)
	if err != nil {
		return RunResult{}, fmt.Errorf("convert: data: %w", err)
	}

	envelope := struct {
		Type     string          `json:"type"`
		Truncat  bool            `json:"truncated"`
		RowCount int             `json:"row_count"`
		MaxRows  any             `json:"max_rows"`
		Data     json.RawMessage `json:"data"`
	}{
		Type:     queryEnvelopeType,
		Truncat:  truncated,
		RowCount: len(values),
		MaxRows:  maxRows,
		Data:     dataBytes,
	}
	out, err := jsonutil.MarshalPretty(envelope)
	if err != nil {
		return RunResult{}, fmt.Errorf("convert: envelope: %w", err)
	}

	res.Data = out
	res.Truncated = truncated
	res.RowCount = len(values)
	return res, nil
}

// RunConnection validates SQL first (before any network), opens a connection
// appropriate for conn.Driver, runs the query, and closes the DB.
//
// Policy-before-Open is intentional so disallowed SQL never touches the server.
//
// The driver dispatch is intentionally minimal: each connector owns its own
// DSN rules and database/sql registration. We do not call database/sql.Open
// directly from this package; that would leak driver names into query code.
// SQLite support exists only for offline integration tests (Plan 006); the
// product remains MySQL-only for `query` (and `ddl`, which rejects non-mysql).
func RunConnection(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error) {
	res, err := RunConnectionWithLimit(ctx, conn, sqlText, 0)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// RunConnectionWithLimit mirrors RunWithLimit for the connection-opening path.
// See RunWithLimit for the envelope contract and the maxRows semantics.
func RunConnectionWithLimit(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (RunResult, error) {
	if maxRows < 0 {
		return RunResult{}, fmt.Errorf("max-rows must be > 0")
	}

	if err := ValidateQuery(sqlText); err != nil {
		return RunResult{}, err
	}

	database, err := openConnection(ctx, conn)
	if err != nil {
		return RunResult{}, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	return RunWithLimit(ctx, database, sqlText, maxRows)
}

// openConnection selects the connector based on the normalized Driver field.
// config.Connection already restricts Driver to mysql or sqlite, so an
// unknown value here means the caller bypassed validation (possible from
// direct test usage of RunConnection).
func openConnection(ctx context.Context, conn config.Connection) (db.DB, error) {
	switch conn.Driver {
	case "", "mysql":
		return mysql.Open(ctx, conn)
	case "sqlite":
		return sqlite.Open(ctx, conn)
	default:
		return nil, fmt.Errorf("unsupported driver %q", conn.Driver)
	}
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
