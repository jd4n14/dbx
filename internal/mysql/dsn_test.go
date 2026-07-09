package mysql

import (
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"github.com/jd4n14/dbx/internal/config"
)

func TestBuildDSN_FieldBuiltForceSafe(t *testing.T) {
	conn := config.Connection{
		Name:     "local",
		Host:     "127.0.0.1",
		Port:     3306,
		User:     "root",
		Password: "secret",
		Database: "wms",
	}
	dsn, err := BuildDSN(conn)
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}

	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN output: %v\nDSN: %s", err, dsn)
	}

	if cfg.MultiStatements {
		t.Errorf("MultiStatements: got true, want false")
	}
	if !cfg.ParseTime {
		t.Errorf("ParseTime: got false, want true")
	}
	if cfg.Loc != time.UTC {
		t.Errorf("Loc: got %v, want UTC", cfg.Loc)
	}
	if cfg.Timeout != DialTimeout {
		t.Errorf("Timeout: got %v, want %v", cfg.Timeout, DialTimeout)
	}
	if cfg.ReadTimeout != ReadTimeout {
		t.Errorf("ReadTimeout: got %v, want %v", cfg.ReadTimeout, ReadTimeout)
	}
	if cfg.WriteTimeout != WriteTimeout {
		t.Errorf("WriteTimeout: got %v, want %v", cfg.WriteTimeout, WriteTimeout)
	}
	if cfg.User != "root" || cfg.Passwd != "secret" || cfg.DBName != "wms" {
		t.Errorf("auth/db fields: user=%q pass=%q db=%q", cfg.User, cfg.Passwd, cfg.DBName)
	}
	if cfg.Addr != "127.0.0.1:3306" {
		t.Errorf("Addr: got %q", cfg.Addr)
	}
	// parseTime must appear in formatted DSN; multiStatements must not be true.
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN missing parseTime=true: %s", dsn)
	}
	if strings.Contains(strings.ToLower(dsn), "multistatements=true") {
		t.Errorf("DSN must not enable multiStatements: %s", dsn)
	}
	if !strings.Contains(dsn, "timeout=10s") {
		t.Errorf("DSN missing dial timeout: %s", dsn)
	}
	if !strings.Contains(dsn, "readTimeout=30s") {
		t.Errorf("DSN missing readTimeout: %s", dsn)
	}
	if !strings.Contains(dsn, "writeTimeout=30s") {
		t.Errorf("DSN missing writeTimeout: %s", dsn)
	}
	if !strings.Contains(dsn, "charset=utf8mb4") {
		t.Errorf("field-built DSN should set charset=utf8mb4: %s", dsn)
	}
}

func TestBuildDSN_PasswordSpecialCharactersEscaped(t *testing.T) {
	// Special characters that must round-trip via FormatDSN / ParseDSN.
	pass := `p@ss:w/ord?&=#%`
	conn := config.Connection{
		Name:     "special",
		Host:     "db.example.com",
		Port:     3307,
		User:     "u$er",
		Password: pass,
		Database: "app",
	}
	dsn, err := BuildDSN(conn)
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v\nDSN: %s", err, dsn)
	}
	if cfg.Passwd != pass {
		t.Errorf("password round-trip: got %q want %q", cfg.Passwd, pass)
	}
	if cfg.User != "u$er" {
		t.Errorf("user: got %q", cfg.User)
	}
}

func TestBuildDSN_RawMultiStatementsForcedOff(t *testing.T) {
	// Required: raw DSN cannot re-enable multiStatements.
	raw := "root:secret@tcp(127.0.0.1:3306)/wms?multiStatements=true&parseTime=false&timeout=1s"
	conn := config.Connection{
		Name: "raw",
		DSN:  raw,
	}
	dsn, err := BuildDSN(conn)
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}

	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN output: %v\nDSN: %s", err, dsn)
	}
	if cfg.MultiStatements {
		t.Fatalf("raw multiStatements=true must be forced false; DSN=%s", dsn)
	}
	if strings.Contains(strings.ToLower(dsn), "multistatements=true") {
		t.Fatalf("formatted DSN still has multiStatements=true: %s", dsn)
	}
	if !cfg.ParseTime {
		t.Errorf("ParseTime must be forced true")
	}
	if cfg.Loc != time.UTC {
		t.Errorf("Loc must be forced UTC, got %v", cfg.Loc)
	}
	if cfg.Timeout != DialTimeout {
		t.Errorf("Timeout must be forced to %v, got %v", DialTimeout, cfg.Timeout)
	}
	if cfg.ReadTimeout != ReadTimeout {
		t.Errorf("ReadTimeout must be forced to %v, got %v", ReadTimeout, cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != WriteTimeout {
		t.Errorf("WriteTimeout must be forced to %v, got %v", WriteTimeout, cfg.WriteTimeout)
	}
}

func TestBuildDSN_RawInvalidError(t *testing.T) {
	conn := config.Connection{
		Name: "bad",
		DSN:  "not-a-valid-dsn-missing-slash",
	}
	_, err := BuildDSN(conn)
	if err == nil {
		t.Fatal("expected error for invalid raw DSN")
	}
	if !strings.Contains(err.Error(), "invalid dsn") {
		t.Errorf("error should mention invalid dsn: %v", err)
	}
	// Must not look like a silent passthrough success path.
	if strings.Contains(err.Error(), "not-a-valid-dsn-missing-slash@") {
		t.Errorf("should not invent a passthrough DSN: %v", err)
	}
}

func TestBuildDSN_DefaultPort(t *testing.T) {
	conn := config.Connection{
		Name:     "p",
		Host:     "localhost",
		User:     "u",
		Password: "p",
		Database: "d",
		// Port 0 → default 3306
	}
	dsn, err := BuildDSN(conn)
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.Addr != "localhost:3306" {
		t.Errorf("Addr: got %q want localhost:3306", cfg.Addr)
	}
}
