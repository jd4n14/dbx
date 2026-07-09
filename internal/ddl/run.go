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
func FetchConnection(ctx context.Context, conn config.Connection, table string) (string, error) {
	table = strings.TrimSpace(table)
	if err := ValidateTableName(table); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer database.Close()

	return Fetch(ctx, database, table)
}
