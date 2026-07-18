package introspect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/db"
)

func TestListTables_DefaultAndRows(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
				data: [][]any{
					{"orders"},
					{"order_items"},
					{"shipments"},
				},
			}, nil
		},
	}
	names, err := ListTables(context.Background(), fdb, "", "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	want := []string{"orders", "order_items", "shipments"}
	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(names), len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	if fdb.lastSQL != "SHOW TABLES" {
		t.Fatalf("SQL = %q", fdb.lastSQL)
	}
}

func TestListTables_ExplicitSchemaRewritesSQL(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_audit"},
				data: [][]any{{"events"}, {"audit_log"}},
			}, nil
		},
	}
	names, err := ListTables(context.Background(), fdb, "audit", "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(names) != 2 || names[0] != "events" {
		t.Fatalf("names = %v", names)
	}
	if want := "SHOW TABLES FROM `audit`"; fdb.lastSQL != want {
		t.Fatalf("SQL = %q, want %q", fdb.lastSQL, want)
	}
}

func TestListTables_LikeFilterInSQL(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
				data: [][]any{{"orders"}},
			}, nil
		},
	}
	if _, err := ListTables(context.Background(), fdb, "", "ord"); err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if want := "SHOW TABLES LIKE 'ord'"; fdb.lastSQL != want {
		t.Fatalf("SQL = %q, want %q", fdb.lastSQL, want)
	}
}

func TestListTables_InvalidSchemaRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListTables(context.Background(), fdb, "bogus'--", ""); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("want invalid schema error, got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestListTables_InvalidLikeRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListTables(context.Background(), fdb, "", "ord;DROP"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("want invalid like error, got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestQuoteLikeLiteral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"a", "'a'"},
		{"", "''"},
		{`a'b`, `'a\'b'`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := quoteLikeLiteral(tc.in); got != tc.want {
				t.Fatalf("quoteLikeLiteral(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestListTables_ZeroRowsReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
			}, nil
		},
	}
	names, err := ListTables(context.Background(), fdb, "", "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected empty, got %v", names)
	}
}

func TestListTables_QueryErrorWrapped(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, errors.New("boom")
		},
	}
	if _, err := ListTables(context.Background(), fdb, "", ""); err == nil || !strings.Contains(err.Error(), "introspect:") {
		t.Fatalf("got %v", err)
	}
}

func TestListTables_NonStringCellRejected(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
				data: [][]any{{123}},
			}, nil
		},
	}
	if _, err := ListTables(context.Background(), fdb, "", ""); err == nil || !strings.Contains(err.Error(), "got int") {
		t.Fatalf("got %v", err)
	}
}

func TestListTables_EmptyRowsInResultAreSkipped(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
				data: [][]any{
					{""},
					{"orders"},
					{nil},
				},
			}, nil
		},
	}
	names, err := ListTables(context.Background(), fdb, "", "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(names) != 1 || names[0] != "orders" {
		t.Fatalf("got %v", names)
	}
}

func TestListTables_NilCellBecomesEmptyString(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"Tables_in_wms"},
				data: [][]any{{nil}, {"orders"}},
			}, nil
		},
	}
	names, err := ListTables(context.Background(), fdb, "", "")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(names) != 1 || names[0] != "orders" {
		t.Fatalf("got %v", names)
	}
}

func TestBuildShowTablesSQL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, schema, like, want string
	}{
		{"empty", "", "", "SHOW TABLES"},
		{"schema", "audit", "", "SHOW TABLES FROM `audit`"},
		{"like", "", "ord", "SHOW TABLES LIKE 'ord'"},
		{"both", "audit", "events", "SHOW TABLES FROM `audit` LIKE 'events'"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildShowTablesSQL(tc.schema, tc.like)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
