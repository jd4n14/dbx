package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/explain"
	"github.com/jd4n14/dbx/internal/export"
	"github.com/jd4n14/dbx/internal/mysql"
	"github.com/jd4n14/dbx/internal/query"
)

// runExplain dispatches `dbx explain` to the testable entry.
func runExplain(args []string) error {
	return runExplainCmd(args, os.Stdin, os.Stdout, os.Stderr, "")
}

// explainRunner is the injectable seam so tests can exercise the command
// without opening a real MySQL connection. Production uses
// explainRunConnection; tests inject a fake that returns canned results.
type explainRunner func(ctx context.Context, conn config.Connection, sqlText string, mode explain.Mode) (explain.Result, error)

// runExplainCmd implements `dbx explain [--json] [-o FILE] [--no-json-sidecar] [--conn NAME] <SQL>`:
//
//	--conn              required named connection (mandatory when SQL comes
//	                    from a positional arg; ignored when stdin carries SQL
//	                    and the caller already resolved a connection)
//	--config            optional config path (else discovery / DBX_CONFIG)
//	--json              switch to EXPLAIN FORMAT=JSON (default: tabular)
//	-o FILE             write the rendered plan to FILE using the Plan 008
//	                    atomic write helper. Required for JSON mode if the
//	                    sidecar is desired.
//	--json-sidecar      emit `<FILE>.meta.json` audit envelope (default ON,
//	                    matches Plan 008)
//	--no-json-sidecar   disable the sidecar (wins over --json-sidecar)
//
// SQL is taken from positional arguments first; if none are supplied the
// command reads stdin. This keeps the CLI both scriptable (one-shot:
// `dbx explain --conn local_wms "SELECT * FROM orders"`) and pipe-friendly
// (Neovim pipes SQL via stdin, just like `dbx query`).
//
// cwd defaults to os.Getwd when empty.
func runExplainCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) error {
	return runExplainCmdWithRunner(args, stdin, stdout, stderr, cwd, nil)
}

// runExplainCmdWithRunner is the fully-injectable entry used by tests.
// Pass nil runner to fall back to explainRunConnection.
func runExplainCmdWithRunner(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string, runner explainRunner) error {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	fs.SetOutput(stderr)

	connName := fs.String("conn", "", "named connection from config")
	configPath := fs.String("config", "", "path to config file (optional)")
	asJSON := fs.Bool("json", false, "emit EXPLAIN FORMAT=JSON output instead of tabular")
	outPath := fs.String("o", "", "output path (atomic write). Required for JSON output if sidecar is desired")
	// --json-sidecar default ON; --no-json-sidecar wins over --json-sidecar.
	withSidecar := fs.Bool("json-sidecar", true, "write <data>.meta.json sidecar with audit metadata (default ON)")
	noSidecar := fs.Bool("no-json-sidecar", false, "disable the JSON sidecar (overrides --json-sidecar)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}

	// SQL: positional args first, else stdin. We accept both so the Neovim
	// integration can pipe via stdin while the CLI remains ergonomic from a
	// terminal.
	sqlText, err := resolveExplainSQL(fs.Args(), stdin)
	if err != nil {
		return err
	}
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return fmt.Errorf("explain requires SQL (positional arg or stdin; usage: dbx explain [--json] [-o FILE] [--no-json-sidecar] [--conn NAME] <SQL>)")
	}

	// --conn is required except when the caller supplied stdin (legacy
	// ergonomics: pipe + named config still works). We refuse the call
	// with a friendly message instead of silently dialing the default.
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

	mode := explain.ModeTabular
	if *asJSON {
		mode = explain.ModeJSON
	}

	// -o FILE + --json without --no-json-sidecar requires -o (we need a
	// target file to anchor the sidecar). Tabular with -o emits no sidecar
	// (the plan locks the sidecar to JSON mode).
	emitSidecar := *withSidecar && !*noSidecar
	resolvedPath := strings.TrimSpace(*outPath)
	if mode == explain.ModeJSON && emitSidecar && resolvedPath == "" {
		return fmt.Errorf("--json output requires -o FILE when sidecar is enabled (use --no-json-sidecar to opt out)")
	}

	if runner == nil {
		runner = explainRunConnection
	}

	ctx, cancel := context.WithTimeout(context.Background(), query.DefaultQueryTimeout+connectBudget)
	defer cancel()

	res, err := runner(ctx, conn, sqlText, mode)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	// Tabular: stdout only OR file (no sidecar in tabular mode per plan).
	if mode == explain.ModeTabular {
		rendered, err := explain.RenderTabular(res, explain.DefaultExtraTruncate)
		if err != nil {
			return err
		}
		if resolvedPath == "" {
			if _, err := stdout.Write(rendered); err != nil {
				return fmt.Errorf("write stdout: %w", err)
			}
			return nil
		}
		abs, err := filepath.Abs(resolvedPath)
		if err != nil {
			return fmt.Errorf("resolve output path: %w", err)
		}
		if err := export.AtomicWrite(abs, rendered, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", filepath.Base(abs), err)
		}
		if _, err := fmt.Fprintln(stdout, abs); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}

	// JSON mode: stdout (no -o) OR file (+ optional sidecar).
	if resolvedPath == "" {
		if _, err := stdout.Write(res.RawJSON); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}

	abs, err := filepath.Abs(resolvedPath)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	if err := writeExplainJSON(abs, res, conn.Name, now, emitSidecar, stdout); err != nil {
		return err
	}
	return nil
}

