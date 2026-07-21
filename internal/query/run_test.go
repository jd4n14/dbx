package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/db"
)

// fakeDB is a hand-rolled db.DB for offline Run tests (no mock library).
type fakeDB struct {
	// queryFn is invoked by QueryContext. If nil, QueryContext fails.
	queryFn func(ctx context.Context, query string, args ...any) (db.Rows, error)
	// queried is set true when QueryContext is called.
	queried bool
	// lastSQL is the last query string passed to QueryContext.
	lastSQL string
}

func (f *fakeDB) QueryContext(ctx context.Context, query string, args ...any) (db.Rows, error) {
	f.queried = true
	f.lastSQL = query
	if f.queryFn == nil {
		return nil, fmt.Errorf("fakeDB: unexpected QueryContext")
	}
	return f.queryFn(ctx, query, args...)
}

func (f *fakeDB) PingContext(ctx context.Context) error { return nil }

func (f *fakeDB) Close() error { return nil }

// fakeRows implements db.Rows over in-memory columnar data.
type fakeRows struct {
	cols   []string
	data   [][]any
	i      int // index of next row to yield (0-based into data)
	closed bool
	err    error
}

func (r *fakeRows) Columns() ([]string, error) {
	if r.closed {
		return nil, fmt.Errorf("fakeRows: closed")
	}
	return r.cols, nil
}

func (r *fakeRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.closed {
		return fmt.Errorf("fakeRows: closed")
	}
	if r.i == 0 || r.i > len(r.data) {
		return fmt.Errorf("fakeRows: Scan without Next")
	}
	row := r.data[r.i-1]
	for i := range dest {
		p, ok := dest[i].(*any)
		if !ok {
			return fmt.Errorf("fakeRows: dest[%d] is %T, want *any", i, dest[i])
		}
		if i < len(row) {
			*p = row[i]
		} else {
			*p = nil
		}
	}
	return nil
}

func (r *fakeRows) Err() error { return r.err }

func (r *fakeRows) Close() error {
	r.closed = true
	return nil
}

func TestRun_OfflineFakeRowsPrettyJSON(t *testing.T) {
	t.Parallel()

	// Multi-column row covering types jsonutil expects: int64, string,
	// []byte DECIMAL text, []byte JSON object, time.Time, nil.
	ts := time.Date(2026, 7, 8, 17, 20, 31, 0, time.UTC)
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"id", "name", "amount", "meta", "created_at", "note"},
				data: [][]any{
					{
						int64(1),
						"alice",
						[]byte("12.34"),
						[]byte(`{"a":1}`),
						ts,
						nil,
					},
				},
			}, nil
		},
	}

	got, err := Run(context.Background(), fdb, "SELECT 1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !fdb.queried {
		t.Fatal("expected QueryContext to be called")
	}

	// encoding/json sorts object keys alphabetically.
	want := "[\n" +
		"  {\n" +
		"    \"amount\": \"12.34\",\n" +
		"    \"created_at\": \"2026-07-08T17:20:31Z\",\n" +
		"    \"id\": 1,\n" +
		"    \"meta\": {\n" +
		"      \"a\": 1\n" +
		"    },\n" +
		"    \"name\": \"alice\",\n" +
		"    \"note\": null\n" +
		"  }\n" +
		"]\n"
	if string(got) != want {
		t.Fatalf("pretty JSON mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRun_OfflineEmptyResult(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"id"},
				data: nil,
			}, nil
		},
	}

	got, err := Run(context.Background(), fdb, "SELECT id FROM t WHERE 0=1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "[]\n"
	if string(got) != want {
		t.Fatalf("empty result: got %q, want %q", got, want)
	}
}

func TestRun_DeniedSQLNeverQueries(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			t.Error("QueryContext must not be called for denied SQL")
			return nil, fmt.Errorf("should not query")
		},
	}

	cases := []string{
		"DELETE FROM t",
		"UPDATE t SET x=1",
		"WITH c AS (SELECT 1) DELETE FROM t",
		"SELECT 1; DROP TABLE x",
	}
	for _, sqlText := range cases {
		fdb.queried = false
		_, err := Run(context.Background(), fdb, sqlText)
		if err == nil {
			t.Fatalf("expected policy error for %q", sqlText)
		}
		if fdb.queried {
			t.Fatalf("QueryContext invoked for denied SQL %q", sqlText)
		}
	}
}

func TestRun_EmptySQL(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{}
	_, err := Run(context.Background(), fdb, "   \n\t  ")
	if err == nil {
		t.Fatal("expected empty query error")
	}
	if fdb.queried {
		t.Fatal("QueryContext must not be called for empty SQL")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should mention empty, got: %v", err)
	}
}

