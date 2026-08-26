package introspect

import (
	"context"
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/ddl"
)

// Index mirrors one row of `information_schema.STATISTICS` for a single
// table. The struct is shared by the CLI and the Neovim result buffer;
// JSON tags match the MySQL column names in snake_case so the result is
// self-describing without needing an external schema doc.
//
// Cardinality is approximate (MySQL recomputes it via sampling); -1 means
// the engine did not report a value. Collation is "A" (ascending), "D"
// (descending), or empty for indexes that do not store column ordering
// (e.g. FULLTEXT, HASH).
type Index struct {
	Name        string `json:"name"`
	NonUnique   bool   `json:"non_unique"`
	SeqInIndex  int    `json:"seq_in_index"`
	ColumnName  string `json:"column_name"`
	Collation   string `json:"collation"`
	Cardinality int64  `json:"cardinality"`
	IndexType   string `json:"index_type"`
}

// ListIndexes runs
//
//	SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME,
//	       COLLATION, CARDINALITY, INDEX_TYPE
//	FROM information_schema.STATISTICS
//	WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
//	ORDER BY INDEX_NAME, SEQ_IN_INDEX
//
// and returns one Index per row, in the order MySQL reported them. The
// table name is validated before any database call; the parameter binding
// handles the value-level quoting (no identifier injection risk).
func ListIndexes(ctx context.Context, database db.DB, table string) ([]Index, error) {
	table = strings.TrimSpace(table)
	if err := ddl.ValidateTableName(table); err != nil {
		return nil, err
	}

	rows, err := database.QueryContext(ctx, buildListIndexesSQL(), table)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("introspect: columns: %w", err)
	}

	// Map by header (MySQL 5.7/8 return the columns above in this exact
	// order, but match by name so the function is resilient to a future
	// reordering).
	nameIdx, nonUniqueIdx, seqIdx, columnIdx, collationIdx, cardIdx, typeIdx := -1, -1, -1, -1, -1, -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "index_name":
			nameIdx = i
		case "non_unique":
			nonUniqueIdx = i
		case "seq_in_index":
			seqIdx = i
		case "column_name":
			columnIdx = i
		case "collation":
			collationIdx = i
		case "cardinality":
			cardIdx = i
		case "index_type":
			typeIdx = i
		}
	}
	if nameIdx < 0 || nonUniqueIdx < 0 || seqIdx < 0 || columnIdx < 0 || collationIdx < 0 || cardIdx < 0 || typeIdx < 0 {
		return nil, fmt.Errorf("introspect: information_schema.STATISTICS missing required columns (got %v)", cols)
	}

	var out []Index
	for rows.Next() {
		holders := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range holders {
			dest[i] = &holders[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("introspect: scan: %w", err)
		}

		name, err := cellToString(holders[nameIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: index_name: %w", err)
		}
		column, err := cellToString(holders[columnIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: column_name: %w", err)
		}
		collation, err := cellToString(holders[collationIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: collation: %w", err)
		}
		indexType, err := cellToString(holders[typeIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: index_type: %w", err)
		}

		nonUnique, err := cellToBool(holders[nonUniqueIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: non_unique: %w", err)
		}
		seq, err := cellToInt(holders[seqIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: seq_in_index: %w", err)
		}
		card, err := cellToInt64(holders[cardIdx])
		if err != nil {
			return nil, fmt.Errorf("introspect: cardinality: %w", err)
		}

		out = append(out, Index{
			Name:        name,
			NonUnique:   nonUnique,
			SeqInIndex:  seq,
			ColumnName:  column,
			Collation:   collation,
			Cardinality: card,
			IndexType:   indexType,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}

	return out, nil
}

// buildListIndexesSQL returns the STATISTICS query with `?` for the table
// name. The literal is parameter-bound by the driver, so it does not need
// identifier quoting.
func buildListIndexesSQL() string {
	return `SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME,
               COLLATION, CARDINALITY, INDEX_TYPE
        FROM information_schema.STATISTICS
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
        ORDER BY INDEX_NAME, SEQ_IN_INDEX`
}