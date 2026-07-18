package introspect

import (
	"context"
	"fmt"

	"github.com/jd4n14/dbx/internal/db"
)

// fakeDB is a hand-rolled db.DB used to assert the SQL text introspect passes
// to QueryContext and the canned Rows it returns. Mirrors the fake in
// internal/ddl/fetch_test.go to keep the test code self-contained.
type fakeDB struct {
	queryFn func(ctx context.Context, query string, args ...any) (db.Rows, error)
	lastSQL string
	calls   int
}

func (f *fakeDB) QueryContext(ctx context.Context, query string, args ...any) (db.Rows, error) {
	f.lastSQL = query
	f.calls++
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
