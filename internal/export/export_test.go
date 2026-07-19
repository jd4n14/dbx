package export

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mustRaw(t *testing.T, s string) []byte {
	t.Helper()
	b := []byte(s)
	if !json.Valid(b) {
		t.Fatalf("invalid test fixture JSON: %s", s)
	}
	return b
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
}

func TestCSV_HeaderAndRows(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	data := mustRaw(t, `[{"id":1,"status":"pending"},{"id":2,"status":"shipped"}]`)

	res, err := Write(out, data, Options{
		SnapshotID: "snap1",
		Format:     FormatCSV,
		WriteJSON:  false,
		Now:        fixedNow(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.RowCount != 2 {
		t.Fatalf("row count %d", res.RowCount)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (header + 2), got %d", len(rows))
	}
	// Header columns are sorted: id, status
	if rows[0][0] != "id" || rows[0][1] != "status" {
		t.Fatalf("header: %v", rows[0])
	}
	if rows[1][0] != "1" || rows[1][1] != "pending" {
		t.Fatalf("row 1: %v", rows[1])
	}
	if rows[2][0] != "2" || rows[2][1] != "shipped" {
		t.Fatalf("row 2: %v", rows[2])
	}
}

func TestCSV_RFC4180Escaping(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	// Multi-cell fixture exercising comma, quote, and newline per RFC 4180.
	data := mustRaw(t, `[{"note":"hello, world","quote":"she said \"hi\"","multi":"line1\nline2"}]`)

	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("want header + 1 row, got %d", len(records))
	}
	// Header is sorted alphabetically; look up cells by column name.
	header := records[0]
	row := records[1]
	indexOf := func(col string) int {
		for i, h := range header {
			if h == col {
				return i
			}
		}
		t.Fatalf("missing column %q in header %v", col, header)
		return -1
	}
	if row[indexOf("note")] != "hello, world" {
		t.Fatalf("comma cell not preserved: %q", row[indexOf("note")])
	}
	if row[indexOf("quote")] != "she said \"hi\"" {
		t.Fatalf("quote cell not preserved: %q", row[indexOf("quote")])
	}
	if row[indexOf("multi")] != "line1\nline2" {
		t.Fatalf("newline cell not preserved: %q", row[indexOf("multi")])
	}
}

func TestCSV_RowTypes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	// mixed row including null, bool, number, and a JSON-typed cell.
	data := mustRaw(t, `[{"n":null,"b":true,"i":42,"f":3.14,"s":"hi"}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, _ := os.ReadFile(out)
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("rows: %d", len(records))
	}
	// header is sorted alphabetically
	want := []string{"b", "f", "i", "n", "s"}
	for i, h := range want {
		if records[0][i] != h {
			t.Fatalf("header[%d]=%q want %q", i, records[0][i], h)
		}
	}
	if records[1][0] != "true" {
		t.Fatalf("bool: %q", records[1][0])
	}
	if records[1][1] != "3.14" {
		t.Fatalf("float: %q", records[1][1])
	}
	if records[1][2] != "42" {
		t.Fatalf("int: %q", records[1][2])
	}
	if records[1][3] != "" {
		t.Fatalf("null should render as empty: %q", records[1][3])
	}
	if records[1][4] != "hi" {
		t.Fatalf("string: %q", records[1][4])
	}
}

func TestJSONL_PreservesTypes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.jsonl")
	data := mustRaw(t, `[
		{"id":1,"name":"alpha","active":true,"deleted":null,"meta":{"k":"v"}},
		{"id":2,"name":"beta","active":false,"deleted":null,"meta":[1,2]}
	]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatJSONL, Now: fixedNow()}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), body)
	}
	var a, b map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &a); err != nil {
		t.Fatalf("row 1 parse: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &b); err != nil {
		t.Fatalf("row 2 parse: %v", err)
	}
	if a["id"].(float64) != 1 || a["name"].(string) != "alpha" || a["active"].(bool) != true {
		t.Fatalf("row 1 types: %+v", a)
	}
	if a["deleted"] != nil {
		t.Fatalf("row 1 deleted should be null: %+v", a["deleted"])
	}
	meta, ok := a["meta"].(map[string]any)
	if !ok || meta["k"].(string) != "v" {
		t.Fatalf("row 1 meta: %+v", a["meta"])
	}
	if b["active"].(bool) != false {
		t.Fatalf("row 2 active: %+v", b["active"])
	}
	if _, ok := b["meta"].([]any); !ok {
		t.Fatalf("row 2 meta should be array: %+v", b["meta"])
	}
}

