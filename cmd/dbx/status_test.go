package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
)

// statusFakeDB implements db.DB with a programmable QueryContext so tests can
// return canned rows for SELECT VERSION() and SELECT @@SESSION.sql_mode. It
// mirrors the hand-rolled fake used by internal/introspect and internal/explain.
type statusFakeDB struct {
	versionRow []any // single cell returned for "SELECT VERSION()"
	sqlModeRow []any // single cell returned for "SELECT @@SESSION.sql_mode"
	queryErr   error // returned by QueryContext when non-nil
	lastSQLs   []string
}

func (f *statusFakeDB) PingContext(ctx context.Context) error { return nil }

func (f *statusFakeDB) QueryContext(ctx context.Context, q string, args ...any) (db.Rows, error) {
	f.lastSQLs = append(f.lastSQLs, q)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	var row []any
	switch {
	case strings.Contains(q, "VERSION()"):
		row = f.versionRow
	case strings.Contains(q, "@@SESSION.sql_mode"):
		row = f.sqlModeRow
	default:
		return nil, errors.New("statusFakeDB: unexpected SQL: " + q)
	}
	return &statusFakeRows{cols: []string{"v"}, data: [][]any{row}}, nil
}

func (f *statusFakeDB) Close() error { return nil }

// statusFakeRows is a minimal db.Rows over in-memory data; only Next/Scan/
// Columns/Err/Close are exercised by fetchStatus.
type statusFakeRows struct {
	cols   []string
	data   [][]any
	i      int
	closed bool
	err    error
}

func (r *statusFakeRows) Columns() ([]string, error) {
	if r.closed {
		return nil, errors.New("closed")
	}
	return r.cols, nil
}

func (r *statusFakeRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *statusFakeRows) Scan(dest ...any) error {
	if r.closed {
		return errors.New("closed")
	}
	if r.i == 0 || r.i > len(r.data) {
		return errors.New("Scan without Next")
	}
	row := r.data[r.i-1]
	for i := range dest {
		p, ok := dest[i].(*any)
		if !ok {
			return errors.New("dest not *any")
		}
		if i < len(row) {
			*p = row[i]
		} else {
			*p = nil
		}
	}
	return nil
}

func (r *statusFakeRows) Err() error { return r.err }

func (r *statusFakeRows) Close() error {
	r.closed = true
	return nil
}

const statusTestConnYAML = `
connections:
  local_wms:
    driver: mysql
    host: 127.0.0.1
    port: 3306
    user: root
    password: secret
    database: testdb
    env: dev
  prod_ro:
    driver: mysql
    host: 127.0.0.1
    port: 3306
    user: root
    password: secret
    database: testdb
    env: readonly
`

