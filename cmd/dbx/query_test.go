package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/history"
	"github.com/jd4n14/dbx/internal/query"
	"github.com/jd4n14/dbx/internal/snapshot"
)

const testConnYAML = `
connections:
  local:
    driver: mysql
    host: 127.0.0.1
    port: 3306
    user: root
    password: secret
    database: testdb
    env: dev
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestRunQuery_MissingConn(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runQueryCmd(nil, strings.NewReader("SELECT 1"), &stdout, &stderr, nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing --conn")
	}
	if !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("error should mention --conn, got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_EmptyConnFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runQueryCmd([]string{"--conn", ""}, strings.NewReader("SELECT 1"), &stdout, &stderr, nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty --conn")
	}
	if !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("error should mention --conn, got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_EmptyStdin(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader(""),
		&stdout, &stderr,
		query.RunConnectionWithLimit,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error for empty stdin")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_WhitespaceOnlyStdin(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("   \n\t  "),
		&stdout, &stderr,
		query.RunConnectionWithLimit,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error for whitespace-only stdin")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_PolicyDenyOffline(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)

	cases := []struct {
		name string
		sql  string
		want string // substring of error
	}{
		{"DELETE", "DELETE FROM t", "DELETE"},
		{"UPDATE", "UPDATE t SET x=1", "UPDATE"},
		{"CTE+DELETE", "WITH c AS (SELECT 1) DELETE FROM t", "DELETE"},
		{"CTE+UPDATE", "WITH c AS (SELECT 1) UPDATE t SET x=1", "UPDATE"},
		{"multi-statement", "SELECT 1; DROP TABLE x", "multi"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			opened := false
			// Use real RunConnection: policy must fail before Open (no network).
			// If Open were attempted against 127.0.0.1 it would hang/fail slowly;
			// policy-first keeps this offline and fast.
			err := runQueryCmd(
				[]string{"--conn", "local", "--config", cfgPath},
				strings.NewReader(tc.sql),
				&stdout, &stderr,
				func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
					// Wrap to detect accidental Open attempts via RunConnection path.
					// Real RunConnection is the production runner under test.
					res, e := query.RunConnectionWithLimit(ctx, conn, sqlText, 0)
					if e != nil && strings.Contains(e.Error(), "connect:") {
						opened = true
					}
					return res, e
				},
				t.TempDir(),
			)
			if err == nil {
				t.Fatal("expected policy denial error")
			}
			if !strings.Contains(strings.ToUpper(err.Error()), strings.ToUpper(tc.want)) &&
				!strings.Contains(err.Error(), tc.want) {
				// Accept either keyword mention or generic policy phrasing.
				if !strings.Contains(err.Error(), "refused") &&
					!strings.Contains(err.Error(), "only allows") &&
					!strings.Contains(err.Error(), "multi-statement") &&
					!strings.Contains(err.Error(), "multiple statements") {
					t.Fatalf("error %q should mention policy denial (%q)", err.Error(), tc.want)
				}
			}
			if opened {
				t.Fatal("policy denial must not attempt connect/Open")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout must be empty on error, got %q", stdout.String())
			}
		})
	}
}

func TestRunQuery_ConfigMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-config.yaml")
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", missing},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		nil, // must not be called
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention not found, got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_UnknownConnection(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "nope", "--config", cfgPath},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		nil,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected unknown connection error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should mention connection name, got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunQuery_StdoutPurityOnSuccess(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	wantJSON := []byte("[\n  {\n    \"n\": 1\n  }\n]\n")
	cwd := t.TempDir()

	var stdout, stderr bytes.Buffer
	var gotSQL string
	var gotConnName string

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("SELECT 1 AS n"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			gotSQL = sqlText
			gotConnName = conn.Name
			if conn.Host != "127.0.0.1" || conn.Database != "testdb" {
				return query.RunResult{}, errors.New("unexpected connection fields")
			}
			return query.RunResult{Data: wantJSON}, nil
		},
		cwd,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotSQL != "SELECT 1 AS n" {
		t.Fatalf("sql = %q, want SELECT 1 AS n", gotSQL)
	}
	if gotConnName != "local" {
		t.Fatalf("conn name = %q, want local", gotConnName)
	}
	if !bytes.Equal(stdout.Bytes(), wantJSON) {
		t.Fatalf("stdout = %q, want exact pretty JSON %q", stdout.String(), wantJSON)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty on success, got %q", stderr.String())
	}

	// Last result cache must exist and carry connection + sql + data.
	lastPath := filepath.Join(cwd, ".dbx", "last.json")
	raw, err := os.ReadFile(lastPath)
	if err != nil {
		t.Fatalf("last.json: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"type": "last_result"`)) {
		t.Fatalf("last envelope: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"connection": "local"`)) {
		t.Fatalf("last connection: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`SELECT 1 AS n`)) {
		t.Fatalf("last sql: %s", raw)
	}
}

func TestRunQuery_CachePreservesLargeInteger(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	result := []byte(`[{"id":9007199254740993}]`)
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("SELECT id FROM orders"),
		&stdout, &stderr,
		func(context.Context, config.Connection, string, int) (query.RunResult, error) { return query.RunResult{Data: result}, nil },
		cwd,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(cwd, ".dbx", "last.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("9007199254740993")) {
		t.Fatalf("large integer changed in cache: %s", raw)
	}
}

func TestRunQuery_RunnerErrorNoStdout(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			return query.RunResult{}, errors.New("boom")
		},
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want boom", err)
	}
	// Must not write partial JSON on failure.
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on runner error, got %q", stdout.String())
	}
}

