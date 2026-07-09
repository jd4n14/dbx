package query_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/query"
)

// Optional live MySQL round-trip. Skipped when DBX_MYSQL_TEST_DSN is unset
// so `go test ./...` stays offline-green (plan §4.1.D / Phase 5).
//
//	export DBX_MYSQL_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/dbname'
//	go test ./internal/query/ -count=1 -v -run Integration
func TestIntegration_SelectConstant(t *testing.T) {
	dsn := os.Getenv("DBX_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("DBX_MYSQL_TEST_DSN unset; skipping live MySQL integration test")
	}

	conn := config.Connection{
		Name:   "integration",
		Driver: "mysql",
		DSN:    dsn,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	sqlText := `SELECT 1 AS id, 'hello' AS label, CAST('12.34' AS DECIMAL(10,2)) AS amount`
	out, err := query.RunConnection(ctx, conn, sqlText)
	if err != nil {
		t.Fatalf("RunConnection: %v", err)
	}

	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Fatalf("expected trailing newline in pretty JSON, got %q", out)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("stdout is not valid JSON array: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %s", len(rows), out)
	}

	row := rows[0]
	// MySQL may return integer as float64 via encoding/json number decode.
	if got := row["id"]; !jsonNumberEqual(got, 1) {
		t.Errorf("id: got %#v want 1", got)
	}
	if got, ok := row["label"].(string); !ok || got != "hello" {
		t.Errorf("label: got %#v want %q", row["label"], "hello")
	}
	// DECIMAL must remain a JSON string for precision (MVP contract).
	if got, ok := row["amount"].(string); !ok || got != "12.34" {
		t.Errorf("amount (DECIMAL as string): got %#v want %q", row["amount"], "12.34")
	}

	// Pretty-print contract: 2-space indent is what MarshalIndent produces;
	// at least ensure it is not a single-line compact array of objects.
	if !bytes.Contains(out, []byte("\n  ")) {
		t.Errorf("expected pretty-printed JSON with 2-space indent, got:\n%s", out)
	}
}

func jsonNumberEqual(v any, want float64) bool {
	switch n := v.(type) {
	case float64:
		return n == want
	case json.Number:
		f, err := n.Float64()
		return err == nil && f == want
	case int:
		return float64(n) == want
	case int64:
		return float64(n) == want
	default:
		return false
	}
}
