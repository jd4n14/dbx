package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/ddl"
)

func TestRunDDL_MissingFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runDDLCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runDDLCmd([]string{"--conn", "local"}, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--table") {
		t.Fatalf("want --table error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunDDL_SQLStdout(t *testing.T) {
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	ddlText := "CREATE TABLE `orders` (`id` int)"
	err := runDDLCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (string, error) {
			if conn.Name != "local" || table != "orders" {
				t.Fatalf("conn=%q table=%q", conn.Name, table)
			}
			return ddlText, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if got != ddlText+"\n" {
		t.Fatalf("stdout = %q, want %q", got, ddlText+"\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunDDL_JSON(t *testing.T) {
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	ddlText := "CREATE TABLE `orders` (`id` int)"
	err := runDDLCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg, "--json"},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (string, error) {
			return ddlText, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var r ddl.Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if r.Type != "ddl" || r.Connection != "local" || r.Dialect != "mysql" || r.Table != "orders" || r.DDL != ddlText {
		t.Fatalf("result: %+v", r)
	}
	if len(stdout.Bytes()) == 0 || stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
}

func TestRunDDL_FetchErrorNoStdout(t *testing.T) {
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	err := runDDLCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (string, error) {
			return "partial", errors.New("boom")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty, got %q", stdout.String())
	}
}

func TestRunDDL_InvalidTable(t *testing.T) {
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runDDLCmd(
		[]string{"--conn", "local", "--table", "a.b", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (string, error) {
			called = true
			return "", nil
		},
	)
	if err == nil {
		t.Fatal("expected invalid table error")
	}
	if called {
		t.Fatal("CLI should validate table name before fetch")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

// TestRunDDL_NonMySQLRejectedBeforeFetch pins the SQLite-only-for-tests
// boundary: configuring a connection with driver: sqlite must NOT cause
// `dbx ddl` to fall through to ddl.FetchConnection. The fetch fake must
// never be invoked, and stdout must remain empty.
func TestRunDDL_NonMySQLRejectedBeforeFetch(t *testing.T) {
	cfg := writeTempConfig(t, `
connections:
  tests:
    driver: sqlite
    dsn: file:dbx_ddl_reject?mode=memory&cache=shared
    env: dev
`)
	var stdout, stderr bytes.Buffer
	err := runDDLCmd(
		[]string{"--conn", "tests", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (string, error) {
			t.Fatalf("fetch must not be called for non-mysql driver; got conn=%q table=%q", conn.Name, table)
			return "", nil
		},
	)
	if err == nil {
		t.Fatal("expected non-mysql driver error")
	}
	if !strings.Contains(err.Error(), "ddl only supports mysql") {
		t.Errorf("error should mention ddl-only-supports-mysql, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty for rejected connection, got %q", stdout.String())
	}
}
