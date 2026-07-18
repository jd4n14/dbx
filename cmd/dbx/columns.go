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

// listColumnsFunc lists columns on a resolved connection. Production uses
// introspect.ListColumnsConnection; tests inject fakes for stdout purity.
type listColumnsFunc func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error)

func runColumns(args []string) error {
	return runColumnsCmd(args, os.Stdout, os.Stderr, introspect.ListColumnsConnection)
}

// runColumnsCmd implements `dbx columns`:
//
//	--conn    required named connection
//	--table   required simple table identifier
//	--config  optional config path (else discovery / DBX_CONFIG)
//	--like    optional LIKE pattern (identifier-shaped)
//	--json    emit JSON array of objects instead of TSV
//
// Default stdout (TSV header + rows):
//
//	field<TAB>type<TAB>null<TAB>key<TAB>default<TAB>extra
//
// one column per line. --json: pretty array of objects with `field`,
// `type`, `null`, `key`, `default`, `extra`.
func runColumnsCmd(args []string, stdout, stderr io.Writer, fetch listColumnsFunc) error {
	fs := flag.NewFlagSet("columns", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	table := fs.String("table", "", "table name (simple identifier)")
	configPath := fs.String("config", "", "path to config file (optional)")
	like := fs.String("like", "", "filter columns whose name matches (identifier-shaped)")
	asJSON := fs.Bool("json", false, "emit JSON array instead of TSV")

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
	likePat := strings.TrimSpace(*like)
	if likePat != "" {
		if err := introspect.ValidateLikeOrSchema(likePat); err != nil {
			return err
		}
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

	if fetch == nil {
		fetch = introspect.ListColumnsConnection
	}

	ctx, cancel := context.WithTimeout(context.Background(), introspect.DefaultTimeout+connectBudget)
	defer cancel()

	cols, err := fetch(ctx, conn, tableName, likePat)
	if err != nil {
		return err
	}

	var out []byte
	if *asJSON {
		// introspect.Column is already tagged for JSON; pretty-print.
		raw, err := json.Marshal(cols)
		if err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		buf.WriteByte('\n')
		out = buf.Bytes()
	} else {
		var buf bytes.Buffer
		// Header matches DataGrip-style "DDL columns" output and is
		// stable so the Lua parse_columns_list helpers can rely on
		// exactly six tab-separated columns per row.
		buf.WriteString("field\ttype\tnull\tkey\tdefault\textra\n")
		for _, c := range cols {
			def := ""
			if c.Default != nil {
				def = fmt.Sprintf("%v", c.Default)
			}
			fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Field, c.Type, c.Null, c.Key, def, c.Extra)
		}
		out = buf.Bytes()
	}

	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