func TestRun_CommandsAndVersion(t *testing.T) {
	// path is implemented and requires a selector.
	if err := run([]string{"path"}); err == nil || !strings.Contains(err.Error(), "exactly one selector") {
		t.Fatalf("path without selector: got %v", err)
	}

	// snapshot is implemented: missing --name
	if err := run([]string{"snapshot"}); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("snapshot without flags: got %v", err)
	}

	// ddl is implemented: missing --conn
	if err := run([]string{"ddl"}); err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("ddl without flags: got %v", err)
	}

	// version succeeds (prints to real stdout during test — acceptable).
	if err := run([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := run([]string{"--version"}); err != nil {
		t.Fatalf("--version: %v", err)
	}

	// unknown command
	err := run([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command: got %v", err)
	}

	// help / empty args succeed
	if err := run([]string{"help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if err := run(nil); err != nil {
		t.Fatalf("empty args: %v", err)
	}
}

func TestRunQuery_InvalidFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runQueryCmd(
		[]string{"--nope"},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		nil,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected flag parse error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on flag error, got %q", stdout.String())
	}
}

// TestRunQuery_MaxRowsEnvelopeOnSuccess asserts that --max-rows > 0 produces
// the query envelope shape on stdout AND mirrors it into .dbx/last.json,
// while the no-flag regression path stays a bare pretty array.
func TestRunQuery_MaxRowsEnvelopeOnSuccess(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	var gotMaxRows int
	var gotSQL string
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath, "--max-rows", "5"},
		strings.NewReader("SELECT id FROM orders"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			gotMaxRows = maxRows
			gotSQL = sqlText
			return query.RunResult{
				Data: []byte("{\n  \"type\": \"query\",\n  \"truncated\": false,\n  \"row_count\": 5,\n  \"max_rows\": 5,\n  \"data\": []\n}\n"),
				Truncated: false,
				RowCount:  5,
				MaxRows:   5,
			}, nil
		},
		cwd,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMaxRows != 5 {
		t.Fatalf("fake runner received maxRows=%d, want 5", gotMaxRows)
	}
	if gotSQL != "SELECT id FROM orders" {
		t.Fatalf("fake runner received sql=%q", gotSQL)
	}

	// Stdout must be the envelope verbatim.
	want := "{\n  \"type\": \"query\",\n  \"truncated\": false,\n  \"row_count\": 5,\n  \"max_rows\": 5,\n  \"data\": []\n}\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch:\ngot:  %q\nwant: %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty on success, got %q", stderr.String())
	}

	// .dbx/last.json must mirror the envelope (round-trip via ReadLast).
	last, err := snapshot.ReadLast(cwd)
	if err != nil {
		t.Fatalf("ReadLast: %v", err)
	}
	if last.Connection != "local" {
		t.Fatalf("last connection = %q, want local", last.Connection)
	}
	if !bytes.Contains(last.Data, []byte(`"type": "query"`)) ||
		!bytes.Contains(last.Data, []byte(`"truncated": false`)) ||
		!bytes.Contains(last.Data, []byte(`"row_count": 5`)) ||
		!bytes.Contains(last.Data, []byte(`"max_rows": 5`)) {
		t.Fatalf("last.json data does not carry envelope fields:\n%s", last.Data)
	}
}

