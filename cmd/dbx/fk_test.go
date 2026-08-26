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

func TestRunFK_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	err := runFKCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runFKCmd([]string{"--conn", "local"}, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--table") {
		t.Fatalf("want --table error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunFK_InvalidTableRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runFKCmd(
		[]string{"--conn", "local", "--table", "a.b", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.ForeignKey, error) {
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

func TestRunFK_NonMysqlDriverRejected(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnWithSQLiteYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runFKCmd(
		[]string{"--conn", "offline", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.ForeignKey, error) {
			called = true
			return nil, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "fk only supports mysql") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("driver check must reject before fetch")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunFK_PrettyJSONStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runFKCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.ForeignKey, error) {
			return []introspect.ForeignKey{
				{Name: "fk_orders_customer", Column: "customer_id", ReferencedSchema: "wms", ReferencedTable: "customers", ReferencedColumn: "id", UpdateRule: "RESTRICT", DeleteRule: "CASCADE"},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var arr []introspect.ForeignKey
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(arr) != 1 {
		t.Fatalf("len = %d", len(arr))
	}
	want := introspect.ForeignKey{
		Name: "fk_orders_customer", Column: "customer_id",
		ReferencedSchema: "wms", ReferencedTable: "customers", ReferencedColumn: "id",
		UpdateRule: "RESTRICT", DeleteRule: "CASCADE",
	}
	if arr[0] != want {
		t.Fatalf("got %+v want %+v", arr[0], want)
	}
	if stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunFK_EmptyResultEmitsJSONArray(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runFKCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.ForeignKey, error) {
			return []introspect.ForeignKey{}, nil
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

func TestRunFK_FetchErrorNoStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runFKCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) ([]introspect.ForeignKey, error) {
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

func TestRunFK_UnknownConn(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runFKCmd(
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
