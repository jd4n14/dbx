package introspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/ddl"
)

// ForeignKey mirrors one row of
// information_schema.KEY_COLUMN_USAGE joined with REFERENTIAL_CONSTRAINTS.
// Multi-column foreign keys produce one row per (constraint, ordinal)
// pair; the consumer can group by Name to recover the full key.
//
// ReferencedSchema/Table/Column may be empty for self-references on
// older MySQL versions; the WHERE clause restricts to rows where
// REFERENCED_TABLE_NAME IS NOT NULL so callers do not see orphaned rows.
type ForeignKey struct {
	Name             string `json:"name"`
	Column           string `json:"column"`
	ReferencedSchema string `json:"referenced_schema"`
	ReferencedTable  string `json:"referenced_table"`
	ReferencedColumn string `json:"referenced_column"`
	UpdateRule       string `json:"update_rule"`
	DeleteRule       string `json:"delete_rule"`
}

// ListForeignKeys runs
//
//	SELECT kcu.COLUMN_NAME, kcu.REFERENCED_TABLE_SCHEMA,
//	       kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME,
//	       rc.CONSTRAINT_NAME, rc.UPDATE_RULE, rc.DELETE_RULE
//	FROM information_schema.KEY_COLUMN_USAGE kcu
//	JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
//	  ON kcu.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA
//	 AND kcu.CONSTRAINT_NAME  = rc.CONSTRAINT_NAME
//	WHERE kcu.TABLE_SCHEMA = DATABASE()
//	  AND kcu.TABLE_NAME   = ?
//	  AND kcu.REFERENCED_TABLE_NAME IS NOT NULL
//	ORDER BY rc.CONSTRAINT_NAME, kcu.ORDINAL_POSITION
//
// and returns one ForeignKey per row. Tables with no foreign keys yield
// an empty slice (not an error).
func ListForeignKeys(ctx context.Context, database db.DB, table string) ([]ForeignKey, error) {
	table = strings.TrimSpace(table)
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}

	rows, err := database.QueryContext(ctx, buildListForeignKeysSQL(), table)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("introspect: columns: %w", err)
	}

	columnIdx, refSchemaIdx, refTableIdx, refColumnIdx, nameIdx, updateIdx, deleteIdx := -1, -1, -1, -1, -1, -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "column_name":
			columnIdx = i
		case "referenced_table_schema":
			refSchemaIdx = i
		case "referenced_table_name":
			refTableIdx = i
		case "referenced_column_name":
			refColumnIdx = i
		case "constraint_name":
			nameIdx = i
		case "update_rule":
			updateIdx = i
		case "delete_rule":
			deleteIdx = i
		}
	}
	if columnIdx < 0 || refSchemaIdx < 0 || refTableIdx < 0 || refColumnIdx < 0 || nameIdx < 0 || updateIdx < 0 || deleteIdx < 0 {
		return nil, fmt.Errorf("introspect: KEY_COLUMN_USAGE/REFERENTIAL_CONSTRAINTS missing required columns (got %v)", cols)
	}

	var out []ForeignKey
	for rows.Next() {
		holders := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range holders {
			dest[i] = &holders[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("introspect: scan: %w", err)
		}

		column, err := cellToString(holders[columnIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: column_name: %w", err)
		}
		refSchema, err := cellToString(holders[refSchemaIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: referenced_table_schema: %w", err)
		}
		refTable, err := cellToString(holders[refTableIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: referenced_table_name: %w", err)
		}
		refColumn, err := cellToString(holders[refColumnIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: referenced_column_name: %w", err)
		}
		name, err := cellToString(holders[nameIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: constraint_name: %w", err)
		}
		updateRule, err := cellToString(holders[updateIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: update_rule: %w", err)
		}
		deleteRule, err := cellToString(holders[deleteIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: delete_rule: %w", err)
		}

		out = append(out, ForeignKey{
			Name:             name,
			Column:           column,
			ReferencedSchema: refSchema,
			ReferencedTable:  refTable,
			ReferencedColumn: refColumn,
			UpdateRule:       updateRule,
			DeleteRule:       deleteRule,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	return out, nil
}

// buildListForeignKeysSQL returns the join query with `?` for the table
// name. The literal is parameter-bound by the driver.
func buildListForeignKeysSQL() string {
	return `SELECT kcu.COLUMN_NAME,
               kcu.REFERENCED_TABLE_SCHEMA,
               kcu.REFERENCED_TABLE_NAME,
               kcu.REFERENCED_COLUMN_NAME,
               rc.CONSTRAINT_NAME,
               rc.UPDATE_RULE,
               rc.DELETE_RULE
        FROM information_schema.KEY_COLUMN_USAGE kcu
        JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
          ON kcu.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA
         AND kcu.CONSTRAINT_NAME  = rc.CONSTRAINT_NAME
        WHERE kcu.TABLE_SCHEMA = DATABASE()
          AND kcu.TABLE_NAME   = ?
          AND kcu.REFERENCED_TABLE_NAME IS NOT NULL
        ORDER BY rc.CONSTRAINT_NAME, kcu.ORDINAL_POSITION`
}