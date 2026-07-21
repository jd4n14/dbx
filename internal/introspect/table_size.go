package introspect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/ddl"
)

// TableSize mirrors one row of `information_schema.TABLES` for a single
// table. Several fields are nullable in MySQL (CreateTime / UpdateTime
// may be NULL for INFORMATION_SCHEMA-internal tables; TABLE_ROWS /
// AUTO_INCREMENT may be NULL on engines that do not track them).
//
// Rows is an APPROXIMATE estimate from InnoDB's stored statistics; for
// accurate counts, callers must run `SELECT COUNT(*)`. The CLI surfaces
// this caveat in the README so users do not mistake the estimate for
// ground truth.
//
// Cardinality-shaped fields (Rows, AutoIncrement, DataLength, ...) that
// MySQL returns as NULL are normalised to -1 in the Go value so JSON
// consumers see a single sentinel.
type TableSize struct {
	Rows          int64     `json:"rows"`
	DataBytes     int64     `json:"data_bytes"`
	IndexBytes    int64     `json:"index_bytes"`
	DataFreeBytes int64     `json:"data_free_bytes"`
	AutoIncrement int64     `json:"auto_increment"`
	Collation     string    `json:"collation"`
	CreateTime    time.Time `json:"create_time"`
	UpdateTime    time.Time `json:"update_time"`
	Engine        string    `json:"engine"`
}

// GetTableSize runs
//
//	SELECT TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH, DATA_FREE,
//	       AUTO_INCREMENT, TABLE_COLLATION, CREATE_TIME, UPDATE_TIME,
//	       ENGINE
//	FROM information_schema.TABLES
//	WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
//
// and returns a single TableSize. Missing rows (unknown table) are
// reported as an empty TableSize plus ErrTableNotFound; missing columns
// are a hard error (the MySQL schema changed under us).
func GetTableSize(ctx context.Context, database db.DB, table string) (TableSize, error) {
	table = strings.TrimSpace(table)
	if err := ddl.ValidateTableName(table); err != nil {
		return TableSize{}, err
	}

	rows, err := database.QueryContext(ctx, buildTableSizeSQL(), table)
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: columns: %w", err)
	}

	rowsIdx, dataIdx, indexIdx, freeIdx, autoIdx, collIdx, createIdx, updateIdx, engineIdx := -1, -1, -1, -1, -1, -1, -1, -1, -1
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "table_rows":
			rowsIdx = i
		case "data_length":
			dataIdx = i
		case "index_length":
			indexIdx = i
		case "data_free":
			freeIdx = i
		case "auto_increment":
			autoIdx = i
		case "table_collation":
			collIdx = i
		case "create_time":
			createIdx = i
		case "update_time":
			updateIdx = i
		case "engine":
			engineIdx = i
		}
	}
	if rowsIdx < 0 || dataIdx < 0 || indexIdx < 0 || freeIdx < 0 || autoIdx < 0 || collIdx < 0 || createIdx < 0 || updateIdx < 0 || engineIdx < 0 {
		return TableSize{}, fmt.Errorf("introspect: information_schema.TABLES missing required columns (got %v)", cols)
	}

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return TableSize{}, fmt.Errorf("introspect: %w", err)
		}
		return TableSize{}, ErrTableNotFound
	}
	// A second row would indicate the engine returned more than one
	// TABLES entry, which would mean our WHERE clause is wrong.
	if rows.Next() {
		return TableSize{}, fmt.Errorf("introspect: information_schema.TABLES returned more than one row for %q", table)
	}

	holders := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range holders {
		dest[i] = &holders[i]
	}
	// Replay Scan for the row we just consumed by Next(); rows.Next()
	// advances the cursor, so we now Scan the current row.
	if err := rows.Scan(dest...); err != nil {
		return TableSize{}, fmt.Errorf("introspect: scan: %w", err)
	}

	rowCount, err := cellToInt64(holders[rowsIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: table_rows: %w", err)
	}
	dataBytes, err := cellToInt64(holders[dataIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: data_length: %w", err)
	}
	indexBytes, err := cellToInt64(holders[indexIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: index_length: %w", err)
	}
	dataFree, err := cellToInt64(holders[freeIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: data_free: %w", err)
	}
	autoInc, err := cellToInt64(holders[autoIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: auto_increment: %w", err)
	}
	collation, err := cellToString(holders[collIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: table_collation: %w", err)
	}
	createTime, err := cellToTime(holders[createIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: create_time: %w", err)
	}
	updateTime, err := cellToTime(holders[updateIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: update_time: %w", err)
	}
	engine, err := cellToString(holders[engineIdx])
	if err != nil {
		return TableSize{}, fmt.Errorf("introspect: engine: %w", err)
	}

	return TableSize{
		Rows:          rowCount,
		DataBytes:     dataBytes,
		IndexBytes:    indexBytes,
		DataFreeBytes: dataFree,
		AutoIncrement: autoInc,
		Collation:     collation,
		CreateTime:    createTime,
		UpdateTime:    updateTime,
		Engine:        engine,
	}, nil
}

// ErrTableNotFound signals `dbx table-size` was run against a table that
// does not exist in the current database. Exported so callers (CLI, tests)
// can match it with errors.Is.
var ErrTableNotFound = fmt.Errorf("introspect: table not found")

// buildTableSizeSQL returns the TABLES query with `?` for the table name.
// The literal is parameter-bound by the driver.
func buildTableSizeSQL() string {
	return `SELECT TABLE_ROWS,
               DATA_LENGTH,
               INDEX_LENGTH,
               DATA_FREE,
               AUTO_INCREMENT,
               TABLE_COLLATION,
               CREATE_TIME,
               UPDATE_TIME,
               ENGINE
        FROM information_schema.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`
}