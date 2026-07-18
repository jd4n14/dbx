package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
)

func TestRunTables_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTables_InvalidSchemaRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runTablesCmd(
		[]string{"--conn", "local", "--schema", "bogus'--", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
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
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTables_InvalidLikeRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runTablesCmd(
		[]string{"--conn", "local", "--like", "ord;DROP", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
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

func TestRunTables_TextStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "local", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
			if conn.Name != "local" {
				t.Fatalf("conn = %q", conn.Name)
			}
			if schema != "" || like != "" {
				t.Fatalf("expected empty schema/like, got schema=%q like=%q", schema, like)
			}
			return []string{"orders", "order_items", "shipments"}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "orders\norder_items\nshipments\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunTables_TextEmptyResultIsEmptyStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "local", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
			return []string{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunTables_JSON(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "local", "--config", cfg, "--json"},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
			return []string{"orders", "order_items"}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var arr []string
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(arr) != 2 || arr[0] != "orders" || arr[1] != "order_items" {
		t.Fatalf("arr = %v", arr)
	}
	if stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
}

func TestRunTables_SchemaAndLikeForwarded(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "local", "--config", cfg, "--schema", "audit", "--like", "events"},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
			if schema != "audit" || like != "events" {
				t.Fatalf("got schema=%q like=%q", schema, like)
			}
			return []string{"events"}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "events\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunTables_FetchErrorNoStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "local", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, schema, like string) ([]string, error) {
			return []string{"orders"}, errors.New("boom")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTables_UnknownConn(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTablesCmd(
		[]string{"--conn", "missing", "--config", cfg},
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
