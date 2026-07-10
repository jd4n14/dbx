package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/danger"
)

func TestRunDangerResults(t *testing.T) {
	cfg := writeTempConfig(t, `
connections:
  production:
    driver: mysql
    dsn: "user:secret@tcp(unreachable.invalid:3306)/db"
    env: prod
  replica:
    driver: mysql
    dsn: "user:secret@tcp(unreachable.invalid:3306)/db"
    env: readonly
`)
	tests := []struct {
		name                string
		args                []string
		sql, severity, code string
		safe                bool
	}{
		{"safe", nil, "SELECT 1", "safe", "", true},
		{"delete", nil, "DELETE FROM orders", "critical", "delete_without_where", false},
		{"update where", nil, "UPDATE orders SET x=1 WHERE id=2", "warning", "write_statement", false},
		{"multiple", nil, "SELECT 1; SELECT 2", "critical", "multiple_statements", false},
		{"prod escalation", []string{"--conn", "production", "--config", cfg}, "INSERT INTO t VALUES (1)", "critical", "restricted_environment_write", false},
		{"readonly escalation", []string{"--conn", "replica", "--config", cfg}, "UPDATE t SET x=1 WHERE id=2", "critical", "restricted_environment_write", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := runDangerCmd(tc.args, strings.NewReader(tc.sql), &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			var got danger.Result
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("JSON: %v\n%s", err, stdout.String())
			}
			if got.Type != "danger" || got.Safe != tc.safe || string(got.Severity) != tc.severity {
				t.Fatalf("result=%+v", got)
			}
			if tc.code != "" && !dangerHasCode(got, tc.code) {
				t.Fatalf("missing %s: %+v", tc.code, got)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr=%q", stderr.String())
			}
			if !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) {
				t.Fatal("pretty JSON must end in newline")
			}
		})
	}
}

func TestRunDangerErrorsHaveEmptyStdout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		sql  string
	}{
		{"empty", nil, "  -- nothing"},
		{"bad flag", []string{"--nope"}, "SELECT 1"},
		{"config without conn", []string{"--config", "x"}, "SELECT 1"},
		{"missing config", []string{"--conn", "x", "--config", filepath.Join(t.TempDir(), "missing.yaml")}, "SELECT 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := runDangerCmd(tc.args, strings.NewReader(tc.sql), &stdout, &stderr); err == nil {
				t.Fatal("expected error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout=%q", stdout.String())
			}
		})
	}
}

func TestRunDangerUnknownConnection(t *testing.T) {
	cfg := writeTempConfig(t, testConnYAML)
	var stdout, stderr bytes.Buffer
	err := runDangerCmd([]string{"--conn", "missing", "--config", cfg}, strings.NewReader("SELECT 1"), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error=%v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func dangerHasCode(r danger.Result, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
