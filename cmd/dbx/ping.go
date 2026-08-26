package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
	"github.com/jd4n14/dbx/internal/mysql"
	"github.com/jd4n14/dbx/internal/query"
)

// openDBFunc opens a database connection from a resolved config.Connection.
// Production uses mysql.Open; tests inject fakes for stdout purity.
type openDBFunc func(ctx context.Context, conn config.Connection) (db.DB, error)

// runPing dispatches `dbx ping --conn NAME` to the testable entry.
func runPing(args []string) error {
	return runPingCmd(args, os.Stdin, os.Stdout, os.Stderr, "", nil)
}

// runPingCmd implements `dbx ping`:
//
//	--conn   required named connection
//	--config optional config path (else discovery / DBX_CONFIG)
//
// On success, prints "ok" + newline to stdout (stdout purity). On failure,
// returns an error so run() emits "error: …" on stderr; stdout stays empty.
//
// open is the injectable seam so tests can drive ping without MySQL. Pass
// nil to fall back to the production opener (mysql.Open).
//
// stdin and cwd are accepted to mirror the testable signature of query /
// explain. They are unused by ping today but kept for symmetry.
func runPingCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, open openDBFunc) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	configPath := fs.String("config", "", "path to config file (optional)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*connName) == "" {
		return fmt.Errorf("--conn is required")
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

	// ping reuses mysql.Open: it inherits the force-safe DSN policy and the
	// pool hygiene (SetMaxOpenConns(1)). SQLite is test-only per Plan 006
	// and rejected up front so users running ping against a sqlite connection
	// get a friendly error instead of silently opening a file-backed DB.
	if conn.Driver == "sqlite" {
		return fmt.Errorf("ping only supports mysql (connection %q uses driver %q)", strings.TrimSpace(*connName), conn.Driver)
	}

	if open == nil {
		open = mysql.Open
	}

	// Overall budget: connect (DSN dial/ping) + default query timeout. The
	// ping itself is cheap, but Open already calls PingContext; we re-ping
	// here so the latency matches the round-trip a query would face.
	ctx, cancel := context.WithTimeout(context.Background(), query.DefaultQueryTimeout+connectBudget)
	defer cancel()

	database, err := open(ctx, conn)
	if err != nil {
		return fmt.Errorf("ping %s: %w", strings.TrimSpace(*connName), err)
	}
	defer database.Close()

	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping %s: %w", strings.TrimSpace(*connName), err)
	}

	if _, err := fmt.Fprintln(stdout, "ok"); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}