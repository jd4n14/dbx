package ddl_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jd4n14/dbx/internal/config"
	"github.com/jd4n14/dbx/internal/ddl"
	"github.com/jd4n14/dbx/internal/mysql"
)

// Optional live MySQL. Skipped when DBX_MYSQL_TEST_DSN is unset
// so `go test ./...` stays offline-green.
//
//	export DBX_MYSQL_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/dbname'
//	go test ./internal/ddl/ -count=1 -v -run Integration
func TestIntegration_ShowCreateTable(t *testing.T) {
	dsn := os.Getenv("DBX_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("DBX_MYSQL_TEST_DSN unset; skipping live MySQL integration test")
	}
	conn := config.Connection{Name: "integration", Driver: "mysql", DSN: dsn}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	dbase, err := mysql.Open(ctx, conn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer dbase.Close()

	const table = "dbx_ddl_it"
	r, err := dbase.QueryContext(ctx, "CREATE TABLE IF NOT EXISTS `"+table+"` (id INT PRIMARY KEY)")
	if err != nil {
		t.Skipf("CREATE TABLE not permitted: %v", err)
	}
	_ = r.Close()
	defer func() {
		r, err := dbase.QueryContext(context.Background(), "DROP TABLE IF EXISTS `"+table+"`")
		if err == nil {
			_ = r.Close()
		}
	}()

	text, err := ddl.Fetch(ctx, dbase, table)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(strings.ToUpper(text), "CREATE TABLE") {
		t.Fatalf("DDL: %s", text)
	}
	if !strings.Contains(text, table) {
		t.Fatalf("expected table name in DDL: %s", text)
	}
}