// writeExplainJSON persists the JSON plan (and optional sidecar) under
// `path`. Sidecar is written first so a partial state never claims a row
// count the data file does not yet contain (Plan 008 invariant).
func writeExplainJSON(path string, res explain.Result, connection string, now time.Time, emitSidecar bool, stdout io.Writer) error {
	if emitSidecar {
		sidecar := path + ".meta.json"
		body, err := export.RenderSidecar(export.Sidecar{
			Version:    export.SidecarVersion,
			Kind:       export.KindExplain,
			SnapshotID: explain.SidecarKey(now, connection),
			Connection: connection,
			ExportedAt: now,
			RowCount:   1, // JSON plan is one document; tabular counts differ (handled in tabular branch)
			Columns:    []string{}, // JSON plans carry no row-shaped columns
			Format:     "json",
			DBXVersion: Version,
		})
		if err != nil {
			return fmt.Errorf("render sidecar: %w", err)
		}
		if err := export.AtomicWrite(sidecar, body, 0o600); err != nil {
			return fmt.Errorf("write sidecar: %w", err)
		}
		if _, err := fmt.Fprintln(stdout, sidecar); err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
	}
	if err := export.AtomicWrite(path, res.RawJSON, 0o600); err != nil {
		// Best-effort sidecar cleanup when data write fails after sidecar
		// already landed. The pair is broken either way; remove the
		// misleading sidecar so the user does not trust it.
		if emitSidecar {
			_ = os.Remove(path + ".meta.json")
		}
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if _, err := fmt.Fprintln(stdout, path); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// resolveExplainSQL reads SQL from positional args first; if empty, drains
// stdin. Returns the trimmed string so the empty-SQL check in the caller
// works on a normalized value.
func resolveExplainSQL(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if stdin == nil {
		return "", nil
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(b), nil
}

// explainRunConnection opens a MySQL pool and runs EXPLAIN via the
// internal/explain package. The driver is locked to MySQL — EXPLAIN
// FORMAT=JSON output is MySQL-specific and DDL/MySQL semantics in the
// README forbid SQLite for the product surface (see Plan 006).
func explainRunConnection(ctx context.Context, conn config.Connection, sqlText string, mode explain.Mode) (explain.Result, error) {
	if conn.Driver == "sqlite" {
		// Hard refuse: SQLite's EXPLAIN is a bytecode dump, not a query
		// plan. Plan 009 explicitly says "use the existing internal/mysql
		// driver". We surface the constraint instead of silently emitting
		// garbage.
		return explain.Result{}, fmt.Errorf("explain: sqlite driver is not supported (use a MySQL connection)")
	}
	database, err := mysql.Open(ctx, conn)
	if err != nil {
		return explain.Result{}, fmt.Errorf("connect: %w", err)
	}
	defer database.Close()
	return explain.Run(ctx, database, sqlText, mode)
}