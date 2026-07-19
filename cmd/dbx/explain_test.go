package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/explain"
)

// fakeExplainRunner returns a canned Result for tests, bypassing MySQL.
// It exposes a `runner` field that satisfies the explainRunner signature
// the production code expects.
type fakeExplainRunner struct {
	gotSQL  string
	gotMode explain.Mode
	gotConn string
	res     explain.Result
	err     error
}

func (f *fakeExplainRunner) runner(ctx context.Context, conn config.Connection, sqlText string, mode explain.Mode) (explain.Result, error) {
	f.gotSQL = sqlText
	f.gotMode = mode
	f.gotConn = conn.Name
	if f.err != nil {
		return explain.Result{}, f.err
	}
	return f.res, nil
}

// invokeExplain runs runExplainCmdWithRunner with the fake runner; cwd is
// isolated per test via t.TempDir().
func invokeExplain(t *testing.T, args []string, stdin string, fake *fakeExplainRunner) (stdout, stderr string, err error) {
	t.Helper()
	cwd := t.TempDir()
	var out, errOut bytes.Buffer
	in := strings.NewReader(stdin)
	err = runExplainCmdWithRunner(args, in, &out, &errOut, cwd, fake.runner)
	return out.String(), errOut.String(), err
}

// sampleTabular returns a hand-built tabular Result that mirrors what
// Run() would produce against a real MySQL EXPLAIN.
func sampleTabular() explain.Result {
	return explain.Result{
		Mode: explain.ModeTabular,
		Columns: []string{
			"id", "select_type", "table", "type", "possible_keys",
			"key", "key_len", "ref", "rows", "Extra",
		},
		Rows: [][]any{
			{int64(1), "SIMPLE", "orders", "ALL", nil, nil, nil, nil, int64(1234), "Using where"},
		},
	}
}

func sampleJSON() explain.Result {
	return explain.Result{
		Mode:    explain.ModeJSON,
		RawJSON: []byte("{\n  \"query_block\": {\"select_id\": 1}\n}\n"),
	}
}

func TestExplain_TabularToStdout(t *testing.T) {
	runner := &fakeExplainRunner{res: sampleTabular()}
	stdout, _, err := invokeExplain(t,
		[]string{"--conn", "local_wms", "SELECT * FROM orders"},
		"", runner)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if runner.gotMode != explain.ModeTabular {
		t.Fatalf("mode: %s", runner.gotMode)
	}
	if runner.gotSQL != "SELECT * FROM orders" {
		t.Fatalf("sql: %q", runner.gotSQL)
	}
	if runner.gotConn != "local_wms" {
		t.Fatalf("conn: %s", runner.gotConn)
	}
	// Tabular output should contain the header + separator + row.
	if !strings.Contains(stdout, "select_type") || !strings.Contains(stdout, "Extra") {
		t.Fatalf("tabular output missing header: %q", stdout)
	}
	if !strings.Contains(stdout, "orders") {
		t.Fatalf("tabular output missing table cell: %q", stdout)
	}
}

func TestExplain_TabularFromStdin(t *testing.T) {
	runner := &fakeExplainRunner{res: sampleTabular()}
	stdout, _, err := invokeExplain(t,
		[]string{"--conn", "local_wms"},
		"SELECT 1\n", runner)
	if err != nil {
		t.Fatal(err)
	}
	if runner.gotSQL != "SELECT 1" {
		t.Fatalf("stdin SQL not threaded: %q", runner.gotSQL)
	}
	if !strings.Contains(stdout, "select_type") {
		t.Fatalf("tabular output missing header: %q", stdout)
	}
}

func TestExplain_JSONToStdoutByDefaultSidecarOff(t *testing.T) {
	runner := &fakeExplainRunner{res: sampleJSON()}
	stdout, _, err := invokeExplain(t,
		[]string{"--json", "--conn", "local_wms", "--no-json-sidecar", "SELECT 1"},
		"", runner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "query_block") {
		t.Fatalf("json stdout missing body: %q", stdout)
	}
}

