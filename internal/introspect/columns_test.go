package introspect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/db"
)

func TestListColumns_DefaultAndRows(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
				data: [][]any{
					{"id", "bigint(20)", "NO", "PRI", nil, "auto_increment"},
					{"status", "varchar(32)", "NO", "", []byte("pending"), ""},
					{"created_at", "datetime", "NO", "", "current_timestamp", "DEFAULT_GENERATED"},
				},
			}, nil
		},
	}
	cols, err := ListColumns(context.Background(), fdb, "orders", "")
	if err != nil {
		t.Fatalf("ListColumns: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("len = %d, want 3 (%+v)", len(cols), cols)
	}
	if cols[0].Field != "id" || cols[0].Type != "bigint(20)" || cols[0].Null != "NO" || cols[0].Key != "PRI" || cols[0].Default != nil || cols[0].Extra != "auto_increment" {
		t.Fatalf("cols[0] = %+v", cols[0])
	}
	if cols[1].Default != "pending" {
		t.Fatalf("cols[1].Default = %v (%T)", cols[1].Default, cols[1].Default)
	}
	if cols[2].Default != "current_timestamp" {
		t.Fatalf("cols[2].Default = %v", cols[2].Default)
	}
	if fdb.lastSQL != "SHOW COLUMNS FROM `orders`" {
		t.Fatalf("SQL = %q", fdb.lastSQL)
	}
}

func TestListColumns_DefaultNullBecomesNil(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
				data: [][]any{
					{"description", "text", "YES", "", nil, ""},
				},
			}, nil
		},
	}
	cols, err := ListColumns(context.Background(), fdb, "orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 {
		t.Fatalf("len = %d", len(cols))
	}
	if cols[0].Default != nil {
		t.Fatalf("Default = %v, want nil", cols[0].Default)
	}
	if cols[0].Null != "YES" {
		t.Fatalf("Null = %q, want YES", cols[0].Null)
	}
}

func TestListColumns_NonStringDefaultRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
				data: [][]any{
					{"b", "datetime", "YES", "", now, ""},
				},
			}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "orders", ""); err == nil || !strings.Contains(err.Error(), "got time.Time") {
		t.Fatalf("got %v", err)
	}
}

func TestListColumns_BytesAndJsonRawMessageDefaults(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
				data: [][]any{
					{"a", "int", "YES", "", []byte("42"), ""},
					{"c", "json", "YES", "", []byte(`{"k":"v"}`), ""},
				},
			}, nil
		},
	}
	cols, err := ListColumns(context.Background(), fdb, "orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 {
		t.Fatalf("len = %d", len(cols))
	}
	if cols[0].Default != "42" {
		t.Fatalf("[0].Default = %v", cols[0].Default)
	}
	if cols[1].Default != `{"k":"v"}` {
		t.Fatalf("[1].Default = %v", cols[1].Default)
	}
}

func TestListColumns_LikeFilterInSQL(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
				data: [][]any{{"id", "int", "NO", "PRI", nil, ""}},
			}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "orders", "id"); err != nil {
		t.Fatal(err)
	}
	if want := "SHOW COLUMNS FROM `orders` LIKE 'id'"; fdb.lastSQL != want {
		t.Fatalf("SQL = %q, want %q", fdb.lastSQL, want)
	}
}

func TestListColumns_InvalidTableRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "a.b", ""); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestListColumns_InvalidLikeRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "orders", "id;DROP"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestListColumns_MissingColumnShape(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Field", "Type", "Null", "Key", "Default"}, // 5 cols, missing Extra
			}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "orders", ""); err == nil || !strings.Contains(err.Error(), "at least 6") {
		t.Fatalf("got %v", err)
	}
}

func TestListColumns_RowsError(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{cols: []string{"Field", "Type", "Null", "Key", "Default", "Extra"}, err: errBoom{msg: "boom"}}, nil
		},
	}
	if _, err := ListColumns(context.Background(), fdb, "orders", ""); err == nil || !strings.Contains(err.Error(), "introspect:") {
		t.Fatalf("got %v", err)
	}
}

func TestListColumns_ReordersToHeaderIndex(t *testing.T) {
	t.Parallel()
	// Permuted header order to prove the function maps by name, not position.
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Type", "Null", "Field", "Key", "Extra", "Default"},
				data: [][]any{
					{"varchar(32)", "NO", "name", "MUL", "", "anon"},
				},
			}, nil
		},
	}
	cols, err := ListColumns(context.Background(), fdb, "users", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 {
		t.Fatalf("len = %d", len(cols))
	}
	if cols[0].Field != "name" || cols[0].Type != "varchar(32)" || cols[0].Null != "NO" || cols[0].Key != "MUL" || cols[0].Extra != "" || cols[0].Default != "anon" {
		t.Fatalf("cols[0] = %+v", cols[0])
	}
}

type errBoom struct{ msg string }

func (e errBoom) Error() string { return e.msg }