func TestJSONL_NumbersStayNumbers(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.jsonl")
	// Use numbers that round-trip cleanly through float64: a 32-bit int and
	// a moderate float. The encoder must keep them as JSON numbers, not
	// quoted strings (which would silently turn e.g. ids into opaque text).
	data := mustRaw(t, `[{"id":2147483647,"big":1.5e10,"small":-3}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatJSONL, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	for _, want := range []string{`"id":2147483647`, `"big":15000000000`, `"small":-3`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("number %q not preserved in body: %s", want, body)
		}
	}
	// None of these should be quoted as strings.
	if strings.Contains(string(body), `"2147483647"`) {
		t.Fatalf("number got quoted: %s", body)
	}
}

func TestSidecar_DefaultsOn(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	data := mustRaw(t, `[{"id":1}]`)
	res, err := Write(out, data, Options{
		SnapshotID: "demo",
		Connection: "local_wms",
		Format:     FormatCSV,
		WriteJSON:  true,
		Version:    "dbx 0.0.1",
		Now:        fixedNow(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Sidecar == "" {
		t.Fatalf("sidecar path missing")
	}
	body, err := os.ReadFile(res.Sidecar)
	if err != nil {
		t.Fatal(err)
	}
	var s Sidecar
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("sidecar parse: %v", err)
	}
	if s.Version != SidecarVersion {
		t.Fatalf("sidecar version: %q", s.Version)
	}
	if s.SnapshotID != "demo" {
		t.Fatalf("sidecar snapshot_id: %q", s.SnapshotID)
	}
	if s.Connection != "local_wms" {
		t.Fatalf("sidecar connection: %q", s.Connection)
	}
	if !s.ExportedAt.Equal(fixedNow()) {
		t.Fatalf("sidecar exported_at: %v", s.ExportedAt)
	}
	if s.RowCount != 1 {
		t.Fatalf("sidecar row_count: %d", s.RowCount)
	}
	if s.Format != string(FormatCSV) {
		t.Fatalf("sidecar format: %q", s.Format)
	}
	if s.DBXVersion != "dbx 0.0.1" {
		t.Fatalf("sidecar dbx_version: %q", s.DBXVersion)
	}
	// Audit: must NEVER carry query text or secrets. Check the raw body.
	if strings.Contains(string(body), "sql") {
		t.Fatalf("sidecar must not include sql field: %s", body)
	}
	if strings.Contains(string(body), "password") || strings.Contains(string(body), "secret") {
		t.Fatalf("sidecar must not include secrets: %s", body)
	}
}

func TestSidecar_OffByRequest(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	data := mustRaw(t, `[{"id":1}]`)
	res, err := Write(out, data, Options{
		SnapshotID: "demo",
		Format:     FormatCSV,
		WriteJSON:  false,
		Now:        fixedNow(),
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Sidecar != "" {
		t.Fatalf("sidecar must be empty when --no-json, got %s", res.Sidecar)
	}
	if _, err := os.Stat(out + ".meta.json"); err == nil {
		t.Fatalf("sidecar file should not exist")
	}
}

func TestAtomicWrite_DataFailureRemovesSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only dir semantics differ on Windows")
	}
	dir := t.TempDir()
	// First, write a real sidecar with --json enabled to verify the
	// rollback path. We then force the data write to fail by pointing at
	// a path inside a read-only directory.
	data := mustRaw(t, `[{"id":1}]`)

	// Cause the data rename to fail: pre-create a directory at the target.
	out := filepath.Join(dir, "snap.csv")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}

	res, err := Write(out, data, Options{
		SnapshotID: "demo",
		Format:     FormatCSV,
		WriteJSON:  true,
		Now:        fixedNow(),
	})
	if err == nil {
		t.Fatalf("expected data-write failure")
	}
	if res.Sidecar != "" {
		t.Fatalf("sidecar should be rolled back, got %s", res.Sidecar)
	}
	if _, err := os.Stat(out + ".meta.json"); err == nil {
		t.Fatalf("sidecar should have been removed after data failure")
	}
}

func TestAtomicWrite_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	if err := os.WriteFile(out, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := mustRaw(t, `[{"id":1}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(body), "id\n") {
		t.Fatalf("atomic replace failed, body: %q", body)
	}
}

func TestSidecar_FailureWhenTargetDirReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only dir semantics differ for root on Windows/Linux")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o700)
	out := filepath.Join(ro, "snap.csv")
	data := mustRaw(t, `[{"id":1}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, WriteJSON: true, Now: fixedNow()}); err == nil {
		t.Fatalf("expected write to fail in read-only dir")
	}
}

func TestEmptyDataIsFriendlyError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	if _, err := Write(out, []byte(""), Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()}); err == nil {
		t.Fatalf("expected error on empty data")
	}
}

func TestDefaultPath(t *testing.T) {
	got, err := DefaultPath("snap1", FormatCSV, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "snap1.csv" {
		t.Fatalf("default csv path: %s", got)
	}
	got, err = DefaultPath("snap1", FormatJSONL, "/tmp/out")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/out", "snap1.jsonl") {
		t.Fatalf("default jsonl path: %s", got)
	}
	if _, err := DefaultPath("", FormatCSV, ""); err == nil {
		t.Fatalf("expected error on empty snapshot id")
	}
}

func TestValidateFormat(t *testing.T) {
	cases := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"", FormatCSV, false},
		{"csv", FormatCSV, false},
		{"CSV", FormatCSV, false},
		{"jsonl", FormatJSONL, false},
		{"json", "", true},
		{"xml", "", true},
	}
	for _, tc := range cases {
		got, err := ValidateFormat(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ValidateFormat(%q) err=%v", tc.in, err)
		}
		if !tc.wantErr && got != tc.want {
			t.Fatalf("ValidateFormat(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestSingleObjectSnapshot(t *testing.T) {
	// A bare object should yield a single-row CSV with the object's keys.
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	data := mustRaw(t, `{"id":1,"status":"pending"}`)
	res, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.RowCount != 1 {
		t.Fatalf("single-object should be one row, got %d", res.RowCount)
	}
	body, _ := os.ReadFile(out)
	records, _ := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if len(records) != 2 {
		t.Fatalf("header + 1 row, got %d", len(records))
	}
	if records[1][0] != "1" {
		t.Fatalf("id cell: %q", records[1][0])
	}
}

func TestScalarTopLevel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.jsonl")
	data := mustRaw(t, `42`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatJSONL, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	if !strings.Contains(string(body), `{"value":42}`) {
		t.Fatalf("scalar should be wrapped: %s", body)
	}
}

func TestObjectRowsOrderPreserved(t *testing.T) {
	// encoding/json already preserves array order. Just sanity-check.
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.jsonl")
	data := mustRaw(t, `[{"a":1},{"a":2},{"a":3}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatJSONL, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	for i, want := range []string{`{"a":1}`, `{"a":2}`, `{"a":3}`} {
		if lines[i] != want {
			t.Fatalf("line %d: got %q want %q", i, lines[i], want)
		}
	}
}

func TestColumnsUnionAcrossRows(t *testing.T) {
	// The CSV header must include the union of keys across rows, not just
	// the keys of the first row.
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.csv")
	data := mustRaw(t, `[{"id":1,"status":"a"},{"id":2,"total":10}]`)
	if _, err := Write(out, data, Options{SnapshotID: "s", Format: FormatCSV, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(out)
	records, _ := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	header := records[0]
	want := []string{"id", "status", "total"}
	if len(header) != len(want) {
		t.Fatalf("header: %v", header)
	}
	for i, h := range want {
		if header[i] != h {
			t.Fatalf("header[%d]=%q want %q", i, header[i], h)
		}
	}
	if records[1][1] != "a" || records[1][2] != "" {
		t.Fatalf("missing key should be empty: %v", records[1])
	}
	if records[2][1] != "" || records[2][2] != "10" {
		t.Fatalf("missing key should be empty: %v", records[2])
	}
}
