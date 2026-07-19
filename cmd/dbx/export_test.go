package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/snapshot"
)

// seedSnapshot saves a snapshot under the default dbx snapshots dir of cwd
// and returns the directory used. Tests pass `cwd = t.TempDir()`.
func seedSnapshot(t *testing.T, cwd, name, data string) string {
	t.Helper()
	dir := snapshot.Dir(cwd)
	at := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	raw, err := snapshot.NormalizeData([]byte(data))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	s := snapshot.NewSnapshot(name, raw, "local_wms", "select 1", at)
	if _, err := snapshot.Save(dir, s, true); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	return dir
}

func invokeExport(t *testing.T, cwd string, args []string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = runExportCmd(args, &out, &errOut, cwd)
	return out.String(), errOut.String(), err
}

func TestExport_DefaultCSVWithSidecar(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "before_split_order", `[{"id":1,"status":"pending"}]`)

	stdout, _, err := invokeExport(t, cwd, []string{"before_split_order"})
	if err != nil {
		t.Fatalf("runExport: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want data path + sidecar path, got %q", stdout)
	}
	if !strings.HasSuffix(lines[0], "before_split_order.csv") {
		t.Fatalf("data path: %s", lines[0])
	}
	if !strings.HasSuffix(lines[1], "before_split_order.csv.meta.json") {
		t.Fatalf("sidecar path: %s", lines[1])
	}
	// Data file should be a real CSV with header.
	data, err := os.ReadFile(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(records) != 2 || records[0][0] != "id" || records[0][1] != "status" {
		t.Fatalf("unexpected csv: %v", records)
	}
	// Sidecar must carry audit metadata only.
	sc, err := os.ReadFile(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	var meta exportSidecarShape
	if err := json.Unmarshal(sc, &meta); err != nil {
		t.Fatalf("sidecar parse: %v", err)
	}
	if meta.SnapshotID != "before_split_order" || meta.RowCount != 1 {
		t.Fatalf("sidecar: %+v", meta)
	}
	// Audit: no SQL or secrets.
	body := string(sc)
	if strings.Contains(body, "sql") || strings.Contains(body, "password") || strings.Contains(body, "secret") {
		t.Fatalf("sidecar leaked sensitive field: %s", body)
	}
}

// exportSidecarShape mirrors internal/export.Sidecar so the test can decode
// without re-importing private fields. Kept inline to avoid a public API
// surface change.
type exportSidecarShape struct {
	Version    string    `json:"version"`
	SnapshotID string    `json:"snapshot_id"`
	Connection string    `json:"connection,omitempty"`
	ExportedAt time.Time `json:"exported_at"`
	RowCount   int       `json:"row_count"`
	Columns    []string  `json:"columns"`
	Format     string    `json:"format"`
	DBXVersion string    `json:"dbx_version,omitempty"`
}

func TestExport_FormatJSONL(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "after_split", `[{"id":1},{"id":2}]`)

	stdout, _, err := invokeExport(t, cwd, []string{"--format", "jsonl", "after_split"})
	if err != nil {
		t.Fatal(err)
	}
	// First line is the data path; second is the sidecar (default ON).
	path := strings.Split(strings.TrimSpace(stdout), "\n")[0]
	if !strings.HasSuffix(path, ".jsonl") {
		t.Fatalf("want jsonl ext: %s", path)
	}
	body, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 2 || lines[0] != `{"id":1}` || lines[1] != `{"id":2}` {
		t.Fatalf("jsonl body: %q", body)
	}
}

func TestExport_NoJSONSidecar(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "nojs", `[{"id":1}]`)

	stdout, _, err := invokeExport(t, cwd, []string{"--no-json", "nojs"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("expected only data path on stdout, got %q", stdout)
	}
	if _, err := os.Stat(strings.TrimSpace(stdout) + ".meta.json"); err == nil {
		t.Fatalf("sidecar must not exist with --no-json")
	}
}

func TestExport_NoJSONOverridesJSON(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "twosw", `[{"id":1}]`)
	// Both flags set: --no-json wins per the dispatch rule.
	stdout, _, err := invokeExport(t, cwd, []string{"--json", "--no-json", "twosw"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("--no-json must win over --json: %q", stdout)
	}
}