func TestExplain_JSONToFileWithSidecar(t *testing.T) {
	cwd := t.TempDir()
	runner := &fakeExplainRunner{res: sampleJSON()}
	target := filepath.Join(cwd, "plan.json")

	var out, errOut bytes.Buffer
	in := strings.NewReader("")
	err := runExplainCmdWithRunner(
		[]string{"--json", "--conn", "local_wms", "-o", target, "SELECT 1"},
		in, &out, &errOut, cwd, runner.runner)
	if err != nil {
		t.Fatalf("explain: %v\nstderr: %s", err, errOut.String())
	}

	// Sidecar must exist alongside the data file.
	sidecar := target + ".meta.json"
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	sc, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(sc, &meta); err != nil {
		t.Fatalf("sidecar not valid JSON: %v\n%s", err, sc)
	}
	if meta["kind"] != "explain" {
		t.Fatalf("sidecar kind: %v", meta["kind"])
	}
	if meta["connection"] != "local_wms" {
		t.Fatalf("sidecar connection: %v", meta["connection"])
	}
	if meta["format"] != "json" {
		t.Fatalf("sidecar format: %v", meta["format"])
	}
	// Audit: NO query text, NO secrets.
	body := string(sc)
	if strings.Contains(body, "SELECT") || strings.Contains(body, "password") || strings.Contains(body, "secret") {
		t.Fatalf("sidecar leaked sensitive field: %s", body)
	}

	// Data file must contain the EXPLAIN FORMAT=JSON body.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "query_block") {
		t.Fatalf("data file missing JSON body: %s", data)
	}

	// stdout should mention both paths.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout should have sidecar + data paths, got %q", out.String())
	}
	if !strings.HasSuffix(lines[0], ".meta.json") {
		t.Fatalf("first stdout line should be sidecar: %s", lines[0])
	}
	if !strings.HasSuffix(lines[1], ".json") || strings.HasSuffix(lines[1], ".meta.json") {
		t.Fatalf("second stdout line should be data: %s", lines[1])
	}
}

func TestExplain_JSONWithoutOAndSidecarDefaultsToOnIsRejected(t *testing.T) {
	runner := &fakeExplainRunner{res: sampleJSON()}
	_, _, err := invokeExplain(t,
		[]string{"--json", "--conn", "local_wms", "SELECT 1"},
		"", runner)
	if err == nil {
		t.Fatal("expected error when --json + sidecar ON has no -o")
	}
	if !strings.Contains(err.Error(), "-o FILE") || !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("error message should mention -o + sidecar: %v", err)
	}
}

func TestExplain_NoJSONSidecarFlagWins(t *testing.T) {
	cwd := t.TempDir()
	runner := &fakeExplainRunner{res: sampleJSON()}
	target := filepath.Join(cwd, "plan.json")

	var out, errOut bytes.Buffer
	in := strings.NewReader("")
	err := runExplainCmdWithRunner(
		[]string{"--json", "--json-sidecar", "--no-json-sidecar", "--conn", "local_wms", "-o", target, "SELECT 1"},
		in, &out, &errOut, cwd, runner.runner)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	if _, err := os.Stat(target + ".meta.json"); err == nil {
		t.Fatalf("sidecar must not exist with --no-json-sidecar")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("data file should exist: %v", err)
	}
	// stdout: only the data path (no sidecar line).
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout should only have data path, got %q", out.String())
	}
}

func TestExplain_TabularToFileNoSidecar(t *testing.T) {
	cwd := t.TempDir()
	runner := &fakeExplainRunner{res: sampleTabular()}
	target := filepath.Join(cwd, "plan.txt")

	var out, errOut bytes.Buffer
	in := strings.NewReader("")
	err := runExplainCmdWithRunner(
		[]string{"--conn", "local_wms", "-o", target, "SELECT 1"},
		in, &out, &errOut, cwd, runner.runner)
	if err != nil {
		t.Fatalf("explain: %v\nstderr: %s", err, errOut.String())
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "select_type") {
		t.Fatalf("file missing header: %s", body)
	}
	if _, err := os.Stat(target + ".meta.json"); err == nil {
		t.Fatalf("tabular mode must not emit a sidecar")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "plan.txt") {
		t.Fatalf("stdout should only mention the data path: %q", out.String())
	}
}