func TestRun_QueryErrorWrapped(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, fmt.Errorf("connection reset")
		},
	}
	_, err := Run(context.Background(), fdb, "SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "query:") {
		t.Fatalf("want query: prefix, got %v", err)
	}
}

func TestRun_ScanMultipleRows(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"n"},
				data: [][]any{
					{int64(1)},
					{int64(2)},
				},
			}, nil
		},
	}
	got, err := Run(context.Background(), fdb, "SELECT n FROM t")
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"n\": 1\n  },\n  {\n    \"n\": 2\n  }\n]\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// fakeRowsIgnoreNext consumes all rows without ever advancing i again; used
// to simulate a server that returns MORE rows than the client requested, to
// exercise the LIMIT+1 truncation detector.
type fakeRowsIgnoreNext struct {
	cols    []string
	data    [][]any
	maxScan int // stop emitting Next() after this many rows have been asked for
	asked   int
	closed  bool
}

func (r *fakeRowsIgnoreNext) Columns() ([]string, error) {
	if r.closed {
		return nil, fmt.Errorf("fakeRowsIgnoreNext: closed")
	}
	return r.cols, nil
}

func (r *fakeRowsIgnoreNext) Next() bool {
	if r.closed {
		return false
	}
	if r.asked >= r.maxScan {
		return false
	}
	r.asked++
	return true
}

func (r *fakeRowsIgnoreNext) Scan(dest ...any) error {
	if r.closed {
		return fmt.Errorf("fakeRowsIgnoreNext: closed")
	}
	if r.asked == 0 || r.asked > len(r.data) {
		return fmt.Errorf("fakeRowsIgnoreNext: Scan without Next")
	}
	row := r.data[r.asked-1]
	for i := range dest {
		p, ok := dest[i].(*any)
		if !ok {
			return fmt.Errorf("fakeRowsIgnoreNext: dest[%d] is %T, want *any", i, dest[i])
		}
		if i < len(row) {
			*p = row[i]
		} else {
			*p = nil
		}
	}
	return nil
}

func (r *fakeRowsIgnoreNext) Err() error  { return nil }
func (r *fakeRowsIgnoreNext) Close() error { r.closed = true; return nil }

// rowsByN builds 1..n rows of a single column "n" with int64 values.
func rowsByN(n int) [][]any {
	out := make([][]any, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, []any{int64(i)})
	}
	return out
}

func TestRunWithLimit_ZeroPreservesBareArray(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"n"}, data: rowsByN(3)}, nil
		},
	}

	res, err := RunWithLimit(context.Background(), fdb, "SELECT n FROM t", 0)
	if err != nil {
		t.Fatalf("RunWithLimit(0): %v", err)
	}
	want := "[\n  {\n    \"n\": 1\n  },\n  {\n    \"n\": 2\n  },\n  {\n    \"n\": 3\n  }\n]\n"
	if string(res.Data) != want {
		t.Fatalf("maxRows=0 must keep bare array:\ngot:\n%s\nwant:\n%s", res.Data, want)
	}
	if res.Truncated || res.RowCount != 0 || res.MaxRows != 0 {
		t.Fatalf("res metadata: %+v, want zero/zero/false", res)
	}
	if fdb.lastSQL != "SELECT n FROM t" {
		t.Fatalf("maxRows=0 must NOT rewrite SQL, got %q", fdb.lastSQL)
	}
}

func TestRunWithLimit_NotTruncatedEnvelope(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"n"}, data: rowsByN(10)}, nil
		},
	}

	res, err := RunWithLimit(context.Background(), fdb, "SELECT n FROM t", 10)
	if err != nil {
		t.Fatalf("RunWithLimit(10): %v", err)
	}

	if !strings.HasSuffix(fdb.lastSQL, " LIMIT 11") {
		t.Fatalf("SQL must have LIMIT 11 appended, got %q", fdb.lastSQL)
	}

	if res.Truncated {
		t.Fatalf("res.Truncated = true, want false (only 10 rows available)")
	}
	if res.RowCount != 10 {
		t.Fatalf("res.RowCount = %d, want 10", res.RowCount)
	}
	if res.MaxRows != 10 {
		t.Fatalf("res.MaxRows = %d, want 10", res.MaxRows)
	}

	want := "{\n" +
		"  \"type\": \"query\",\n" +
		"  \"truncated\": false,\n" +
		"  \"row_count\": 10,\n" +
		"  \"max_rows\": 10,\n" +
		"  \"data\": [\n" +
		"    {\n" +
		"      \"n\": 1\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 2\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 3\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 4\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 5\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 6\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 7\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 8\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 9\n" +
		"    },\n" +
		"    {\n" +
		"      \"n\": 10\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if string(res.Data) != want {
		t.Fatalf("envelope mismatch:\ngot:\n%s\nwant:\n%s", res.Data, want)
	}
}

