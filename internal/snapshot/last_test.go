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

func TestWriteReadLast(t *testing.T) {
	cwd := t.TempDir()
	dataJSON := []byte(`[
  {
    "id": 1
  }
]
`)
	if err := WriteLastFromQueryData(cwd, "local_wms", "select 1 as id", dataJSON); err != nil {
		t.Fatal(err)
	}
	path := LastPath(cwd)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	r, err := ReadLast(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != TypeLastResult || r.Connection != "local_wms" || r.SQL != "select 1 as id" {
		t.Fatalf("%+v", r)
	}
	var rows []map[string]any
	if err := json.Unmarshal(r.Data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["id"].(float64) != 1 {
		t.Fatalf("data %s", r.Data)
	}
}

func TestReadLast_Missing(t *testing.T) {
	_, err := ReadLast(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no last result") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteLast_CreatesDbxDir(t *testing.T) {
	cwd := t.TempDir()
	data, _ := NormalizeData([]byte(`[]`))
	if err := WriteLast(cwd, NewLastResult(data, "c", "s", time.Time{})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".dbx")); err != nil {
		t.Fatal(err)
	}
}

func TestWriteReadLast_PreservesLargeInteger(t *testing.T) {
	cwd := t.TempDir()
	if err := WriteLastFromQueryData(cwd, "local", "select id", []byte(`[{"id":9007199254740993}]`)); err != nil {
		t.Fatal(err)
	}
	r, err := ReadLast(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Data), "9007199254740993") {
		t.Fatalf("large integer changed: %s", r.Data)
	}
}

func TestReadLast_RejectsMalformedAndWrongType(t *testing.T) {
	cwd := t.TempDir()
	path := LastPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"snapshot","data":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLast(cwd); err == nil || !strings.Contains(err.Error(), "envelope type") {
		t.Fatalf("wrong-type error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"last_result","data":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLast(cwd); err == nil || !strings.Contains(err.Error(), "parse last result") {
		t.Fatalf("malformed error = %v", err)
	}
}

func TestWriteLast_PrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission bits are not meaningful")
	}
	cwd := t.TempDir()
	if err := WriteLastFromQueryData(cwd, "local", "select 1", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(cwd, ".dbx"), 0o700)
	assertMode(t, LastPath(cwd), 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %s = %o, want %o", path, got, want)
	}
}
