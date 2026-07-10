package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/snapshot"
)

func TestRunDiff_NoDifferences(t *testing.T) {
	dir := t.TempDir()
	writeDiffSnapshot(t, dir, "before", `[{"id":9007199254740993,"status":"created"}]`)
	writeDiffSnapshot(t, dir, "after", `[{"status":"created","id":9007199254740993}]`)

	var stdout, stderr bytes.Buffer
	err := runDiffCmd([]string{"--dir", dir, "before", "after"}, &stdout, &stderr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "no differences\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDiff_NestedChange(t *testing.T) {
	dir := t.TempDir()
	writeDiffSnapshot(t, dir, "before", `{"rows":[{"status":"created"}]}`)
	writeDiffSnapshot(t, dir, "after", `{"rows":[{"status":"pending"}]}`)

	var stdout, stderr bytes.Buffer
	err := runDiffCmd([]string{"--dir", dir, "before", "after"}, &stdout, &stderr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := "$.rows[0].status\n- \"created\"\n+ \"pending\"\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunDiff_JSON(t *testing.T) {
	dir := t.TempDir()
	writeDiffSnapshot(t, dir, "before", `{"value":1}`)
	writeDiffSnapshot(t, dir, "after", `{"value":2}`)

	var stdout, stderr bytes.Buffer
	err := runDiffCmd([]string{"--dir", dir, "--json", "before", "after"}, &stdout, &stderr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type    string          `json:"type"`
		Before  json.RawMessage `json:"before"`
		After   json.RawMessage `json:"after"`
		Changes []struct {
			Path   string          `json:"path"`
			Kind   string          `json:"kind"`
			Before json.RawMessage `json:"before"`
			After  json.RawMessage `json:"after"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	var before, after map[string]int
	if err := json.Unmarshal(got.Before, &before); err != nil {
		t.Fatalf("decode before value: %v", err)
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatalf("decode after value: %v", err)
	}
	if got.Type != "diff" || before["value"] != 1 || after["value"] != 2 {
		t.Fatalf("envelope = %+v", got)
	}
	if len(got.Changes) != 1 || got.Changes[0].Path != "$.value" || got.Changes[0].Kind != "changed" || string(got.Changes[0].Before) != "1" || string(got.Changes[0].After) != "2" {
		t.Fatalf("changes = %+v", got.Changes)
	}
}

func TestRunDiff_MissingSnapshotLeavesStdoutEmpty(t *testing.T) {
	dir := t.TempDir()
	writeDiffSnapshot(t, dir, "before", `[]`)

	var stdout, stderr bytes.Buffer
	err := runDiffCmd([]string{"--dir", dir, "before", "missing"}, &stdout, &stderr, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout on error = %q", stdout.String())
	}
}

func TestRunDiff_InvalidArgsLeaveStdoutEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runDiffCmd([]string{"only-one"}, &stdout, &stderr, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "exactly two") {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout on error = %q", stdout.String())
	}

	stdout.Reset()
	err = runDiffCmd([]string{"--unexpected"}, &stdout, &stderr, t.TempDir())
	if err == nil {
		t.Fatal("expected bad flag error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout on flag error = %q", stdout.String())
	}
}

func writeDiffSnapshot(t *testing.T, dir, name, data string) {
	t.Helper()
	raw, err := snapshot.NormalizeData([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	_, err = snapshot.Save(dir, snapshot.NewSnapshot(name, raw, "local", "select", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)), false)
	if err != nil {
		t.Fatal(err)
	}
}
