package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/history"
)

func TestRunHistory_MissingSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd(nil, strings.NewReader(""), &stdout, &stderr, t.TempDir(), true)
	if err == nil {
		t.Fatal("expected error for missing subcommand")
	}
}

func TestRunHistory_List(t *testing.T) {
	cwd := t.TempDir()
	for _, e := range []history.Entry{
		entryAt("2026-07-15T10:00:00Z", "local_wms", "select 1", 1),
		entryAt("2026-07-15T11:00:00Z", "local_wms", "select 2", 1),
		entryAt("2026-07-15T12:00:00Z", "local_wms", "select 3", 1),
	} {
		if err := history.Append(cwd, e, 0); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"list"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines=%d body=%q", len(lines), stdout.String())
	}
	// newest first
	if !strings.HasPrefix(lines[0], "1\t") {
		t.Fatalf("first line should start with idx 1, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "select 3") {
		t.Fatalf("first line should hold newest sql, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "local_wms") {
		t.Fatalf("missing connection: %q", lines[0])
	}
}

func TestRunHistory_ListJSON(t *testing.T) {
	cwd := t.TempDir()
	if err := history.Append(cwd, entryAt("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"list", "--json"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Index     int    `json:"index"`
		SQL       string `json:"sql"`
		Connection string `json:"connection"`
	}
	if err := json.Unmarshal(bytesTrim(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 || got.SQL != "select 1" || got.Connection != "c" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunHistory_ListWithLimit(t *testing.T) {
	cwd := t.TempDir()
	for i, s := range []string{"a", "b", "c"} {
		ts := time.Date(2026, 7, 15, 10, i, 0, 0, time.UTC).Format(time.RFC3339)
		if err := history.Append(cwd, entryAt(ts, "c", "select "+s, 1), 0); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"list", "--limit", "2"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("limit not honored, got %d lines", len(lines))
	}
}

func TestRunHistory_Show(t *testing.T) {
	cwd := t.TempDir()
	if err := history.Append(cwd, entryAt("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := history.Append(cwd, entryAt("2026-07-15T11:00:00Z", "c", "select 2", 1), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"show", "1"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(stdout.String(), "\n") != "select 2" {
		t.Fatalf("show 1 should print newest sql, got %q", stdout.String())
	}
}

func TestRunHistory_ShowJSON(t *testing.T) {
	cwd := t.TempDir()
	if err := history.Append(cwd, entryAt("2026-07-15T11:00:00Z", "local_wms", "select 2", 1), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"show", "--json", "1"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Index int `json:"index"`
		history.Entry
	}
	if err := json.Unmarshal(bytesTrim(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 || got.SQL != "select 2" || got.Connection != "local_wms" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunHistory_ShowOutOfRange(t *testing.T) {
	cwd := t.TempDir()
	if err := history.Append(cwd, entryAt("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd([]string{"show", "5"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("got %v", err)
	}
}

func TestRunHistory_ShowInvalidIndex(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd([]string{"show", "zero"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("got %v", err)
	}
	// -1 is consumed by the flag parser as a non-defined flag, which is also
	// a clear error path.
	err = runHistoryCmd([]string{"show", "-1"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil {
		t.Fatalf("expected error for -1 index")
	}
}

func TestRunHistory_ShowMissingArgument(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd([]string{"show"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "requires an index") {
		t.Fatalf("got %v", err)
	}
}

func TestRunHistory_EmptyFile(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	// list: nothing on stdout, no error
	if err := runHistoryCmd([]string{"list"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatalf("list on empty: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("empty list should print nothing, got %q", stdout.String())
	}
	// show: error explaining no history
	err := runHistoryCmd([]string{"show", "1"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "no history") {
		t.Fatalf("got %v", err)
	}
}

func TestRunHistory_Clear(t *testing.T) {
	cwd := t.TempDir()
	if err := history.Append(cwd, entryAt("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"clear"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(history.Path(cwd)); !os.IsNotExist(err) {
		t.Fatalf("history still exists: %v", err)
	}
	// clear on missing file should be silent
	if err := runHistoryCmd([]string{"clear"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatalf("clear of missing file must be ok, got %v", err)
	}
}

func TestRunHistory_ClearRejectsArgs(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd([]string{"clear", "extra"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "no arguments") {
		t.Fatalf("got %v", err)
	}
}

func TestRunHistory_UnknownSubcommand(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runHistoryCmd([]string{"nope"}, strings.NewReader(""), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("got %v", err)
	}
}

func TestRunQuery_AppendsHistoryOnSuccess(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("select 1 as n, 2 as n"),
		&stdout, &stderr,
		func(context.Context, config.Connection, string) ([]byte, error) {
			return []byte(`[{"n":1},{"n":2}]`), nil
		},
		cwd,
	); err != nil {
		t.Fatal(err)
	}

	if n := history.Count(cwd); n != 1 {
		t.Fatalf("history count = %d, want 1", n)
	}
	ent, err := history.ShowByIndex(cwd, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ent.Connection != "local" || ent.SQL != "select 1 as n, 2 as n" {
		t.Fatalf("entry %+v", ent)
	}
	if ent.Rows != 2 {
		t.Fatalf("rows = %d, want 2", ent.Rows)
	}
	if ent.Bytes <= 0 {
		t.Fatalf("bytes = %d, want > 0", ent.Bytes)
	}
	if ent.DurationMs < 0 {
		t.Fatalf("duration = %d, want >= 0", ent.DurationMs)
	}
}

func TestRunQuery_AppendsHistorySingleObjectRow(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("select 1 as n"),
		&stdout, &stderr,
		func(context.Context, config.Connection, string) ([]byte, error) {
			return []byte(`{"n":1}`), nil
		},
		cwd,
	); err != nil {
		t.Fatal(err)
	}
	ent, _ := history.ShowByIndex(cwd, 1)
	if ent.Rows != 1 {
		t.Fatalf("rows = %d, want 1 (single-object fallback)", ent.Rows)
	}
}

func TestRunQuery_NoHistoryOnFailure(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("select 1"),
		&stdout, &stderr,
		func(context.Context, config.Connection, string) ([]byte, error) {
			return nil, errors.New("boom")
		},
		cwd,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if n := history.Count(cwd); n != 0 {
		t.Fatalf("history count after failure = %d, want 0", n)
	}
}

func TestRunQuery_HistoryAppendFailureWarnsButDoesNotFail(t *testing.T) {
	cfgPath := writeTempConfig(t, testConnYAML)
	cwd := t.TempDir()
	// Force history append to fail while keeping snapshot/last.json happy:
	// the history.jsonl path must be unwritable, but the surrounding .dbx
	// dir stays writable so last.json + atomic temp writes can complete.
	//
	// Make <cwd>/.dbx pre-created (last.json + dir creation succeed).
	dbxDir := filepath.Join(cwd, ".dbx")
	if err := os.MkdirAll(dbxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory at history.jsonl makes the OpenFile(O_CREATE) fail with EISDIR.
	histDir := filepath.Join(dbxDir, "history.jsonl")
	if err := os.MkdirAll(histDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := runQueryCmd(
		[]string{"--conn", "local", "--config", cfgPath},
		strings.NewReader("select 1"),
		&stdout, &stderr,
		func(context.Context, config.Connection, string) ([]byte, error) {
			return []byte(`[{"n":1}]`), nil
		},
		cwd,
	); err != nil {
		t.Fatalf("query must succeed even when history append fails, got %v", err)
	}
	if !strings.Contains(stderr.String(), "history append failed") {
		t.Fatalf("expected warn on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"n":1`) {
		t.Fatalf("stdout should still carry JSON, got %q", stdout.String())
	}
}

func TestRunHistory_DefaultPrivateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode semantics differ on Windows")
	}
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := runHistoryCmd([]string{"list"}, strings.NewReader(""), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	// Append a single entry, then assert perms on the created dirs/file.
	if err := history.Append(cwd, entryAt("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(cwd, ".dbx"):                       0o700,
		filepath.Join(cwd, ".dbx", "history.jsonl"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("mode for %s = %o want %o", path, got, want)
		}
	}
}

func entryAt(ts, conn, sql string, rows int) history.Entry {
	t, _ := time.Parse(time.RFC3339, ts)
	return history.Entry{
		Type:       history.Type,
		Timestamp:  t,
		Connection: conn,
		SQL:        sql,
		Rows:       rows,
		Bytes:      len(sql),
		DurationMs: 5,
	}
}
