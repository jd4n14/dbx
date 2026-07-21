package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/introspect"
)

func TestRunTableSize_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	err := runTableSizeCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runTableSizeCmd([]string{"--conn", "local"}, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--table") {
		t.Fatalf("want --table error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTableSize_InvalidTableRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runTableSizeCmd(
		[]string{"--conn", "local", "--table", "a.b", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (introspect.TableSize, error) {
			called = true
			return introspect.TableSize{}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before fetch")
	}
}

func TestRunTableSize_NonMysqlDriverRejected(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnWithSQLiteYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runTableSizeCmd(
		[]string{"--conn", "offline", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (introspect.TableSize, error) {
			called = true
			return introspect.TableSize{}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "table-size only supports mysql") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("driver check must reject before fetch")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTableSize_PrettyJSONStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 7, 21, 22, 0, 0, 0, time.UTC)

	err := runTableSizeCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (introspect.TableSize, error) {
			return introspect.TableSize{
				Rows: 1234, DataBytes: 16384, IndexBytes: 4096, DataFreeBytes: 0,
				AutoIncrement: 1235, Collation: "utf8mb4_unicode_ci",
				CreateTime: created, UpdateTime: updated, Engine: "InnoDB",
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var got introspect.TableSize
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if got.Rows != 1234 || got.Engine != "InnoDB" || !got.CreateTime.Equal(created) {
		t.Fatalf("got %+v", got)
	}
	if stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunTableSize_TableNotFoundFriendlyError(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTableSizeCmd(
		[]string{"--conn", "local", "--table", "ghost", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (introspect.TableSize, error) {
			return introspect.TableSize{}, introspect.ErrTableNotFound
		},
	)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTableSize_FetchErrorNoStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTableSizeCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table string) (introspect.TableSize, error) {
			return introspect.TableSize{}, errors.New("boom")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunTableSize_UnknownConn(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runTableSizeCmd(
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
