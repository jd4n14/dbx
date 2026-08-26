package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/db"
)

// pingFakeDB implements db.DB with a programmable PingContext. Mirrors the
// hand-rolled fake used by internal/introspect and internal/explain so the
// command-level tests stay free of mock libraries.
type pingFakeDB struct {
	pingErr error
}

func (f *pingFakeDB) PingContext(ctx context.Context) error { return f.pingErr }

func (f *pingFakeDB) QueryContext(ctx context.Context, q string, args ...any) (db.Rows, error) {
	return nil, errors.New("pingFakeDB: unexpected QueryContext")
}

func (f *pingFakeDB) Close() error { return nil }

const pingTestConnYAML = `
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

func TestRunPing_MissingConn(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPingCmd(nil, nil, &stdout, &stderr, "", nil)
	if err == nil || !strings.Contains(err.Error(), "--conn is required") {
		t.Fatalf("want --conn required error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunPing_EmptyConnFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPingCmd([]string{"--conn", ""}, nil, &stdout, &stderr, "", nil)
	if err == nil || !strings.Contains(err.Error(), "--conn is required") {
		t.Fatalf("want --conn required error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}

func TestRunPing_UnknownConnection(t *testing.T) {
	cfg := writeTempConfig(t, pingTestConnYAML)
	var stdout, stderr bytes.Buffer
	err := runPingCmd(
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

func TestRunPing_Success(t *testing.T) {
	cfg := writeTempConfig(t, pingTestConnYAML)
	var stdout, stderr bytes.Buffer

	var gotConn config.Connection
	err := runPingCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			gotConn = conn
			return &pingFakeDB{pingErr: nil}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotConn.Name != "local_wms" {
		t.Fatalf("resolved connection name: got %q, want local_wms", gotConn.Name)
	}
	if got := stdout.String(); got != "ok\n" {
		t.Fatalf("stdout = %q, want %q", got, "ok\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty on success, got %q", stderr.String())
	}
}

func TestRunPing_PingError(t *testing.T) {
	cfg := writeTempConfig(t, pingTestConnYAML)
	var stdout, stderr bytes.Buffer

	boom := errors.New("connection refused")
	err := runPingCmd(
		[]string{"--conn", "prod_ro", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return &pingFakeDB{pingErr: boom}, nil
		},
	)
	if err == nil {
		t.Fatal("expected ping error")
	}
	if !strings.Contains(err.Error(), "ping prod_ro:") {
		t.Fatalf("error should mention \"ping prod_ro:\", got %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error should preserve wrapped message, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on ping error, got %q", stdout.String())
	}
}

func TestRunPing_OpenError(t *testing.T) {
	cfg := writeTempConfig(t, pingTestConnYAML)
	var stdout, stderr bytes.Buffer

	openErr := errors.New("network unreachable")
	err := runPingCmd(
		[]string{"--conn", "local_wms", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			return nil, openErr
		},
	)
	if err == nil {
		t.Fatal("expected open error")
	}
	if !strings.Contains(err.Error(), "ping local_wms:") {
		t.Fatalf("error should mention \"ping local_wms:\", got %v", err)
	}
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Fatalf("error should preserve wrapped message, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on open error, got %q", stdout.String())
	}
}

func TestRunPing_SQLiteRejected(t *testing.T) {
	cfg := writeTempConfig(t, `
connections:
  tests:
    driver: sqlite
    dsn: file:dbx_ping_sqlite?mode=memory&cache=shared
    env: dev
`)
	var stdout, stderr bytes.Buffer
	called := false
	err := runPingCmd(
		[]string{"--conn", "tests", "--config", cfg},
		nil, &stdout, &stderr, "",
		func(ctx context.Context, conn config.Connection) (db.DB, error) {
			called = true
			return &pingFakeDB{}, nil
		},
	)
	if err == nil {
		t.Fatal("expected sqlite driver error")
	}
	if !strings.Contains(err.Error(), "ping only supports mysql") {
		t.Fatalf("error should mention ping-only-supports-mysql, got %v", err)
	}
	if called {
		t.Fatal("CLI must reject sqlite before opening a connection")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty for rejected connection, got %q", stdout.String())
	}
}

func TestRunPing_ConfigMissing(t *testing.T) {
	missing := writeTempConfig(t, "") // dir, then non-existent path
	// writeTempConfig wrote the file; we want a non-existent path under that dir
	// (and the parent directory exists) so FindConfigPath returns a clean "not found".
	_ = missing
	bogus := "/tmp/dbx-ping-no-such-config.yaml"
	var stdout, stderr bytes.Buffer
	err := runPingCmd(
		[]string{"--conn", "local_wms", "--config", bogus},
		nil, &stdout, &stderr, "", nil,
	)
	if err == nil {
		t.Fatal("expected missing-config error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention not found, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error, got %q", stdout.String())
	}
}