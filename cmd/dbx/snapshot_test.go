package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/snapshot"
)

func TestRunSnapshot_SaveFromStdin(t *testing.T) {
	cwd := t.TempDir()
	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer

	err := runSnapshotCmd(
		[]string{"--name", "before_split_order", "--conn", "local_wms", "--dir", dir},
		strings.NewReader(`[{"id":1,"status":"pending"}]`),
		&stdout, &stderr,
		cwd,
		true, // pipe mode
	)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(stdout.String())
	if !strings.HasSuffix(path, "before_split_order.json") {
		t.Fatalf("stdout path %q", path)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr %q", stderr.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s snapshot.Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != snapshot.TypeSnapshot || s.Name != "before_split_order" || s.Connection != "local_wms" {
		t.Fatalf("%+v", s)
	}
	var rows []map[string]any
	if err := json.Unmarshal(s.Data, &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["status"] != "pending" {
		t.Fatalf("data %s", s.Data)
	}
}

func TestRunSnapshot_MissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(nil, strings.NewReader(`[]`), &stdout, &stderr, t.TempDir(), true)
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestRunSnapshot_InvalidName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "../evil"},
		strings.NewReader(`[]`),
		&stdout, &stderr,
		t.TempDir(),
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid snapshot name") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSnapshot_EmptyStdin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "x"},
		strings.NewReader(""),
		&stdout, &stderr,
		t.TempDir(),
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "stdin is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSnapshot_InvalidJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "x"},
		strings.NewReader(`{`),
		&stdout, &stderr,
		t.TempDir(),
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSnapshot_OverwriteForce(t *testing.T) {
	cwd := t.TempDir()
	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer

	args := []string{"--name", "snap1", "--dir", dir}
	if err := runSnapshotCmd(args, strings.NewReader(`[{"n":1}]`), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err := runSnapshotCmd(args, strings.NewReader(`[{"n":2}]`), &stdout, &stderr, cwd, true)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout on fail %q", stdout.String())
	}

	stdout.Reset()
	argsForce := []string{"--name", "snap1", "--dir", dir, "--force"}
	if err := runSnapshotCmd(argsForce, strings.NewReader(`[{"n":2}]`), &stdout, &stderr, cwd, true); err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Load(dir, "snap1")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(s.Data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["n"].(float64) != 2 {
		t.Fatalf("data %s", s.Data)
	}
}

func TestRunSnapshot_FromLastResult(t *testing.T) {
	cwd := t.TempDir()
	dataJSON := []byte(`[{"id":42}]`)
	if err := snapshot.WriteLastFromQueryData(cwd, "local_wms", "select 42 as id", dataJSON); err != nil {
		t.Fatal(err)
	}

	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer
	// useStdinAsPipe=false → last result
	err := runSnapshotCmd(
		[]string{"--name", "from_last", "--dir", dir},
		strings.NewReader(""), // unused
		&stdout, &stderr,
		cwd,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Load(dir, "from_last")
	if err != nil {
		t.Fatal(err)
	}
	if s.Connection != "local_wms" || s.SQL != "select 42 as id" {
		t.Fatalf("%+v", s)
	}
	if !bytes.Contains(s.Data, []byte(`42`)) {
		t.Fatalf("data %s", s.Data)
	}
}

func TestRunSnapshot_FromLastFlagWithoutPipedInput(t *testing.T) {
	cwd := t.TempDir()
	if err := snapshot.WriteLastFromQueryData(cwd, "local_wms", "select 42 as id", []byte(`[{"id":42}]`)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "from_last", "--from-last"},
		strings.NewReader(""),
		&stdout, &stderr, cwd, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Load(snapshot.Dir(cwd), "from_last")
	if err != nil {
		t.Fatal(err)
	}
	if s.Connection != "local_wms" || s.SQL != "select 42 as id" {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestRunSnapshot_FromLastRejectsPipedInputWithoutStdout(t *testing.T) {
	cwd := t.TempDir()
	if err := snapshot.WriteLastFromQueryData(cwd, "local_wms", "select 42 as id", []byte(`[{"id":42}]`)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "from_last", "--from-last"},
		strings.NewReader(`[{"id":7}]`),
		&stdout, &stderr, cwd, true,
	)
	if err == nil || !strings.Contains(err.Error(), "--from-last") {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout on ambiguity = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, ".dbx", "snapshots", "from_last.json")); !os.IsNotExist(err) {
		t.Fatalf("snapshot was written, stat error = %v", err)
	}
}

func TestRunSnapshot_NoLastResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "x"},
		strings.NewReader(""),
		&stdout, &stderr,
		t.TempDir(),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "no last result") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSnapshot_ListAndShow(t *testing.T) {
	cwd := t.TempDir()
	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer

	for _, name := range []string{"after_split", "before_split"} {
		if err := runSnapshotCmd(
			[]string{"--name", name, "--dir", dir},
			strings.NewReader(`[{"ok":true}]`),
			&stdout, &stderr,
			cwd, true,
		); err != nil {
			t.Fatal(err)
		}
		stdout.Reset()
	}

	if err := runSnapshotCmd([]string{"list", "--dir", dir}, nil, &stdout, &stderr, cwd, false); err != nil {
		t.Fatal(err)
	}
	listOut := stdout.String()
	if !strings.Contains(listOut, "after_split") || !strings.Contains(listOut, "before_split") {
		t.Fatalf("list %q", listOut)
	}
	// sorted: after before
	if strings.Index(listOut, "after_split") > strings.Index(listOut, "before_split") {
		t.Fatalf("not sorted: %q", listOut)
	}

	stdout.Reset()
	// Go flag: options before the positional name.
	if err := runSnapshotCmd([]string{"show", "--dir", dir, "before_split"}, nil, &stdout, &stderr, cwd, false); err != nil {
		t.Fatal(err)
	}
	var s snapshot.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &s); err != nil {
		t.Fatalf("show json: %v\n%s", err, stdout.String())
	}
	if s.Name != "before_split" || s.Type != snapshot.TypeSnapshot {
		t.Fatalf("%+v", s)
	}

	stdout.Reset()
	if err := runSnapshotCmd([]string{"show", "--dir", dir, "--data", "before_split"}, nil, &stdout, &stderr, cwd, false); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("data: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0]["ok"] != true {
		t.Fatalf("%v", rows)
	}
}

