package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadList(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snapshots")
	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	data, _ := NormalizeData([]byte(`[{"id":1,"status":"pending"}]`))
	s := NewSnapshot("before_split_order", data, "local_wms", "select 1", at)

	path, err := Save(dir, s, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "before_split_order.json") {
		t.Fatalf("path %s", path)
	}

	// no force → exists error
	_, err = Save(dir, s, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want already exists, got %v", err)
	}

	// force ok
	s2 := s
	s2.Data, _ = NormalizeData([]byte(`[{"id":1,"status":"shipped"}]`))
	if _, err := Save(dir, s2, true); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir, "before_split_order")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "before_split_order" || loaded.Connection != "local_wms" {
		t.Fatalf("%+v", loaded)
	}
	var rows []map[string]any
	if err := json.Unmarshal(loaded.Data, &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["status"] != "shipped" {
		t.Fatalf("data %s", loaded.Data)
	}

	// second snapshot for list
	s3 := NewSnapshot("after_split_order", data, "", "", at)
	if _, err := Save(dir, s3, false); err != nil {
		t.Fatal(err)
	}

	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len %d", len(list))
	}
	// sorted by name
	if list[0].Name != "after_split_order" || list[1].Name != "before_split_order" {
		t.Fatalf("order: %+v", list)
	}
	if !list[1].CreatedAt.Equal(at) {
		t.Fatalf("created_at %v", list[1].CreatedAt)
	}
}

func TestLoad_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "missing_snap")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestList_MissingDir(t *testing.T) {
	list, err := List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if list != nil && len(list) != 0 {
		t.Fatalf("want empty, got %+v", list)
	}
}

func TestSave_InvalidName(t *testing.T) {
	_, err := Save(t.TempDir(), Snapshot{Name: "../evil", Data: json.RawMessage(`[]`)}, false)
	if err == nil {
		t.Fatal("expected invalid name")
	}
}

func TestAtomicWrite_Content(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := atomicWrite(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello\n" {
		t.Fatalf("got %q", b)
	}
}

func TestDir(t *testing.T) {
	got := Dir("/proj")
	want := filepath.Join("/proj", ".dbx", "snapshots")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestLoad_RejectsInvalidEnvelope(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"malformed", `{"type":"snapshot"`, "parse snapshot"},
		{"wrong-type", `{"type":"last_result","name":"snap","created_at":"2026-07-08T12:00:00Z","data":[]}`, "envelope type"},
		{"wrong-name", `{"type":"snapshot","name":"other","created_at":"2026-07-08T12:00:00Z","data":[]}`, "mismatched name"},
		{"zero-time", `{"type":"snapshot","name":"snap","created_at":"0001-01-01T00:00:00Z","data":[]}`, "empty created_at"},
		{"empty-data", `{"type":"snapshot","name":"snap","created_at":"2026-07-08T12:00:00Z"}`, "empty data"},
		{"invalid-data", `{"type":"snapshot","name":"snap","created_at":"2026-07-08T12:00:00Z","data":`, "parse snapshot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := SnapshotPath(dir, "snap")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir, "snap"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSave_PrivateFileModeWithoutChangingCustomParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits are not meaningful")
	}
	dir := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSnapshot("snap", json.RawMessage(`[]`), "", "", time.Time{})
	path, err := Save(dir, s, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
	assertMode(t, dir, 0o755)
}

func TestSaveLoad_PreservesLargeInteger(t *testing.T) {
	dir := t.TempDir()
	data, err := NormalizeData([]byte(`[{"id":9007199254740993}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(dir, NewSnapshot("large", data, "", "", time.Time{}), false); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir, "large")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(loaded.Data), "9007199254740993") {
		t.Fatalf("large integer changed: %s", loaded.Data)
	}
}
