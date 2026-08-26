package introspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/db"
)

func TestTableSize_Populated(t *testing.T) {
	t.Parallel()
	create := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	update := time.Date(2026, 7, 21, 22, 0, 0, 0, time.UTC)
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: [][]any{
					{int64(1234), int64(16384), int64(4096), int64(0), int64(1235), "utf8mb4_unicode_ci", create, update, "InnoDB"},
				},
			}, nil
		},
	}
	out, err := GetTableSize(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if out.Rows != 1234 || out.DataBytes != 16384 || out.IndexBytes != 4096 || out.DataFreeBytes != 0 || out.AutoIncrement != 1235 {
		t.Fatalf("numeric fields wrong: %+v", out)
	}
	if out.Collation != "utf8mb4_unicode_ci" || out.Engine != "InnoDB" {
		t.Fatalf("string fields wrong: %+v", out)
	}
	if !out.CreateTime.Equal(create) || !out.UpdateTime.Equal(update) {
		t.Fatalf("time fields wrong: create=%v update=%v", out.CreateTime, out.UpdateTime)
	}
	if !strings.Contains(fdb.lastSQL, "information_schema.TABLES") {
		t.Fatalf("SQL = %q", fdb.lastSQL)
	}
}

func TestTableSize_NullFieldsNormalisedToSentinel(t *testing.T) {
	t.Parallel()
	// ENGINE can also surface as []byte from the driver.
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: [][]any{
					{nil, int64(0), int64(0), int64(0), nil, "", nil, nil, []byte("MEMORY")},
				},
			}, nil
		},
	}
	out, err := GetTableSize(context.Background(), fdb, "scratch")
	if err != nil {
		t.Fatal(err)
	}
	if out.Rows != -1 || out.AutoIncrement != -1 {
		t.Fatalf("NULL should normalise to -1; got rows=%d auto=%d", out.Rows, out.AutoIncrement)
	}
	if out.Engine != "MEMORY" {
		t.Fatalf("Engine = %q, want MEMORY (from []byte)", out.Engine)
	}
	if !out.CreateTime.IsZero() || !out.UpdateTime.IsZero() {
		t.Fatalf("NULL times should be zero; got create=%v update=%v", out.CreateTime, out.UpdateTime)
	}
}

func TestTableSize_TableNotFound(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: nil,
			}, nil
		},
	}
	_, err := GetTableSize(context.Background(), fdb, "ghost")
	if err == nil || !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("want ErrTableNotFound, got %v", err)
	}
}

func TestTableSize_MultipleRowsRejected(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: [][]any{
					{int64(1), int64(0), int64(0), int64(0), nil, "", nil, nil, "InnoDB"},
					{int64(2), int64(0), int64(0), int64(0), nil, "", nil, nil, "InnoDB"},
				},
			}, nil
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "more than one row") {
		t.Fatalf("got %v", err)
	}
}

func TestTableSize_InvalidTableRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "a.b"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestTableSize_MissingColumnShape(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME"}, // missing ENGINE
			}, nil
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "missing required columns") {
		t.Fatalf("got %v", err)
	}
}

func TestTableSize_QueryError(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, errors.New("boom")
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func TestTableSize_RowsErr(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				err:    errBoom{msg: "iter fail"},
			}, nil
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "iter fail") {
		t.Fatalf("got %v", err)
	}
}

func TestTableSize_TableBoundAsArgument(t *testing.T) {
	t.Parallel()
	var gotArg any
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			if len(args) > 0 {
				gotArg = args[0]
			}
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: nil,
			}, nil
		},
	}
	if _, err := GetTableSize(context.Background(), fdb, "orders"); err == nil {
		t.Fatal("expected ErrTableNotFound for empty result")
	}
	if gotArg != "orders" {
		t.Fatalf("arg = %v (%T)", gotArg, gotArg)
	}
}

func TestTableSize_DateTimeAsBytes(t *testing.T) {
	t.Parallel()
	// Some drivers surface DATETIME as []byte in "YYYY-MM-DD HH:MM:SS".
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"TABLE_ROWS", "DATA_LENGTH", "INDEX_LENGTH", "DATA_FREE", "AUTO_INCREMENT", "TABLE_COLLATION", "CREATE_TIME", "UPDATE_TIME", "ENGINE"},
				data: [][]any{
					{int64(10), int64(0), int64(0), int64(0), nil, "utf8mb4_unicode_ci", []byte("2026-07-21 10:00:00"), []byte("2026-07-21 22:00:00"), "InnoDB"},
				},
			}, nil
		},
	}
	out, err := GetTableSize(context.Background(), fdb, "t")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	if !out.CreateTime.Equal(want) {
		t.Fatalf("CreateTime = %v, want %v", out.CreateTime, want)
	}
}
