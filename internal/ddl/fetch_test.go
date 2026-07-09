package ddl

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/db"
)

// fakeDB is a hand-rolled db.DB for offline Fetch tests (no mock library).
type fakeDB struct {
	queryFn func(ctx context.Context, query string, args ...any) (db.Rows, error)
	lastSQL string
}

func (f *fakeDB) QueryContext(ctx context.Context, query string, args ...any) (db.Rows, error) {
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
	i      int
	closed bool
	err    error
}

func (r *fakeRows) Columns() ([]string, error) {
	if r.closed {
		return nil, fmt.Errorf("closed")
	}
	return r.cols, nil
}

func (r *fakeRows) Next() bool {
	if r.closed || r.err != nil || r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.i == 0 || r.i > len(r.data) {
		return fmt.Errorf("Scan without Next")
	}
	row := r.data[r.i-1]
	for i := range dest {
		p := dest[i].(*any)
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

func TestFetch_HappyPath(t *testing.T) {
	t.Parallel()
	ddlText := "CREATE TABLE `orders` (\n  `id` int\n)"
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Table", "Create Table"},
				data: [][]any{{"orders", ddlText}},
			}, nil
		},
	}
	got, err := Fetch(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != ddlText {
		t.Fatalf("ddl = %q, want %q", got, ddlText)
	}
	if fdb.lastSQL != "SHOW CREATE TABLE `orders`" {
		t.Fatalf("SQL = %q", fdb.lastSQL)
	}
}

func TestFetch_ColumnFallbackIndex1(t *testing.T) {
	t.Parallel()
	ddlText := "CREATE TABLE `t` (`id` int)"
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"foo", "bar"},
				data: [][]any{{"t", []byte(ddlText)}},
			}, nil
		},
	}
	got, err := Fetch(context.Background(), fdb, "t")
	if err != nil {
		t.Fatal(err)
	}
	if got != ddlText {
		t.Fatalf("got %q", got)
	}
}

func TestFetch_InvalidTable(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{}
	_, err := Fetch(context.Background(), fdb, "a.b")
	if err == nil {
		t.Fatal("expected invalid table error")
	}
	if fdb.lastSQL != "" {
		t.Fatal("must not query on invalid table name")
	}
}

func TestFetch_ZeroRows(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"Table", "Create Table"}}, nil
		},
	}
	_, err := Fetch(context.Background(), fdb, "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestFetch_QueryError(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, fmt.Errorf("table doesn't exist")
		},
	}
	_, err := Fetch(context.Background(), fdb, "nope")
	if err == nil || !strings.HasPrefix(err.Error(), "ddl:") {
		t.Fatalf("got %v", err)
	}
}

func TestFetch_EmptyDDL(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Table", "Create Table"},
				data: [][]any{{"t", ""}},
			}, nil
		},
	}
	_, err := Fetch(context.Background(), fdb, "t")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

func TestFetch_MultiRowUnexpected(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Table", "Create Table"},
				data: [][]any{
					{"t", "CREATE TABLE `t` (`id` int)"},
					{"t", "CREATE TABLE `t` (`id` int)"},
				},
			}, nil
		},
	}
	_, err := Fetch(context.Background(), fdb, "t")
	if err == nil || !strings.Contains(err.Error(), "unexpected row count") {
		t.Fatalf("got %v", err)
	}
}
