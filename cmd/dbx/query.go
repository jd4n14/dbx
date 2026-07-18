package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/history"
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
// .dbx/last.json under cwd, the lightweight .dbx/history.jsonl entry is
// appended, then pretty JSON rows are written only to stdout.
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

	startedAt := time.Now()
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
	resolvedConn := strings.TrimSpace(*connName)
	if err := snapshot.WriteLastFromQueryData(cwd, resolvedConn, sqlText, out); err != nil {
		return err
	}

	// Append a lightweight history entry so users can re-run recent
	// successful queries. History failures must not break the user-visible
	// success path — they only warn on stderr.
	if err := history.Append(cwd, history.Entry{
		Timestamp:  startedAt.UTC(),
		Connection: resolvedConn,
		SQL:        sqlText,
		Rows:       countJSONRows(out),
		Bytes:      len(out),
		DurationMs: time.Since(startedAt).Milliseconds(),
	}, 0); err != nil {
		fmt.Fprintf(stderr, "warn: history append failed: %v\n", err)
	}

	// Write JSON only after full success (stdout purity).
	if _, err := stdout.Write(out); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

// countJSONRows returns the number of rows encoded in pretty/compact JSON
// emitted by dbx query. Accepts an array form (the common case) or a single
// object (treated as 1 row). Anything else returns 0.
func countJSONRows(data []byte) int {
	trimmed := bytesTrim(data)
	if len(trimmed) == 0 {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err == nil {
		return len(arr)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		return 1
	}
	return 0
}

// bytesTrim trims ASCII whitespace from both ends without allocating a copy.
func bytesTrim(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