func TestExplain_MissingSQL(t *testing.T) {
	runner := &fakeExplainRunner{}
	_, _, err := invokeExplain(t,
		[]string{"--conn", "local_wms"},
		"", runner)
	if err == nil || !strings.Contains(err.Error(), "explain requires SQL") {
		t.Fatalf("want missing-SQL error, got %v", err)
	}
}

func TestExplain_MissingConn(t *testing.T) {
	runner := &fakeExplainRunner{}
	_, _, err := invokeExplain(t,
		[]string{"SELECT 1"},
		"", runner)
	if err == nil || !strings.Contains(err.Error(), "--conn is required") {
		t.Fatalf("want missing-conn error, got %v", err)
	}
}

func TestExplain_RunnerErrorPropagates(t *testing.T) {
	runner := &fakeExplainRunner{err: explainResultErr("syntax error near FROM")}
	_, _, err := invokeExplain(t,
		[]string{"--conn", "local_wms", "SELECT FROM"},
		"", runner)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "syntax error near FROM") {
		t.Fatalf("runner error not propagated: %v", err)
	}
}

func TestExplain_BogusFlag(t *testing.T) {
	runner := &fakeExplainRunner{}
	_, stderr, err := invokeExplain(t,
		[]string{"--conn", "local_wms", "--bogus", "SELECT 1"},
		"", runner)
	if err == nil {
		t.Fatal("expected error on unknown flag")
	}
	if !strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf("stderr should explain flag error: %q", stderr)
	}
}

func TestExplain_DataFailureRollsBackSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename-into-directory semantics differ on Windows")
	}
	cwd := t.TempDir()
	runner := &fakeExplainRunner{res: sampleJSON()}
	// Pre-create a directory at the target path so the rename fails.
	target := filepath.Join(cwd, "plan.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	in := strings.NewReader("")
	err := runExplainCmdWithRunner(
		[]string{"--json", "--conn", "local_wms", "-o", target, "SELECT 1"},
		in, &out, &errOut, cwd, runner.runner)
	if err == nil {
		t.Fatalf("expected data-write failure")
	}
	if _, err := os.Stat(target + ".meta.json"); err == nil {
		t.Fatalf("sidecar should be removed after data failure")
	}
}

// helper to fabricate an error without leaking the explain package's
// imports into the test file.
func explainResultErr(msg string) error {
	return &runnerError{msg: msg}
}

type runnerError struct{ msg string }

func (e *runnerError) Error() string { return e.msg }

// Smoke: usage text mentions `explain`.
func TestPrintUsage_MentionsExplain(t *testing.T) {
	var buf bytes.Buffer
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()
	done := make(chan struct{})
	go func() {
		printUsage()
		w.Close()
		done <- struct{}{}
	}()
	<-done
	buf.ReadFrom(r)
	if !strings.Contains(buf.String(), "explain") {
		t.Fatalf("usage missing explain command")
	}
}

// JSON sidecar timestamp parses as RFC3339.
func TestExplain_SidecarTimestampParses(t *testing.T) {
	cwd := t.TempDir()
	runner := &fakeExplainRunner{res: sampleJSON()}
	target := filepath.Join(cwd, "plan.json")
	var out, errOut bytes.Buffer
	in := strings.NewReader("")
	if err := runExplainCmdWithRunner(
		[]string{"--json", "--conn", "local_wms", "-o", target, "SELECT 1"},
		in, &out, &errOut, cwd, runner.runner); err != nil {
		t.Fatalf("explain: %v", err)
	}
	body, _ := os.ReadFile(target + ".meta.json")
	var meta map[string]any
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("sidecar parse: %v", err)
	}
	ts, ok := meta["exported_at"].(string)
	if !ok {
		t.Fatalf("exported_at missing or wrong type: %v", meta["exported_at"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("exported_at is not RFC3339: %v (%v)", ts, err)
	}
}