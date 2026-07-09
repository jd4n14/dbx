package query

import (
	"context"
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
