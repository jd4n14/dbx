package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/query"
	"github.com/jd4n14/dbx/internal/snapshot"
)

// connectBudget is added to the query timeout so dial/ping can finish
// before the overall CLI context expires.
const connectBudget = 15 * time.Second

// runConnectionFunc executes SQL against a resolved connection.
// Production uses query.RunConnection; tests inject fakes for stdout purity.
type runConnectionFunc func(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error)

func runQuery(args []string) error {
	return runQueryCmd(args, os.Stdin, os.Stdout, os.Stderr, query.RunConnection, "")
}

// runQueryCmd implements `dbx query`:
//
//	--conn   required named connection
//	--config optional config path (else discovery / DBX_CONFIG)
//
// SQL is read fully from stdin. On success, the result is cached as
// .dbx/last.json under cwd, then pretty JSON rows are written only to stdout.
// Policy runs inside runConn before any Open (see query.RunConnection).
//
// cwd is the project root for last-result storage; empty uses os.Getwd().
func runQueryCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, runConn runConnectionFunc, cwd string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
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

	sqlBytes, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	sqlText := string(sqlBytes)

	// Overall budget: connect (DSN dial/ping) + default query timeout.
	ctx, cancel := context.WithTimeout(context.Background(), query.DefaultQueryTimeout+connectBudget)
	defer cancel()

	if runConn == nil {
		runConn = query.RunConnection
	}

	out, err := runConn(ctx, conn, sqlText)
	if err != nil {
		return err
	}

	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}

	// Cache last result before stdout so failures leave stdout empty.
	if err := snapshot.WriteLastFromQueryData(cwd, strings.TrimSpace(*connName), sqlText, out); err != nil {
		return err
	}

	// Write JSON only after full success (stdout purity).
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
