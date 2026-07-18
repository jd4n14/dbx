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
