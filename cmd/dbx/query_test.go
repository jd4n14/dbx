package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/query"
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
		query.RunConnection,
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
		query.RunConnection,
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
				func(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error) {
					// Wrap to detect accidental Open attempts via RunConnection path.
					// Real RunConnection is the production runner under test.
					out, e := query.RunConnection(ctx, conn, sqlText)
					if e != nil && strings.Contains(e.Error(), "connect:") {
						opened = true
					}
					return out, e
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
		func(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error) {
			gotSQL = sqlText
			gotConnName = conn.Name
			if conn.Host != "127.0.0.1" || conn.Database != "testdb" {
				return nil, errors.New("unexpected connection fields")
			}
			return wantJSON, nil
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
		func(context.Context, config.Connection, string) ([]byte, error) { return result, nil },
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
		func(ctx context.Context, conn config.Connection, sqlText string) ([]byte, error) {
			return []byte("partial"), errors.New("boom")
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

func TestRun_StubsAndVersion(t *testing.T) {
	// Remaining stubs.
	for _, cmd := range []string{"path", "danger"} {
		err := run([]string{cmd})
		if err == nil || !strings.Contains(err.Error(), "not implemented") {
			t.Fatalf("command %q: want not implemented, got %v", cmd, err)
		}
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