func TestRunStatus_MissingConn(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runStatusCmd(nil, nil, &stdout, &stderr, "", nil)
	if err == nil || !strings.Contains(err.Error(), "--conn is required") {
		t.Fatalf("want --conn required error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunStatus_UnknownConnection(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer
	err := runStatusCmd(
		[]string{"--conn", "nope", "--config", cfg},
		nil, &stdout, &stderr, "", nil,
	)
	if err == nil {
		t.Fatal("expected unknown-connection error")
	}
	if !strings.Contains(err.Error(), `connection "nope" not found`) {
		t.Fatalf("error should mention connection \"nope\" not found, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunStatus_TextSuccess(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	fdb := &statusFakeDB{
		versionRow: []any{"8.0.36"},
		sqlModeRow: []any{"ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES"},
	}
	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return fdb, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	line := strings.TrimRight(stdout.String(), "\n")
	if !strings.Contains(line, "local_wms") {
		t.Errorf("line missing connection name: %q", line)
	}
	if !strings.Contains(line, "dev") {
		t.Errorf("line missing env label: %q", line)
	}
	if !strings.Contains(line, "mysql") {
		t.Errorf("line missing driver: %q", line)
	}
	if !strings.Contains(line, "8.0.36") {
		t.Errorf("line missing server_version: %q", line)
	}
	if !strings.Contains(line, "sql_mode=") {
		t.Errorf("line missing sql_mode when non-empty: %q", line)
	}
	if !strings.Contains(line, "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES") {
		t.Errorf("line missing sql_mode value: %q", line)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty on success, got %q", stderr.String())
	}
}

func TestRunStatus_TextSuccessEmptySQLMode(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	fdb := &statusFakeDB{
		versionRow: []any{"8.0.36"},
		sqlModeRow: []any{""}, // empty string is valid; must not render sql_mode=
	}
	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return fdb, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	line := strings.TrimRight(stdout.String(), "\n")
	if strings.Contains(line, "sql_mode=") {
		t.Errorf("line should not contain sql_mode= when empty: %q", line)
	}
}

func TestRunStatus_JSONSuccess(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	fdb := &statusFakeDB{
		versionRow: []any{"8.0.36"},
		sqlModeRow: []any{"ONLY_FULL_GROUP_BY"},
	}
	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg, "--json"},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return fdb, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got statusEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json unmarshal: %v\n%s", err, stdout.String())
	}
	if got.Type != "status" {
		t.Errorf("type: got %q, want status", got.Type)
	}
	if got.Connection != "local_wms" {
		t.Errorf("connection: got %q, want local_wms", got.Connection)
	}
	if got.Driver != "mysql" {
		t.Errorf("driver: got %q, want mysql", got.Driver)
	}
	if got.Env != "dev" {
		t.Errorf("env: got %q, want dev", got.Env)
	}
	if got.ServerVersion != "8.0.36" {
		t.Errorf("server_version: got %q, want 8.0.36", got.ServerVersion)
	}
	if got.SQLMode != "ONLY_FULL_GROUP_BY" {
		t.Errorf("sql_mode: got %q, want ONLY_FULL_GROUP_BY", got.SQLMode)
	}
	if got.DBXVersion != Version {
		t.Errorf("dbx_version: got %q, want %q", got.DBXVersion, Version)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty on success, got %q", stderr.String())
	}
	// Pretty contract: trailing newline + indent.
	if len(stdout.Bytes()) == 0 || stdout.Bytes()[len(stdout.Bytes())-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	if !strings.Contains(stdout.String(), "\n  ") {
		t.Errorf("expected pretty-printed JSON, got %q", stdout.String())
	}
}

func TestRunStatus_QueryError(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	fdb := &statusFakeDB{queryErr: errors.New("connection lost")}
	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return fdb, nil
		},
	)
	if err == nil {
		t.Fatal("expected query error")
	}
	if !strings.Contains(err.Error(), "error: status:") && !strings.Contains(err.Error(), "status:") {
		t.Errorf("error should carry status: prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("error should preserve server message, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunStatus_OpenError(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return nil, errors.New("network unreachable")
		},
	)
	if err == nil {
		t.Fatal("expected open error")
	}
	if !strings.Contains(err.Error(), "status local_wms:") {
		t.Errorf("error should mention status local_wms:, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on open error, got %q", stdout.String())
	}
}

func TestRunStatus_SQLiteRejected(t *testing.T) {
	cfg := writeTempConfig(t, `
connections:
  tests:
    driver: sqlite
    dsn: file:dbx_status_sqlite?mode=memory&cache=shared
    env: dev
`)
	var stdout, stderr bytes.Buffer
	called := false
	err := runStatusCmd(
		[]string{"--conn", "tests", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			called = true
			return &statusFakeDB{}, nil
		},
	)
	if err == nil {
		t.Fatal("expected sqlite driver error")
	}
	if !strings.Contains(err.Error(), "status only supports mysql") {
		t.Errorf("error should mention status-only-supports-mysql, got %v", err)
	}
	if called {
		t.Fatal("CLI must reject sqlite before opening a connection")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty for rejected connection, got %q", stdout.String())
	}
}

func TestRunStatus_NoRows(t *testing.T) {
	cfg := writeTempConfig(t, statusTestConnYAML)
	var stdout, stderr bytes.Buffer

	// NoRowsDB returns zero rows for VERSION() so scanSingleString reports
	// "no rows returned". It is a hand-crafted variant because the default
	// fakeDB returns one row per query (with possibly-nil cells).
	noRowsDB := &noRowsForVersionFakeDB{}
	err := runStatusCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return noRowsDB, nil
		},
	)
	if err == nil {
		t.Fatal("expected no-rows error")
	}
	if !strings.Contains(err.Error(), "no rows returned") {
		t.Errorf("error should mention no rows returned, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

// noRowsForVersionFakeDB returns zero rows for SELECT VERSION() and a
// one-cell row for SELECT @@SESSION.sql_mode. Used by TestRunStatus_NoRows
// to exercise the "no rows returned" error path of scanSingleString.
type noRowsForVersionFakeDB struct {
	calls int
}

func (f *noRowsForVersionFakeDB) PingContext(ctx context.Context) error { return nil }

func (f *noRowsForVersionFakeDB) QueryContext(ctx context.Context, q string, args ...any) (db.Rows, error) {
	f.calls++
	if strings.Contains(q, "VERSION()") {
		return &statusFakeRows{cols: []string{"v"}, data: nil}, nil
	}
	return &statusFakeRows{cols: []string{"v"}, data: [][]any{{""}}}, nil
}

func (f *noRowsForVersionFakeDB) Close() error { return nil }