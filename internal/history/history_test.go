package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func entry(ts string, conn, sql string, rows int) Entry {
	t, _ := time.Parse(time.RFC3339, ts)
	return Entry{
		Type:       Type,
		Timestamp:  t,
		Connection: conn,
		SQL:        sql,
		Rows:       rows,
		Bytes:      len(sql),
		DurationMs: 10,
	}
}

func TestAppend_PersistsAndOrders(t *testing.T) {
	cwd := t.TempDir()
	e := entry("2026-07-15T10:00:00Z", "local_wms", "select 1;", 1)
	if err := Append(cwd, e, 0); err != nil {
		t.Fatal(err)
	}
	if Count(cwd) != 1 {
		t.Fatalf("count = %d", Count(cwd))
	}
	raw, err := os.ReadFile(Path(cwd))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sql":"select 1;"`) {
		t.Fatalf("missing sql in: %s", raw)
	}
	if !strings.Contains(string(raw), `"connection":"local_wms"`) {
		t.Fatalf("missing connection in: %s", raw)
	}
}

func TestAppend_SkipsEmptySQL(t *testing.T) {
	cwd := t.TempDir()
	if err := Append(cwd, Entry{Connection: "x", SQL: "   "}, 10); err != nil {
		t.Fatal(err)
	}
	if Count(cwd) != 0 {
		t.Fatalf("blank SQL must be a no-op, count=%d", Count(cwd))
	}
}

func TestAppend_DefaultLimitUsedForNonPositive(t *testing.T) {
	cwd := t.TempDir()
	if err := Append(cwd, entry("2026-07-15T10:00:00Z", "c", "select 1", 1), -1); err != nil {
		t.Fatal(err)
	}
	if Count(cwd) != 1 {
		t.Fatalf("non-positive limit must still accept one entry, count=%d", Count(cwd))
	}
}

func TestList_NewestFirstWithIndex(t *testing.T) {
	cwd := t.TempDir()
	for _, e := range []Entry{
		entry("2026-07-15T10:00:00Z", "c", "select 1", 1),
		entry("2026-07-15T11:00:00Z", "c", "select 2", 1),
		entry("2026-07-15T12:00:00Z", "c", "select 3", 1),
	} {
		if err := Append(cwd, e, 10); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := List(cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("len = %d", len(listed))
	}
	if listed[0].Index != 1 || listed[0].SQL != "select 3" {
		t.Fatalf("newest should be first with idx 1, got %+v", listed[0])
	}
	if listed[2].Index != 3 || listed[2].SQL != "select 1" {
		t.Fatalf("oldest should be last with idx 3, got %+v", listed[2])
	}
}

func TestList_RespectsLimit(t *testing.T) {
	cwd := t.TempDir()
	for i := 0; i < 5; i++ {
		ts := time.Date(2026, 7, 15, 10, i, 0, 0, time.UTC).Format(time.RFC3339)
		if err := Append(cwd, entry(ts, "c", "select "+string(rune('1'+i)), 1), 0); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := List(cwd, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("limit not honored, got %d", len(listed))
	}
	if listed[0].SQL != "select 5" {
		t.Fatalf("first should be newest, got %q", listed[0].SQL)
	}
}

func TestAppend_TrimsToLimit(t *testing.T) {
	cwd := t.TempDir()
	for i := 0; i < 5; i++ {
		ts := time.Date(2026, 7, 15, 10, i, 0, 0, time.UTC).Format(time.RFC3339)
		if err := Append(cwd, entry(ts, "c", "select "+string(rune('a'+i)), 1), 3); err != nil {
			t.Fatal(err)
		}
	}
	if got := Count(cwd); got != 3 {
		t.Fatalf("count = %d want 3", got)
	}
	listed, _ := List(cwd, 0)
	// Newest must be the last appended (e), oldest must be 'c'.
	if listed[0].SQL != "select e" {
		t.Fatalf("newest should be 'e', got %q", listed[0].SQL)
	}
	if listed[2].SQL != "select c" {
		t.Fatalf("oldest retained should be 'c', got %q", listed[2].SQL)
	}
}

func TestShowByIndex(t *testing.T) {
	cwd := t.TempDir()
	for _, e := range []Entry{
		entry("2026-07-15T10:00:00Z", "c", "select 1", 1),
		entry("2026-07-15T11:00:00Z", "c", "select 2", 1),
	} {
		if err := Append(cwd, e, 0); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		idx  int
		want string
	}{
		{1, "select 2"},
		{2, "select 1"},
	}
	for _, tc := range cases {
		ent, err := ShowByIndex(cwd, tc.idx)
		if err != nil {
			t.Fatalf("idx %d: %v", tc.idx, err)
		}
		if ent.SQL != tc.want {
			t.Fatalf("idx %d: %q want %q", tc.idx, ent.SQL, tc.want)
		}
	}
}

func TestShowByIndex_OutOfRange(t *testing.T) {
	cwd := t.TempDir()
	if err := Append(cwd, entry("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ShowByIndex(cwd, 5); err == nil {
		t.Fatalf("expected error")
	}
}

func TestShowByIndex_Empty(t *testing.T) {
	cwd := t.TempDir()
	if _, err := ShowByIndex(cwd, 1); err == nil || !strings.Contains(err.Error(), "no history") {
		t.Fatalf("expected no-history error, got %v", err)
	}
}

func TestShowByIndex_Invalid(t *testing.T) {
	cwd := t.TempDir()
	if _, err := ShowByIndex(cwd, 0); err == nil {
		t.Fatalf("expected error for index 0")
	}
}

func TestClear(t *testing.T) {
	cwd := t.TempDir()
	if err := Append(cwd, entry("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	if err := Clear(cwd); err != nil {
		t.Fatal(err)
	}
	if Count(cwd) != 0 {
		t.Fatalf("count after clear = %d", Count(cwd))
	}
	if _, err := os.Stat(Path(cwd)); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	// Clearing a missing file must not error.
	if err := Clear(cwd); err != nil {
		t.Fatalf("clear of missing file should be ok, got %v", err)
	}
}

func TestAppend_GarbageLineIgnoredNotFatal(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".dbx"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := Path(cwd)
	if err := os.WriteFile(path, []byte("not json\n"+`{"ts":"2026-07-15T10:00:00Z","sql":"select 1","connection":"c"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Count(cwd) != 1 {
		t.Fatalf("garbage line must be skipped, count=%d", Count(cwd))
	}
}

func TestAppend_TypeFieldAutoFilled(t *testing.T) {
	cwd := t.TempDir()
	e := Entry{
		Timestamp:  time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		Connection: "c",
		SQL:        "select 1",
	}
	if err := Append(cwd, e, 0); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path(cwd))
	var got Entry
	if err := json.Unmarshal(bytesTrim(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != Type {
		t.Fatalf("type marker not auto-filled, got %q", got.Type)
	}
}

func TestAppend_FileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode semantics differ on Windows")
	}
	cwd := t.TempDir()
	if err := Append(cwd, entry("2026-07-15T10:00:00Z", "c", "select 1", 1), 0); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(cwd, ".dbx"):            0o700,
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
