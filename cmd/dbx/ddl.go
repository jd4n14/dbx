package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/ddl"
)

// ddlFetchFunc fetches CREATE TABLE DDL for a table on a resolved connection.
// Production uses ddl.FetchConnection; tests inject fakes for stdout purity.
type ddlFetchFunc func(ctx context.Context, conn config.Connection, table string) (string, error)

func runDDL(args []string) error {
	return runDDLCmd(args, os.Stdout, os.Stderr, ddl.FetchConnection)
}

// runDDLCmd implements `dbx ddl`.
//
//	--conn   required named connection
//	--table  required simple table identifier
//	--config optional config path (else discovery / DBX_CONFIG)
//	--json   optional JSON envelope instead of raw SQL
//
// On success, SQL or JSON is written only to stdout (stdout purity).
func runDDLCmd(args []string, stdout, stderr io.Writer, fetch ddlFetchFunc) error {
	fs := flag.NewFlagSet("ddl", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	table := fs.String("table", "", "table name (simple identifier)")
	configPath := fs.String("config", "", "path to config file (optional)")
	asJSON := fs.Bool("json", false, "emit JSON envelope instead of raw SQL")

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

	// ddl is MySQL-specific (SHOW CREATE TABLE). SQLite is accepted in
	// config so `go test ./...` can drive query integration tests offline,
	// but it is NOT a documented production DDL target. Reject non-mysql
	// drivers before any fetch/network/table validation.
	if conn.Driver != "mysql" {
		return fmt.Errorf("ddl only supports mysql (connection %q uses driver %q)", strings.TrimSpace(*connName), conn.Driver)
	}

	if fetch == nil {
		fetch = ddl.FetchConnection
	}

	// Overall budget: connect (DSN dial/ping) + default DDL timeout.
	// Reuses package-level connectBudget from query.go.
	ctx, cancel := context.WithTimeout(context.Background(), ddl.DefaultTimeout+connectBudget)
	defer cancel()

	ddlText, err := fetch(ctx, conn, tableName)
	if err != nil {
		return err
	}

	var out []byte
	if *asJSON {
		out, err = ddl.EncodeJSON(ddl.Result{
			Type:       "ddl",
			Connection: strings.TrimSpace(*connName),
			Dialect:    "mysql",
			Table:      tableName,
			DDL:        ddlText,
		})
		if err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
	} else {
		out = []byte(ddlText)
		if len(out) == 0 || out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}

	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
