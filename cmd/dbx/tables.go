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
	"github.com/jd4n14/dbx/internal/introspect"
)

// listTablesFunc lists tables on a resolved connection. Production uses
// introspect.ListTablesConnection; tests inject fakes for stdout purity.
type listTablesFunc func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error)

func runTables(args []string) error {
	return runTablesCmd(args, os.Stdout, os.Stderr, introspect.ListTablesConnection)
}

// runTablesCmd implements `dbx tables`:
//
//	--conn    required named connection
//	--config  optional config path (else discovery / DBX_CONFIG)
//	--schema  optional schema name (defaults to the connection's database)
//	--like    optional LIKE pattern (identifier-shaped, validated before query)
//	--json    emit JSON array of strings instead of one-name-per-line text
//
// Default stdout: one table name per line (pipe friendly).
// --json: pretty JSON array of strings.
func runTablesCmd(args []string, stdout, stderr io.Writer, fetch listTablesFunc) error {
	fs := flag.NewFlagSet("tables", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	configPath := fs.String("config", "", "path to config file (optional)")
	schema := fs.String("schema", "", "schema (database) to introspect (optional)")
	like := fs.String("like", "", "filter tables whose name matches (identifier-shaped)")
	asJSON := fs.Bool("json", false, "emit JSON array instead of one-name-per-line text")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*connName) == "" {
		return fmt.Errorf("--conn is required")
	}
	schemaName := strings.TrimSpace(*schema)
	if schemaName != "" {
		if err := introspect.ValidateLikeOrSchema(schemaName); err != nil {
			return err
		}
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
		fetch = introspect.ListTablesConnection
	}

	ctx, cancel := context.WithTimeout(context.Background(), introspect.DefaultTimeout+connectBudget)
	defer cancel()

	names, err := fetch(ctx, conn, schemaName, likePat)
	if err != nil {
		return err
	}

	var out []byte
	if *asJSON {
		// json.Marshal handles []string directly; pretty-print + trailing newline.
		raw, err := json.Marshal(names)
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
		for _, n := range names {
			buf.WriteString(n)
			buf.WriteByte('\n')
		}
		out = buf.Bytes()
	}

	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
