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

func TestRunColumns_MissingFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(nil, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--conn") {
		t.Fatalf("want --conn error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runColumnsCmd([]string{"--conn", "local"}, &stdout, &stderr, nil)
	if err == nil || !strings.Contains(err.Error(), "--table") {
		t.Fatalf("want --table error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunColumns_InvalidTableRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "a.b", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
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

func TestRunColumns_InvalidLikeRejectsBeforeFetch(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	called := false
	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "orders", "--like", "id;DROP", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
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

func TestRunColumns_TSVStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
			if conn.Name != "local" || table != "orders" {
				t.Fatalf("conn=%q table=%q", conn.Name, table)
			}
			if like != "" {
				t.Fatalf("like should be empty, got %q", like)
			}
			return []introspect.Column{
				{Field: "id", Type: "bigint(20)", Null: "NO", Key: "PRI", Default: nil, Extra: "auto_increment"},
				{Field: "status", Type: "varchar(32)", Null: "NO", Key: "", Default: "pending", Extra: ""},
				{Field: "created_at", Type: "datetime", Null: "NO", Key: "", Default: "current_timestamp", Extra: "DEFAULT_GENERATED"},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "field\ttype\tnull\tkey\tdefault\textra\n" +
		"id\tbigint(20)\tNO\tPRI\t\tauto_increment\n" +
		"status\tvarchar(32)\tNO\t\tpending\t\n" +
		"created_at\tdatetime\tNO\t\tcurrent_timestamp\tDEFAULT_GENERATED\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q\nwant %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

func TestRunColumns_JSON(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg, "--json"},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
			return []introspect.Column{
				{Field: "id", Type: "bigint(20)", Null: "NO", Key: "PRI", Default: nil, Extra: "auto_increment"},
				{Field: "status", Type: "varchar(32)", Null: "NO", Key: "", Default: "pending", Extra: ""},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var arr []introspect.Column
	if err := json.Unmarshal(stdout.Bytes(), &arr); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(arr) != 2 {
		t.Fatalf("len = %d", len(arr))
	}
	if arr[0].Field != "id" || arr[0].Default != nil {
		t.Fatalf("[0] = %+v", arr[0])
	}
	if arr[1].Default != "pending" {
		t.Fatalf("[1].Default = %v", arr[1].Default)
	}
	if stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
}

func TestRunColumns_LikeForwarded(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "orders", "--like", "id", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
			if table != "orders" || like != "id" {
				t.Fatalf("table=%q like=%q", table, like)
			}
			return []introspect.Column{
				{Field: "id", Type: "bigint(20)", Null: "NO", Key: "PRI", Default: nil, Extra: "auto_increment"},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunColumns_FetchErrorNoStdout(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(
		[]string{"--conn", "local", "--table", "orders", "--config", cfg},
		&stdout, &stderr,
		func(ctx context.Context, conn config.Connection, table, like string) ([]introspect.Column, error) {
			return []introspect.Column{{Field: "id"}}, errors.New("boom")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout: %q", stdout.String())
	}
}

func TestRunColumns_UnknownConn(t *testing.T) {
	t.Parallel()
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer

	err := runColumnsCmd(
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
