package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/ddl"
	"github.com/jd4n14/dbx/internal/introspect"
)

// listIndexesFunc lists indexes on a resolved connection. Production uses
// introspect.ListIndexesConnection; tests inject fakes for stdout purity.
type listIndexesFunc func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error)

func runIndexes(args []string) error {
	return runIndexesCmd(args, os.Stdout, os.Stderr, introspect.ListIndexesConnection)
}

// runIndexesCmd implements `dbx indexes`:
//
//	--conn   required named connection
//	--table  required simple table identifier
//	--config optional config path (else discovery / DBX_CONFIG)
//
// Default stdout is pretty JSON (indexes are nested / composite and TSV
// would be misleading). The MySQL-only driver check mirrors `dbx ddl`:
// sqlite is accepted in config as a test seam but rejected here.
func runIndexesCmd(args []string, stdout, stderr io.Writer, fetch listIndexesFunc) error {
	fs := flag.NewFlagSet("indexes", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	table := fs.String("table", "", "table name (simple identifier)")
	configPath := fs.String("config", "", "path to config file (optional)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*connName) == "" {
		return fmt.Errorf("--conn is required")
	}
	tableName := strings.TrimSpace(*table)
	if tableName == "" {
		return fmt.Errorf("--table is required")
	}
	if err := ddl.ValidateTableName(tableName); err != nil {
		return err
	}

	path, err := config.FindConfigPath(*configPath, os.Getenv, "", "")
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	conn, err := cfg.Connection(*connName)
	if err != nil {
		return err
	}

	// information_schema is MySQL-only; sqlite is test-only.
	if conn.Driver != "mysql" {
		return fmt.Errorf("indexes only supports mysql (connection %q uses driver %q)", strings.TrimSpace(*connName), conn.Driver)
	}

	if fetch == nil {
		fetch = introspect.ListIndexesConnection
	}

	ctx, cancel := context.WithTimeout(context.Background(), introspect.DefaultTimeout+connectBudget)
	defer cancel()

	idx, err := fetch(ctx, conn, tableName)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	buf.WriteByte('\n')

	if _, err := stdout.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
