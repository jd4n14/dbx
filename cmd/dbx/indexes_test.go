package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/introspect"
)

// testConnWithSQLiteYAML mirrors testConnYAML but adds an `offline`
// connection with driver sqlite so the non-mysql driver rejection path
// can be exercised without mutating the shared helper.
const testConnWithSQLiteYAML = `
connections:
  local:
    driver: mysql
    host: 127.0.0.1
    port: 3306
    user: root
    password: secret
    database: testdb
    env: dev
  offline:
    driver: sqlite
    dsn: "file:dbx_test?mode=memory&cache=shared"
    env: dev
`

func TestRunIndexes_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	err := runIndexesCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runIndexesCmd([]string{"--conn", "local"}, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--table") {
		t.Fatalf("want --table error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunIndexes_InvalidTableRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runIndexesCmd(
		[]string{"--conn", "local", "--table", "a.b", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error) {
			called = true
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before fetch")
	}
}

func TestRunIndexes_NonMysqlDriverRejected(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnWithSQLiteYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runIndexesCmd(
		[]string{"--conn", "offline", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error) {
			called = true
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "indexes only supports mysql") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("driver check must reject before fetch")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunIndexes_PrettyJSONStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runIndexesCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error) {
			if conn.Name != "local" || table != "orders" {
				t.Fatalf("conn=%q table=%q", conn.Name, table)
			}
			return []introspect.Index{
				{Name: "PRIMARY", NonUnique: false, SeqInIndex: 1, ColumnName: "id", Collation: "A", Cardinality: 1234, IndexType: "BTREE"},
				{Name: "idx_status", NonUnique: true, SeqInIndex: 1, ColumnName: "status", Collation: "A", Cardinality: 3, IndexType: "BTREE"},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var arr []introspect.Index
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(arr) != 2 {
		t.Fatalf("len = %d", len(arr))
	}
	if arr[0].Name != "PRIMARY" || arr[0].NonUnique {
		t.Fatalf("[0] = %+v", arr[0])
	}
	if arr[1].Name != "idx_status" || !arr[1].NonUnique {
		t.Fatalf("[1] = %+v", arr[1])
	}
	if stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunIndexes_EmptyResultEmitsJSONArray(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runIndexesCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error) {
			return []introspect.Index{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "[]" {
		t.Fatalf("empty result should emit [], got %q", got)
	}
}

func TestRunIndexes_FetchErrorNoStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runIndexesCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.Index, error) {
			return nil, errors.New("boom")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunIndexes_UnknownConn(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runIndexesCmd(
		[]string{"--conn", "missing", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		nil,
	)
	if err == nil {
		t.Fatal("want error for unknown connection")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}