func TestRunWithLimit_TruncatedEnvelope(t *testing.T) {
	t.Parallel()

	// The driver returns 11 rows even though we asked for LIMIT 10 (the
	// +1 trick surfaces this case). fakeRowsIgnoreNext stops emitting
	// after maxScan rows so the slice has exactly 11 entries.
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRowsIgnoreNext{
				cols:    []string{"n"},
				data:    rowsByN(11),
				maxScan: 11,
			}, nil
		},
	}

	res, err := RunWithLimit(context.Background(), fdb, "SELECT n FROM t", 10)
	if err != nil {
		t.Fatalf("RunWithLimit(10): %v", err)
	}

	if !res.Truncated {
		t.Fatalf("res.Truncated = false, want true (11 rows available, limit 10)")
	}
	if res.RowCount != 10 {
		t.Fatalf("res.RowCount = %d, want 10", res.RowCount)
	}

	// Parse envelope and assert data length == 10 (dropped the +1).
	var env struct {
		Type     string         `json:"type"`
		Truncat  bool           `json:"truncated"`
		RowCount int            `json:"row_count"`
		MaxRows  int            `json:"max_rows"`
		Data     []map[string]int `json:"data"`
	}
	if err := json.Unmarshal(res.Data, &env); err != nil {
		t.Fatalf("envelope must be valid JSON: %v\nbody:\n%s", err, res.Data)
	}
	if env.Type != "query" {
		t.Fatalf("type = %q, want query", env.Type)
	}
	if !env.Truncat {
		t.Fatalf("envelope.truncated = false, want true")
	}
	if env.RowCount != 10 {
		t.Fatalf("envelope.row_count = %d, want 10", env.RowCount)
	}
	if env.MaxRows != 10 {
		t.Fatalf("envelope.max_rows = %d, want 10", env.MaxRows)
	}
	if len(env.Data) != 10 {
		t.Fatalf("envelope.data length = %d, want 10", len(env.Data))
	}
	// Truncation drops the LAST row (the +1 sentinel), not the first.
	if env.Data[9]["n"] != 10 {
		t.Fatalf("envelope.data[9].n = %d, want 10 (must drop sentinel row 11)", env.Data[9]["n"])
	}
	if env.Data[0]["n"] != 1 {
		t.Fatalf("envelope.data[0].n = %d, want 1 (must keep first row)", env.Data[0]["n"])
	}
}

func TestRunWithLimit_NegativeRejected(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, fmt.Errorf("QueryContext must not be called for negative maxRows")
		},
	}
	_, err := RunWithLimit(context.Background(), fdb, "SELECT 1", -1)
	if err == nil {
		t.Fatal("expected error for maxRows=-1")
	}
	if !strings.Contains(err.Error(), "max-rows must be > 0") {
		t.Fatalf("error should mention max-rows, got: %v", err)
	}
	if fdb.queried {
		t.Fatal("QueryContext must not be called for negative maxRows")
	}
}

func TestRunWithLimit_TrailingSemicolonStillStripsBeforeAppendingLimit(t *testing.T) {
	t.Parallel()

	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"n"}, data: rowsByN(2)}, nil
		},
	}

	if _, err := RunWithLimit(context.Background(), fdb, "SELECT n FROM t ;", 10); err != nil {
		t.Fatalf("RunWithLimit(10) with trailing semicolon: %v", err)
	}
	want := "SELECT n FROM t LIMIT 11"
	if fdb.lastSQL != want {
		t.Fatalf("SQL rewrite:\ngot:  %q\nwant: %q", fdb.lastSQL, want)
	}
}

func TestRunWithLimit_RunBackwardCompatStillWorks(t *testing.T) {
	t.Parallel()

	// Existing Run() callers (snapshot diff, Neovim render) must keep
	// getting a bare pretty array — no envelope, no behavior change.
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"n"}, data: rowsByN(2)}, nil
		},
	}

	got, err := Run(context.Background(), fdb, "SELECT n FROM t")
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"n\": 1\n  },\n  {\n    \"n\": 2\n  }\n]\n"
	if string(got) != want {
		t.Fatalf("Run() output changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if fdb.lastSQL != "SELECT n FROM t" {
		t.Fatalf("Run() must not rewrite SQL, got %q", fdb.lastSQL)
	}
}