func TestRunSnapshot_ShowNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"show", "--dir", t.TempDir(), "missing_snap"},
		nil, &stdout, &stderr, t.TempDir(), false,
	)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestRunSnapshot_ShowMissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd([]string{"show"}, nil, &stdout, &stderr, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSnapshot_ConnFlagOverridesLast(t *testing.T) {
	cwd := t.TempDir()
	if err := snapshot.WriteLastFromQueryData(cwd, "from_last", "sql", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "s", "--dir", dir, "--conn", "override"},
		nil, &stdout, &stderr, cwd, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	s, err := snapshot.Load(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if s.Connection != "override" {
		t.Fatalf("conn %q", s.Connection)
	}
	// SQL still from last
	if s.SQL != "sql" {
		t.Fatalf("sql %q", s.SQL)
	}
}

func TestRunSnapshot_DefaultDir(t *testing.T) {
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := runSnapshotCmd(
		[]string{"--name", "default_dir_snap"},
		strings.NewReader(`[{"x":1}]`),
		&stdout, &stderr, cwd, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, ".dbx", "snapshots", "default_dir_snap.json")
	got := strings.TrimSpace(stdout.String())
	if got != want {
		t.Fatalf("path %q want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		assertSnapshotMode(t, filepath.Join(cwd, ".dbx"), 0o700)
		assertSnapshotMode(t, snapshot.Dir(cwd), 0o700)
		assertSnapshotMode(t, want, 0o600)
	}
}

func TestRunSnapshot_ShowDataPreservesLargeInteger(t *testing.T) {
	cwd := t.TempDir()
	dir := snapshot.Dir(cwd)
	var stdout, stderr bytes.Buffer
	if err := runSnapshotCmd(
		[]string{"--name", "large", "--dir", dir},
		strings.NewReader(`[{"id":9007199254740993}]`),
		&stdout, &stderr, cwd, true,
	); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runSnapshotCmd([]string{"show", "--dir", dir, "--data", "large"}, nil, &stdout, &stderr, cwd, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "9007199254740993") {
		t.Fatalf("large integer changed: %s", stdout.String())
	}
}

func assertSnapshotMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", path, got, want)
	}
}
