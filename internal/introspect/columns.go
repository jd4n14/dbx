package introspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/ddl"
)

// Column mirrors a single row of MySQL `SHOW COLUMNS FROM <table>`.
//
// Default is nil for SQL NULL and string otherwise. The CLI JSON encoder
// emits nil as JSON null and string as a JSON string (numbers come back as
// scanned []byte in practice and are stringified for safety).
type Column struct {
	Field   string `json:"field"`
	Type    string `json:"type"`
	Null    string `json:"null"`
	Key     string `json:"key"`
	Default any    `json:"default"`
	Extra   string `json:"extra"`
}

// ListColumns runs `SHOW COLUMNS FROM <table> [LIKE '<pattern>']` and returns
// one Column per row. The table name is validated before any database call.
func ListColumns(ctx context.Context, database db.DB, table, like string) ([]Column, error) {
	table = strings.TrimSpace(table)
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}
	if strings.TrimSpace(like) != "" {
		if err := ValidateLikeOrSchema(like); err != nil {
			return nil, err
		}
	}

	sqlText := buildShowColumnsSQL(table, like)
	rows, err := database.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("introspect: columns: %w", err)
	}
	if len(cols) < 6 {
		return nil, fmt.Errorf("introspect: expected at least 6 columns from SHOW COLUMNS, got %d", len(cols))
	}

	// Map SHOW COLUMNS result columns by name (case-insensitive). MySQL 8
	// returns: Field, Type, Null, Key, Default, Extra. Older versions may
	// return different orders; match by header so we are resilient.
	fieldIdx, typeIdx, nullIdx, keyIdx, defaultIdx, extraIdx := -1, -1, -1, -1, -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "field":
			fieldIdx = i
		case "type":
			typeIdx = i
		case "null":
			nullIdx = i
		case "key":
			keyIdx = i
		case "default":
			defaultIdx = i
		case "extra":
			extraIdx = i
		}
	}
	if fieldIdx < 0 || typeIdx < 0 || nullIdx < 0 || keyIdx < 0 || defaultIdx < 0 || extraIdx < 0 {
		return nil, fmt.Errorf("introspect: SHOW COLUMNS result missing required columns (got %v)", cols)
	}

	var out []Column
	for rows.Next() {
		holders := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range holders {
			dest[i] = &holders[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("introspect: scan: %w", err)
		}

		fieldStr, err := cellToString(holders[fieldIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: field: %w", err)
		}
		typeStr, err := cellToString(holders[typeIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: type: %w", err)
		}
		nullStr, err := cellToString(holders[nullIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: null: %w", err)
		}
		keyStr, err := cellToString(holders[keyIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: key: %w", err)
		}
		extraStr, err := cellToString(holders[extraIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: extra: %w", err)
		}

		var defaultValue any
		rawDefault := holders[defaultIdx]
		if rawDefault == nil {
			defaultValue = nil
		} else {
			s, err := cellToString(rawDefault)
			if err != nil {
				return nil, fmt.Errorf("introspect: default: %w", err)
			}
			defaultValue = s
		}

		out = append(out, Column{
			Field:   fieldStr,
			Type:    typeStr,
			Null:    nullStr,
			Key:     keyStr,
			Default: defaultValue,
			Extra:   extraStr,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	return out, nil
}

// buildShowColumnsSQL constructs the SHOW COLUMNS statement for a quoted table.
// LIKE is optional; the literal is single-quoted after escaping.
func buildShowColumnsSQL(table, like string) string {
	var b strings.Builder
	b.WriteString("SHOW COLUMNS FROM ")
	b.WriteString(ddl.QuoteIdentifier(table))
	if like != "" {
		b.WriteString(" LIKE ")
		b.WriteString(quoteLikeLiteral(like))
	}
	return b.String()
}
