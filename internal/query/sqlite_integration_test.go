package query_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/query"

	// Pure-Go SQLite driver, same one internal/sqlite uses.
	_ "modernc.org/sqlite"
)

// TestIntegrationSQLite_RoundTrip drives query.RunConnection end-to-end
// against a portable shared in-memory SQLite fixture. This is the offline
// equivalent of the existing optional MySQL integration test (Plan 006 §3).
//
//	export DBX_SQLITE_INTEGRATION=1   # default on; set to 0 to skip
//	go test ./internal/query -count=1 -run SQLiteIntegration
//
// The test is always-on by default: it does not require Docker, credentials,
// or network access. Set DBX_SQLITE_INTEGRATION=0 only if a developer needs
// to temporarily turn it off in a particular environment.
func TestIntegrationSQLite_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sqlite integration test in -short mode")
	}
	if v := strings.ToLower(strings.TrimSpace(getenv("DBX_SQLITE_INTEGRATION"))); v == "0" || v == "false" {
		t.Skip("DBX_SQLITE_INTEGRATION=0 set; skipping")
	}
	// Mirror the MySQL test's intent: deny statements must short-circuit
	// before any SQLite open.
	t.Run("DELETE rejected before open", func(t *testing.T) {
		_, err := query.RunConnection(t.Context(), config.Connection{
			Name:   "tests",
			Driver: "sqlite",
			DSN:    "file:unused_doesnt_matter?mode=memory&cache=shared",
		}, "DELETE FROM t")
		if err == nil {
			t.Fatal("expected policy error for DELETE")
		}
		// The policy error mentions the refusing statement keyword. We don't
		// depend on the exact wording, only that no open side effect occurred
		// (the connection DSN is bogus on purpose).
		if !strings.Contains(err.Error(), "DELETE") {
			t.Errorf("error should mention DELETE refusal, got %v", err)
		}
	})

	t.Run("unsupported driver errors cleanly", func(t *testing.T) {
		_, err := query.RunConnection(t.Context(), config.Connection{
			Name:   "tests",
			Driver: "bogus",
			DSN:    "file:unused?mode=memory&cache=shared",
		}, "SELECT 1")
		if err == nil {
			t.Fatal("expected unsupported driver error")
		}
		if !strings.Contains(err.Error(), "bogus") {
			t.Errorf("error should mention driver, got %v", err)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		ctx := t.Context()

		// Use a unique shared-memory URI per test so parallel subtests
		// don't trample each other's schema.
		dsn := "file:dbx_sqlite_roundtrip_" + sanitize(t.Name()) +
			"?mode=memory&cache=shared"

		// The seeder handle must outlive every Open() call so the
		// shared-cache database remains populated.
		seed, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatalf("seeder sql.Open: %v", err)
		}
		t.Cleanup(func() { _ = seed.Close() })

		if _, err := seed.ExecContext(ctx,
			`CREATE TABLE orders(id INTEGER, status TEXT, meta TEXT)`); err != nil {
			t.Fatalf("CREATE: %v", err)
		}
		// meta is a JSON object literal stored as TEXT so we exercise the
		// "nested JSON string" round-trip path documented in Plan 006.
		if _, err := seed.ExecContext(ctx,
			`INSERT INTO orders VALUES (1, 'pending', '{"source":"shopify","fulfillment":{"status":"created"}}')`); err != nil {
			t.Fatalf("INSERT: %v", err)
		}

		out, err := query.RunConnection(ctx, config.Connection{
			Name:   "tests",
			Driver: "sqlite",
			DSN:    dsn,
		}, "SELECT id, status, meta FROM orders ORDER BY id")
		if err != nil {
			t.Fatalf("RunConnection: %v", err)
		}

		// Pretty JSON contract: trailing newline + 2-space indent.
		if len(out) == 0 || out[len(out)-1] != '\n' {
			t.Fatalf("expected trailing newline, got %q", out)
		}
		if !bytes.Contains(out, []byte("\n  ")) {
			t.Errorf("expected pretty 2-space indent:\n%s", out)
		}

		var rows []map[string]any
		if err := json.Unmarshal(out, &rows); err != nil {
			t.Fatalf("stdout is not valid JSON array: %v\n%s", err, out)
		}
		if len(rows) != 1 {
			t.Fatalf("want 1 row, got %d: %s", len(rows), out)
		}

		row := rows[0]
		// SQLite driver delivers INTEGER as int64; encoding/json then
		// unmarshals it back into a json.Number (or float64). Cover both.
		if got := row["id"]; !numericEqual(got, 1) {
			t.Errorf("id: got %#v want 1", got)
		}
		if got, ok := row["status"].(string); !ok || got != "pending" {
			t.Errorf("status: got %#v want %q", row["status"], "pending")
		}
		// meta is auto-parsed by jsonutil because the TEXT is valid JSON.
		meta, ok := row["meta"].(map[string]any)
		if !ok {
			t.Fatalf("meta: want nested object, got %#v", row["meta"])
		}
		if meta["source"] != "shopify" {
			t.Errorf("meta.source: got %#v want shopify", meta["source"])
		}
		ff, ok := meta["fulfillment"].(map[string]any)
		if !ok {
			t.Fatalf("meta.fulfillment: want nested object, got %#v", meta["fulfillment"])
		}
		if ff["status"] != "created" {
			t.Errorf("meta.fulfillment.status: got %#v want created", ff["status"])
		}
	})
}

func numericEqual(v any, want float64) bool {
	switch n := v.(type) {
	case float64:
		return n == want
	case json.Number:
		f, err := n.Float64()
		return err == nil && f == want
	case int64:
		return float64(n) == want
	case int:
		return float64(n) == want
	default:
		return false
	}
}

// sanitize turns t.Name() into something safe to embed in a URI fragment:
// it strips filesystem-weird characters and replaces spaces with underscores.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_' || r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\' || r == '*':
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

// getenv is a tiny indirection so the test can be cheaply toggled without
// pulling in os at the call sites (and to keep this file self-contained).
func getenv(key string) string {
	return os.Getenv(key)
}