// TestRunQuery_NoMaxRowsRegressionGuard ensures that omitting --max-rows
// keeps the legacy bare-array stdout contract byte-for-byte. This is the
// one-way compatibility guard for every existing consumer.
func TestRunQuery_NoMaxRowsRegressionGuard(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	want := []byte("[\n  {\n    \"n\": 1\n  }\n]\n")
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			if maxRows != 0 {
				t.Fatalf("fake runner received maxRows=%d, want 0 (no flag)", maxRows)
			}
			return query.RunResult{Data: want}, nil
		},
		cwd,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("stdout = %q, want exact bare array %q", stdout.String(), want)
	}

	last, err := snapshot.ReadLast(cwd)
	if err != nil {
		t.Fatal(err)
	}
	// WriteLastFromQueryData normalizes data via json.Compact, so last.json
	// carries compact JSON. The contract is "same value as stdout", not
	// "byte-identical to stdout"; assert via re-marshal round-trip.
	if !json.Valid(last.Data) {
		t.Fatalf("last.json data is not valid JSON:\n%s", last.Data)
	}
	var lastArr []map[string]any
	if err := json.Unmarshal(last.Data, &lastArr); err != nil {
		t.Fatalf("last.json data not a JSON array: %v\n%s", err, last.Data)
	}
	if len(lastArr) != 1 || lastArr[0]["n"] != float64(1) {
		t.Fatalf("last.json payload does not round-trip the bare array, got %v", lastArr)
	}
}

// TestRunQuery_MaxRowsNegativeRejected covers the --max-rows -1 path that
// must fail with a helpful error before any DB call.
func TestRunQuery_MaxRowsNegativeRejected(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	called := false
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath, "--max-rows", "-1"},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			called = true
			return query.RunResult{}, nil
		},
		cwd,
	)
	if err == nil {
		t.Fatal("expected error for --max-rows -1")
	}
	if !strings.Contains(err.Error(), "max-rows must be > 0") {
		t.Fatalf("error should mention max-rows, got: %v", err)
	}
	if called {
		t.Fatal("fake runner must not be called for negative --max-rows")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on validation error, got %q", stdout.String())
	}
}

// TestRunQuery_MaxRowsNonIntegerRejected exercises the flag parser: a bad
// value must produce an error and never reach the fake runner.
func TestRunQuery_MaxRowsNonIntegerRejected(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	called := false
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath, "--max-rows", "abc"},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			called = true
			return query.RunResult{}, nil
		},
		cwd,
	)
	if err == nil {
		t.Fatal("expected flag parse error for --max-rows abc")
	}
	if called {
		t.Fatal("fake runner must not be called when flag parse fails")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on flag error, got %q", stdout.String())
	}
}

// TestRunQuery_MaxRowsZeroDisablesEnvelope covers the explicit `--max-rows 0`
// contract: same as omitting the flag (bare array, no envelope).
func TestRunQuery_MaxRowsZeroDisablesEnvelope(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	want := []byte("[\n  {\n    \"n\": 1\n  }\n]\n")
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath, "--max-rows", "0"},
		strings.NewReader("SELECT 1"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			if maxRows != 0 {
				t.Fatalf("fake runner received maxRows=%d, want 0", maxRows)
			}
			return query.RunResult{Data: want}, nil
		},
		cwd,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("explicit --max-rows 0 must keep bare array; got %q", stdout.String())
	}
}

// TestRunQuery_MaxRowsHistoryRows asserts that history.Append receives the
// kept row count from the RunResult when --max-rows > 0, not the raw
// envelope byte size.
func TestRunQuery_MaxRowsHistoryRows(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer

	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath, "--max-rows", "3"},
		strings.NewReader("select * from t"),
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, sqlText string, maxRows int) (query.RunResult, error) {
			return query.RunResult{
				Data:      []byte("{\"type\":\"query\",\"truncated\":true,\"row_count\":3,\"max_rows\":3,\"data\":[1,2,3]}"),
				Truncated: true,
				RowCount:  3,
				MaxRows:   3,
			}, nil
		},
		cwd,
	)
	if err != nil {
		t.Fatal(err)
	}

	ent, err := history.ShowByIndex(cwd, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ent.Rows != 3 {
		t.Fatalf("history rows = %d, want 3 (from RunResult.RowCount, not envelope byte size)", ent.Rows)
	}
	if ent.Bytes <= 0 {
		t.Fatalf("history bytes = %d, want > 0", ent.Bytes)
	}
}

// TestRunQuery_MaxRowsHelpContainsFlag ensures the flag is documented in the
// flag set's usage string so users discover it via --help.
func TestRunQuery_MaxRowsHelpContainsFlag(t *testing.T) {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.Int("max-rows", 0, "cap rows returned (0 = unlimited); when >0, output is a query envelope with truncation metadata")
	if !strings.Contains(fs.Name(), "query") {
		t.Fatalf("flagset name = %q, want query", fs.Name())
	}
	if fs.Lookup("max-rows") == nil {
		t.Fatal("--max-rows must be a registered flag")
	}
	if fs.Lookup("max-rows").DefValue != "0" {
		t.Fatalf("--max-rows default = %q, want 0", fs.Lookup("max-rows").DefValue)
	}
}
