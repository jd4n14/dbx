package ddl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/mysql"
)

// DefaultTimeout is the budget for SHOW CREATE TABLE execution.
const DefaultTimeout = 30 * time.Second

// FetchConnection validates the table name before Open, then fetches DDL.
// Open uses the caller's context (CLI supplies connect budget); Fetch applies
// DefaultTimeout so dial/ping is not starved by the query deadline alone.
func FetchConnection(ctx context.Context, conn config.Connection, table string) (string, error) {
	table = strings.TrimSpace(table)
	if err := ValidateTableName(table); err != nil {
		return "", err
	}

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	fetchCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	return Fetch(fetchCtx, database, table)
}
