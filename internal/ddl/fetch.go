package ddl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/db"
)

// Fetch validates table, runs SHOW CREATE TABLE, and returns the CREATE text.
// Validate runs before QueryContext (no network when name is invalid — caller
// still owns Open; Fetch itself only uses the provided db).
func Fetch(ctx context.Context, database db.DB, table string) (string, error) {
	table = strings.TrimSpace(table)
	if err := ValidateTableName(table); err != nil {
		return "", err
	}

	sqlText := "SHOW CREATE TABLE " + QuoteIdentifier(table)
	rows, err := database.QueryContext(ctx, sqlText)
	if err != nil {
		return "", fmt.Errorf("ddl: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("ddl: columns: %w", err)
	}
	if len(cols) < 2 {
		return "", fmt.Errorf("ddl: expected at least 2 columns from SHOW CREATE TABLE, got %d", len(cols))
	}

	ddlIdx := 1
	for i, c := range cols {
		if strings.EqualFold(c, "Create Table") {
			ddlIdx = i
			break
		}
	}

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("ddl: %w", err)
		}
		return "", fmt.Errorf("ddl: table not found or empty SHOW CREATE result")
	}

	holders := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range holders {
		dest[i] = &holders[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return "", fmt.Errorf("ddl: scan: %w", err)
	}

	if rows.Next() {
		return "", fmt.Errorf("ddl: unexpected row count from SHOW CREATE TABLE")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("ddl: %w", err)
	}

	ddlText, err := cellString(holders[ddlIdx])
	if err != nil {
		return "", fmt.Errorf("ddl: %w", err)
	}
	if ddlText == "" {
		return "", fmt.Errorf("ddl: empty CREATE TABLE text")
	}
	return ddlText, nil
}

func cellString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", fmt.Errorf("CREATE TABLE value is NULL")
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	default:
		return "", fmt.Errorf("CREATE TABLE value has unexpected type %T", v)
	}
}
