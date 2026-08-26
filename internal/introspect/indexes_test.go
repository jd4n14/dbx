package introspect

import (
	"context"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/db"
)

func TestListIndexes_EmptyResult(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
			}, nil
		},
	}
	idx, err := ListIndexes(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(idx) != 0 {
		t.Fatalf("len = %d, want 0 (%+v)", len(idx), idx)
	}
}

func TestListIndexes_CompositeAndMultiIndex(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
				data: [][]any{
					// Composite unique key over (tenant_id, order_id)
					{"uk_orders_tenant_order", int64(0), int64(1), "tenant_id", "A", int64(1200), "BTREE"},
					{"uk_orders_tenant_order", int64(0), int64(2), "order_id", "A", int64(1200), "BTREE"},
					// Secondary index on status (non-unique, descending)
					{"idx_orders_status", int64(1), int64(1), "status", "D", int64(8), "BTREE"},
					// FULLTEXT index (Collation empty)
					{"ft_orders_notes", int64(1), int64(1), "notes", "", int64(-1), "FULLTEXT"},
				},
			}, nil
		},
	}
	idx, err := ListIndexes(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(idx) != 4 {
		t.Fatalf("len = %d, want 4 (%+v)", len(idx), idx)
	}
	if idx[0].Name != "uk_orders_tenant_order" || idx[0].NonUnique != false || idx[0].SeqInIndex != 1 || idx[0].ColumnName != "tenant_id" || idx[0].Collation != "A" || idx[0].Cardinality != 1200 || idx[0].IndexType != "BTREE" {
		t.Fatalf("idx[0] = %+v", idx[0])
	}
	if idx[1].SeqInIndex != 2 || idx[1].ColumnName != "order_id" {
		t.Fatalf("idx[1] = %+v", idx[1])
	}
	if idx[2].Name != "idx_orders_status" || idx[2].NonUnique != true || idx[2].Collation != "D" || idx[2].Cardinality != 8 {
		t.Fatalf("idx[2] = %+v", idx[2])
	}
	if idx[3].IndexType != "FULLTEXT" || idx[3].Collation != "" {
		t.Fatalf("idx[3] = %+v", idx[3])
	}
}

func TestListIndexes_NullCardinalityBecomesMinusOne(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
				data: [][]any{
					{"idx_x", int64(1), int64(1), "x", "A", nil, "BTREE"},
				},
			}, nil
		},
	}
	idx, err := ListIndexes(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 1 {
		t.Fatalf("len = %d", len(idx))
	}
	if idx[0].Cardinality != -1 {
		t.Fatalf("Cardinality = %d, want -1 (NULL sentinel)", idx[0].Cardinality)
	}
}

func TestListIndexes_NonUniqueFromInt64One(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
				data: [][]any{
					{"PRIMARY", int64(0), int64(1), "id", "A", int64(42), "BTREE"},
				},
			}, nil
		},
	}
	idx, err := ListIndexes(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if idx[0].NonUnique != false {
		t.Fatalf("NonUnique = %v, want false", idx[0].NonUnique)
	}
}

func TestListIndexes_NonUniqueFromBytesOne(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
				data: [][]any{
					{"idx_x", []byte("1"), int64(1), "x", "A", int64(1), "BTREE"},
				},
			}, nil
		},
	}
	idx, err := ListIndexes(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if !idx[0].NonUnique {
		t.Fatalf("NonUnique = false, want true (bytes=1)")
	}
}

func TestListIndexes_InvalidTableRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListIndexes(context.Background(), fdb, "a.b"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
}

func TestListIndexes_MissingColumnShape(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"INDEX_NAME", "SEQ_IN_INDEX", "COLUMN_NAME"}, // missing fields
			}, nil
		},
	}
	if _, err := ListIndexes(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "missing required columns") {
		t.Fatalf("got %v", err)
	}
}

func TestListIndexes_PassesTableAsBoundArg(t *testing.T) {
	t.Parallel()
	var captured []any
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			captured = args
			return &fakeRows{
				cols: []string{"INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX", "COLUMN_NAME", "COLLATION", "CARDINALITY", "INDEX_TYPE"},
			}, nil
		},
	}
	if _, err := ListIndexes(context.Background(), fdb, "orders"); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0] != "orders" {
		t.Fatalf("args = %v, want [\"orders\"]", captured)
	}
	if !strings.Contains(fdb.lastSQL, "information_schema.STATISTICS") {
		t.Fatalf("SQL must target STATISTICS, got %q", fdb.lastSQL)
	}
}