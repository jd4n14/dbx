package main

import (
	"context"
	"encoding/json"
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

// runStatus dispatches `dbx status --conn NAME [--json]` to the testable entry.
func runStatus(args []string) error {
	return runStatusCmd(args, os.Stdin, os.Stdout, os.Stderr, "", nil)
}

// runStatusCmd implements `dbx status`:
//
//	--conn   required named connection
//	--config optional config path (else discovery / DBX_CONFIG)
//	--json   emit pretty JSON envelope instead of single-line text
//
// On success, stdout carries either a single-line text summary
// (`<conn> <env> <driver> <server_version>` with `sql_mode=…` appended when
// non-empty) or a pretty JSON envelope. On failure, run() emits "error: …"
// on stderr; stdout stays empty.
//
// open is the injectable seam so tests can drive status without MySQL. Pass
// nil to fall back to the production opener (mysql.Open).
//
// stdin and cwd are accepted to mirror the testable signature of query /
// explain. They are unused by status today but kept for symmetry.
func runStatusCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, open openDBFunc) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	configPath := fs.String("config", "", "path to config file (optional)")
	asJSON := fs.Bool("json", false, "emit JSON envelope instead of text")

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

	// MySQL-only: SELECT VERSION() / SELECT @@SESSION.sql_mode are MySQL
	// idioms. Plan 006 keeps sqlite as a test-only connector; status is a
	// product surface, so reject sqlite up front for a friendly error.
	if conn.Driver == "sqlite" {
		return fmt.Errorf("status only supports mysql (connection %q uses driver %q)", strings.TrimSpace(*connName), conn.Driver)
	}

	if open == nil {
		open = mysql.Open
	}

	// Overall budget: connect (DSN dial/ping) + default query timeout. The
	// two SELECTs are trivial; the dial is what dominates.
	ctx, cancel := context.WithTimeout(context.Background(), query.DefaultQueryTimeout+connectBudget)
	defer cancel()

	database, err := open(ctx, conn)
	if err != nil {
		return fmt.Errorf("status %s: %w", strings.TrimSpace(*connName), err)
	}
	defer database.Close()

	serverVersion, sqlMode, err := fetchStatus(ctx, database)
	if err != nil {
		return err
	}

	statusEnv := conn.Env // empty when unset; preserved verbatim
	resolvedConn := strings.TrimSpace(*connName)
	driver := conn.Driver // already normalized to mysql / sqlite by validateAndNormalize

	if *asJSON {
		envelope := statusEnvelope{
			Type:          "status",
			Connection:    resolvedConn,
			Driver:        driver,
			Env:           statusEnv,
			ServerVersion: serverVersion,
			SQLMode:       sqlMode,
			DBXVersion:    Version,
		}
		body, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		body = append(body, '\n')
		if _, err := stdout.Write(body); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}

	line := fmt.Sprintf("%s %s %s %s", resolvedConn, statusEnv, driver, serverVersion)
	if sqlMode != "" {
		line += " sql_mode=" + sqlMode
	}
	if _, err := fmt.Fprintln(stdout, line); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// statusEnvelope is the on-disk / stdout JSON shape for `dbx status --json`.
// Field order mirrors the plan: type, connection, driver, env, server_version,
// sql_mode, dbx_version.
type statusEnvelope struct {
	Type          string `json:"type"`
	Connection    string `json:"connection"`
	Driver        string `json:"driver"`
	Env           string `json:"env"`
	ServerVersion string `json:"server_version"`
	SQLMode       string `json:"sql_mode"`
	DBXVersion    string `json:"dbx_version"`
}

// fetchStatus runs the two SELECTs (VERSION() and @@SESSION.sql_mode) against
// the already-open database. Both queries are pre-validated by query.ValidateQuery
// so the same write barrier that protects dbx query also guards status — no
// new SQL surface is introduced.
//
// Each query is expected to return a single row, single column. Anything else
// is treated as an error so callers do not silently get a misleading value.
func fetchStatus(ctx context.Context, database db.DB) (serverVersion, sqlMode string, err error) {
	// Validate both SQL strings up front. ValidateQuery is the package's
	// write barrier and rejects anything outside the SELECT / WITH / SHOW /
	// DESCRIBE / EXPLAIN allowlist. These literals are SELECT-only, but we
	// still route them through the same gate so a future edit cannot
	// silently widen status's SQL surface.
	const versionSQL = "SELECT VERSION()"
	if vErr := query.ValidateQuery(versionSQL); vErr != nil {
		return "", "", fmt.Errorf("status: %w", vErr)
	}
	const sqlModeSQL = "SELECT @@SESSION.sql_mode"
	if vErr := query.ValidateQuery(sqlModeSQL); vErr != nil {
		return "", "", fmt.Errorf("status: %w", vErr)
	}

	serverVersion, err = scanSingleString(ctx, database, versionSQL, "VERSION()")
	if err != nil {
		return "", "", fmt.Errorf("status: %w", err)
	}
	sqlMode, err = scanSingleString(ctx, database, sqlModeSQL, "@@SESSION.sql_mode")
	if err != nil {
		return "", "", fmt.Errorf("status: %w", err)
	}
	return serverVersion, sqlMode, nil
}

// scanSingleString runs a single-column query and returns the string value of
// the first row. nil / non-string values become empty strings; extra rows are
// ignored (MySQL VERSION() never returns more than one row).
func scanSingleString(ctx context.Context, database db.DB, sqlText, label string) (string, error) {
	rows, err := database.QueryContext(ctx, sqlText)
	if err != nil {
		return "", fmt.Errorf("query %s: %w", label, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("scan %s: %w", label, err)
		}
		return "", fmt.Errorf("query %s: no rows returned", label)
	}
	var dest any
	if err := rows.Scan(&dest); err != nil {
		return "", fmt.Errorf("scan %s: %w", label, err)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("scan %s: %w", label, err)
	}
	if dest == nil {
		return "", nil
	}
	if s, ok := dest.(string); ok {
		return s, nil
	}
	if b, ok := dest.([]byte); ok {
		return string(b), nil
	}
	// Fall back to %v for exotic types (e.g. numeric VERSION() variants on
	// non-MySQL drivers) — never seen in production but cheap to support.
	return strings.TrimSpace(fmt.Sprintf("%v", dest)), nil
}