package introspect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/db"
)

func TestListForeignKeys_Empty(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "CONSTRAINT_NAME", "UPDATE_RULE", "DELETE_RULE"},
				data: nil,
			}, nil
		},
	}
	out, err := ListForeignKeys(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("len = %d, want 0", len(out))
	}
	if !strings.Contains(fdb.lastSQL, "information_schema.KEY_COLUMN_USAGE") {
		t.Fatalf("SQL = %q", fdb.lastSQL)
	}
	if !strings.Contains(fdb.lastSQL, "REFERENTIAL_CONSTRAINTS") {
		t.Fatalf("SQL must join REFERENTIAL_CONSTRAINTS, got %q", fdb.lastSQL)
	}
	if !strings.Contains(fdb.lastSQL, "REFERENCED_TABLE_NAME IS NOT NULL") {
		t.Fatalf("SQL must filter REFERENCES_TABLE_NAME IS NOT NULL, got %q", fdb.lastSQL)
	}
}

func TestListForeignKeys_SingleFK(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "CONSTRAINT_NAME", "UPDATE_RULE", "DELETE_RULE"},
				data: [][]any{
					{"customer_id", "wms", "customers", "id", "fk_orders_customer", "RESTRICT", "CASCADE"},
				},
			}, nil
		},
	}
	out, err := ListForeignKeys(context.Background(), fdb, "orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	want := ForeignKey{
		Name:             "fk_orders_customer",
		Column:           "customer_id",
		ReferencedSchema: "wms",
		ReferencedTable:  "customers",
		ReferencedColumn: "id",
		UpdateRule:       "RESTRICT",
		DeleteRule:       "CASCADE",
	}
	if out[0] != want {
		t.Fatalf("got %+v want %+v", out[0], want)
	}
}

func TestListForeignKeys_MultiColumnFK(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "CONSTRAINT_NAME", "UPDATE_RULE", "DELETE_RULE"},
				data: [][]any{
					{"tenant_id", "wms", "users", "tenant_id", "fk_session_user", "RESTRICT", "RESTRICT"},
					{"user_id", "wms", "users", "id", "fk_session_user", "RESTRICT", "RESTRICT"},
				},
			}, nil
		},
	}
	out, err := ListForeignKeys(context.Background(), fdb, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Name != "fk_session_user" || out[0].Column != "tenant_id" || out[0].ReferencedColumn != "tenant_id" {
		t.Fatalf("out[0] = %+v", out[0])
	}
	if out[1].Name != "fk_session_user" || out[1].Column != "user_id" || out[1].ReferencedColumn != "id" {
		t.Fatalf("out[1] = %+v", out[1])
	}
}

func TestListForeignKeys_SelfReference(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "CONSTRAINT_NAME", "UPDATE_RULE", "DELETE_RULE"},
				data: [][]any{
					{"parent_id", "wms", "categories", "id", "fk_cat_parent", "CASCADE", "SET NULL"},
				},
			}, nil
		},
	}
	out, err := ListForeignKeys(context.Background(), fdb, "categories")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].ReferencedTable != "categories" {
		t.Fatalf("expected self-reference; got %+v", out[0])
	}
}

func TestListForeignKeys_InvalidTableRejectsBeforeQuery(t *testing.T) {
	t.Parallel()
	called := false
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			called = true
			return &fakeRows{}, nil
		},
	}
	if _, err := ListForeignKeys(context.Background(), fdb, "a.b"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
	if called {
		t.Fatal("validation must reject before any QueryContext call")
	}
	if fdb.calls != 0 {
		t.Fatalf("calls = %d, want 0", fdb.calls)
	}
}

func TestListForeignKeys_MissingColumnShape(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_NAME"}, // missing 5
			}, nil
		},
	}
	if _, err := ListForeignKeys(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "missing required columns") {
		t.Fatalf("got %v", err)
	}
}

func TestListForeignKeys_QueryError(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return nil, errors.New("boom")
		},
	}
	if _, err := ListForeignKeys(context.Background(), fdb, "orders"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func TestListForeignKeys_TableBoundAsArgument(t *testing.T) {
	t.Parallel()
	var gotArg any
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			if len(args) > 0 {
				gotArg = args[0]
			}
			return &fakeRows{
				cols: []string{"COLUMN_NAME", "REFERENCED_TABLE_SCHEMA", "REFERENCED_TABLE_NAME", "REFERENCED_COLUMN_NAME", "CONSTRAINT_NAME", "UPDATE_RULE", "DELETE_RULE"},
			}, nil
		},
	}
	if _, err := ListForeignKeys(context.Background(), fdb, "orders"); err != nil {
		t.Fatal(err)
	}
	if gotArg != "orders" {
		t.Fatalf("arg = %v (%T)", gotArg, gotArg)
	}
}

func TestListForeignKeys_HeaderReorderResilient(t *testing.T) {
	t.Parallel()
	fdb := &fakeDB{
		queryFn: func(ctx context.Context, query string, args ...any) (db.Rows, error) {
			return &fakeRows{
				cols: []string{"CONSTRAINT_NAME", "DELETE_RULE", "UPDATE_RULE", "REFERENCED_COLUMN_NAME", "REFERENCED_TABLE_NAME", "REFERENCED_TABLE_SCHEMA", "COLUMN_NAME"},
				data: [][]any{
					{"fk_x", "NO ACTION", "RESTRICT", "id", "parents", "wms", "parent_id"},
				},
			}, nil
		},
	}
	out, err := ListForeignKeys(context.Background(), fdb, "children")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Name != "fk_x" || out[0].Column != "parent_id" || out[0].ReferencedTable != "parents" || out[0].UpdateRule != "RESTRICT" || out[0].DeleteRule != "NO ACTION" {
		t.Fatalf("out[0] = %+v", out[0])
	}
}
