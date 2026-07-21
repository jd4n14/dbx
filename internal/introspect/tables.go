// Package introspect provides injectable database-schema introspection
// helpers used by the dbx CLI. It mirrors the design of internal/ddl:
// validate inputs first, return early on empty results, and own nothing but
// the SQL builder. The CLI layer (cmd/dbx) is responsible for opening the
// connection and serialising the result.
package introspect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/ddl"
)

// ListTables runs `SHOW TABLES [FROM <schema>] [LIKE '<pattern>']` and returns
// the table names in the order MySQL reported them.
//
// `schema` and `like` are both optional. When supplied, they are validated
// before any database call so an invalid identifier never reaches the wire
// (and tests can assert that QueryContext is never called). `like` is scoped
// to plain identifier-shaped values (`^[A-Za-z_][A-Za-z0-9_]*$`) to avoid
// SQL injection while still letting `:DbTables ord%` filter by prefix.
func ListTables(ctx context.Context, database db.DB, schema, like string) ([]string, error) {
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

	sqlText := buildShowTablesSQL(schema, like)
	rows, err := database.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("introspect: columns: %w", err)
	}
	if len(cols) < 1 {
		return nil, fmt.Errorf("introspect: expected at least 1 column from SHOW TABLES, got %d", len(cols))
	}

	var names []string
	for rows.Next() {
		var name any
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("introspect: scan: %w", err)
		}
		s, err := cellToString(name)
		if err != nil {
			return nil, fmt.Errorf("introspect: %w", err)
		}
		if s == "" {
			continue
		}
		names = append(names, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	return names, nil
}

// buildShowTablesSQL constructs the SHOW TABLES statement. Schema (if any) is
// identifier-quoted via ddl.QuoteIdentifier; the LIKE literal is single-quoted
// after escaping internal '.
func buildShowTablesSQL(schema, like string) string {
	var b strings.Builder
	b.WriteString("SHOW TABLES")
	if schema != "" {
		b.WriteString(" FROM ")
		b.WriteString(ddl.QuoteIdentifier(schema))
	}
	if like != "" {
		b.WriteString(" LIKE ")
		b.WriteString(quoteLikeLiteral(like))
	}
	return b.String()
}

// ValidateLikeOrSchema enforces the simple identifier shape used for both
// the optional schema name and the LIKE literal. Exported so the CLI can
// validate before issuing any database call (defence in depth: production
// wrappers call into this too).
func ValidateLikeOrSchema(v string) error {
	if len(v) == 0 || len(v) > ddl.MaxTableNameLen {
		return fmt.Errorf("invalid identifier (must be ASCII letters, digits, or underscore; max %d)", ddl.MaxTableNameLen)
	}
	for i, r := range v {
		if r > 127 {
			return fmt.Errorf("invalid identifier (must be ASCII letters, digits, or underscore; max %d)", ddl.MaxTableNameLen)
		}
		c := byte(r)
		if i == 0 {
			if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
				return fmt.Errorf("invalid identifier (must be ASCII letters, digits, or underscore; max %d)", ddl.MaxTableNameLen)
			}
			continue
		}
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return fmt.Errorf("invalid identifier (must be ASCII letters, digits, or underscore; max %d)", ddl.MaxTableNameLen)
		}
	}
	return nil
}

// quoteLikeLiteral wraps a LIKE pattern in single quotes after escaping any
// internal '. Use only with inputs already accepted by validateLikeOrSchema.
func quoteLikeLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// cellToString converts a scanned DB cell into a string. NULL becomes "".
func cellToString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	default:
		return "", fmt.Errorf("expected string-compatible cell, got %T", v)
	}
}

// cellToBool converts a scanned DB cell into a bool. MySQL exposes
// BOOLEAN/NON_UNIQUE as TINYINT(1); the driver surfaces it as int64.
// NULL becomes false (the callers are flag-shaped: NonUnique, ...).
func cellToBool(v any) (bool, error) {
	switch t := v.(type) {
	case nil:
		return false, nil
	case bool:
		return t, nil
	case int64:
		return t != 0, nil
	case int:
		return t != 0, nil
	case []byte:
		switch string(t) {
		case "0":
			return false, nil
		case "1":
			return true, nil
		case "":
			return false, nil
		default:
			return false, fmt.Errorf("expected bool-compatible cell, got %q", string(t))
		}
	case string:
		switch t {
		case "0":
			return false, nil
		case "1":
			return true, nil
		case "":
			return false, nil
		default:
			return false, fmt.Errorf("expected bool-compatible cell, got %q", t)
		}
	default:
		return false, fmt.Errorf("expected bool-compatible cell, got %T", v)
	}
}

// cellToInt converts a scanned DB cell into an int. NULL becomes 0.
// MySQL TINYINT/SMALLINT/INT come back as int64 via the standard driver;
// we accept all numeric shapes the driver can surface.
func cellToInt(v any) (int, error) {
	n, err := cellToInt64(v)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// cellToInt64 converts a scanned DB cell into an int64. NULL becomes -1
// (the consumers — Cardinality, Rows, DataLength, IndexLength, DataFree,
// AutoIncrement — use -1 as the "unknown" sentinel so JSON output is
// always numeric, never JSON null).
func cellToInt64(v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return -1, nil
	case int64:
		return t, nil
	case int:
		return int64(t), nil
	case int32:
		return int64(t), nil
	case float64:
		return int64(t), nil
	case []byte:
		if len(t) == 0 {
			return -1, nil
		}
		var n int64
		if _, err := fmt.Sscan(string(t), &n); err != nil {
			return 0, fmt.Errorf("expected int-compatible cell, got %q", string(t))
		}
		return n, nil
	case string:
		if t == "" {
			return -1, nil
		}
		var n int64
		if _, err := fmt.Sscan(t, &n); err != nil {
			return 0, fmt.Errorf("expected int-compatible cell, got %q", t)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected int-compatible cell, got %T", v)
	}
}

// cellToTime converts a scanned DB cell into a time.Time. NULL becomes
// the zero value (callers encode it as JSON null via omitempty or by
// checking IsZero upstream). MySQL DATETIME surfaces as time.Time via
// the driver; we also accept []byte for fake test fixtures.
func cellToTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case nil:
		return time.Time{}, nil
	case time.Time:
		return t, nil
	case []byte:
		if len(t) == 0 {
			return time.Time{}, nil
		}
		// MySQL "YYYY-MM-DD HH:MM:SS" without a timezone — interpret as
		// UTC so callers do not get a silent local-time conversion.
		parsed, err := time.Parse("2006-01-02 15:04:05", string(t))
		if err != nil {
			return time.Time{}, fmt.Errorf("expected datetime cell, got %q", string(t))
		}
		return parsed.UTC(), nil
	case string:
		if t == "" {
			return time.Time{}, nil
		}
		parsed, err := time.Parse("2006-01-02 15:04:05", t)
		if err != nil {
			return time.Time{}, fmt.Errorf("expected datetime cell, got %q", t)
		}
		return parsed.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("expected datetime cell, got %T", v)
	}
}