func TestExport_DefaultPathFromSnapshotID(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "abc", `[{"a":1}]`)
	stdout, _, err := invokeExport(t, cwd, []string{"abc"})
	if err != nil {
		t.Fatal(err)
	}
	path := strings.Split(strings.TrimSpace(stdout), "\n")[0]
	want := filepath.Join(cwd, "abc.csv")
	if path != want {
		t.Fatalf("default path: got %s want %s", path, want)
	}
}

func TestExport_CustomOutputPath(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "abc", `[{"a":1}]`)
	target := filepath.Join(t.TempDir(), "custom.csv")
	stdout, _, err := invokeExport(t, cwd, []string{"-o", target, "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), target) {
		t.Fatalf("custom path not echoed: %q", stdout)
	}
	body, _ := os.ReadFile(target)
	if !bytes.HasPrefix(body, []byte("a\n")) {
		t.Fatalf("custom file contents: %q", body)
	}
}

func TestExport_MissingSnapshot(t *testing.T) {
	cwd := t.TempDir()
	_, _, err := invokeExport(t, cwd, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "snapshot not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestExport_InvalidSnapshotID(t *testing.T) {
	cwd := t.TempDir()
	_, _, err := invokeExport(t, cwd, []string{"../evil"})
	if err == nil || !strings.Contains(err.Error(), "invalid snapshot name") {
		t.Fatalf("want invalid name error, got %v", err)
	}
}

func TestExport_InvalidFormat(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "x", `[{"id":1}]`)
	_, _, err := invokeExport(t, cwd, []string{"--format", "xml", "x"})
	if err == nil || !strings.Contains(err.Error(), "--format") {
		t.Fatalf("want format error, got %v", err)
	}
}

func TestExport_NoArgs(t *testing.T) {
	cwd := t.TempDir()
	_, _, err := invokeExport(t, cwd, []string{})
	if err == nil || !strings.Contains(err.Error(), "snapshot id") {
		t.Fatalf("want missing-id error, got %v", err)
	}
}

func TestExport_RowTypesPreservedInJSONL(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "rt", `[{"id":1,"active":true,"deleted":null,"meta":{"k":"v"}}]`)
	stdout, _, err := invokeExport(t, cwd, []string{"--format", "jsonl", "--no-json", "rt"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(strings.TrimSpace(stdout))
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("jsonl parse: %v", err)
	}
	if row["active"].(bool) != true || row["deleted"] != nil {
		t.Fatalf("types: %+v", row)
	}
	if _, ok := row["meta"].(map[string]any); !ok {
		t.Fatalf("nested meta: %+v", row["meta"])
	}
}

func TestExport_SidecarWrittenBeforeData(t *testing.T) {
	// We can't easily observe ordering from outside, but we can force the
	// data write to fail and verify the sidecar gets cleaned up.
	if runtime.GOOS == "windows" {
		t.Skip("rename-into-directory semantics differ on Windows")
	}
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "ordr", `[{"id":1}]`)
	// Force data rename to fail by pre-creating a directory at the target.
	collision := filepath.Join(cwd, "ordr.csv")
	if err := os.Mkdir(collision, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err := invokeExport(t, cwd, []string{"ordr"})
	if err == nil {
		t.Fatalf("expected data-write failure")
	}
	// Sidecar should have been removed; check both possible names.
	for _, suffix := range []string{".csv.meta.json", ".json.meta.json"} {
		if _, err := os.Stat(filepath.Join(cwd, "ordr"+suffix)); err == nil {
			t.Fatalf("sidecar should be removed after data failure: ordr%s", suffix)
		}
	}
}

func TestExport_HelpfulStderrOnUnknownFlag(t *testing.T) {
	cwd := t.TempDir()
	seedSnapshot(t, cwd, "hf", `[{"id":1}]`)
	_, stderr, err := invokeExport(t, cwd, []string{"--bogus", "hf"})
	if err == nil {
		t.Fatalf("expected error on unknown flag")
	}
	if !strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf("stderr should explain flag error, got %q", stderr)
	}
}

// Smoke test: ensure the help line in printUsage mentions `export`.
func TestPrintUsage_MentionsExport(t *testing.T) {
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
	if !strings.Contains(buf.String(), "export") {
		t.Fatalf("usage missing export command")
	}
}
