package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/snapshot"
)

func TestRunPath_DefaultLastResult(t *testing.T) {
	cwd := t.TempDir()
	writePathLast(t, cwd, `[{"metadata":{"status":"created"}}]`)

	var stdout, stderr bytes.Buffer
	err := runPathCmd([]string{"metadata.status"}, &stdout, &stderr, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "[\n  \"created\"\n]\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunPath_NamedSnapshotWildcard(t *testing.T) {
	dir := t.TempDir()
	writePathSnapshot(t, dir, "before", `[{"items":[{"id":1},{"id":2}]}]`)

	var stdout, stderr bytes.Buffer
	err := runPathCmd([]string{"--dir", dir, "--snapshot", "before", "items[*].id"}, &stdout, &stderr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "[\n  1,\n  2\n]\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPath_NoMatches(t *testing.T) {
	cwd := t.TempDir()
	writePathLast(t, cwd, `[{"items":[]}]`)
	var stdout, stderr bytes.Buffer
	if err := runPathCmd([]string{"items[0]"}, &stdout, &stderr, cwd); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "[]\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunPath_ErrorsLeaveStdoutEmpty(t *testing.T) {
	cases := []struct {
		name string
		args []string
		cwd  string
		want string
	}{
		{"bad selector", []string{"items[-1]"}, t.TempDir(), "invalid path"},
		{"missing snapshot", []string{"--dir", t.TempDir(), "--snapshot", "missing", "id"}, t.TempDir(), "not found"},
		{"empty snapshot", []string{"--snapshot", "", "id"}, t.TempDir(), "invalid snapshot name"},
		{"unexpected args", []string{"id", "other"}, t.TempDir(), "exactly one"},
		{"bad flag", []string{"--unknown", "id"}, t.TempDir(), "flag provided"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runPathCmd(tc.args, &stdout, &stderr, tc.cwd)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout on error = %q", stdout.String())
			}
		})
	}
}

func writePathLast(t *testing.T, cwd, data string) {
	t.Helper()
	raw, err := snapshot.NormalizeData([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.WriteLast(cwd, snapshot.NewLastResult(raw, "local", "select", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
}

func writePathSnapshot(t *testing.T, dir, name, data string) {
	t.Helper()
	raw, err := snapshot.NormalizeData([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Save(dir, snapshot.NewSnapshot(name, raw, "local", "select", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)), false); err != nil {
		t.Fatal(err)
	}
}
