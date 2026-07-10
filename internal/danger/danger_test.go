package danger

import "testing"

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name, sql, env, code string
		severity             Severity
		safe                 bool
	}{
		{"safe select", "SELECT 'DELETE', `UPDATE` FROM café", "", "", SeveritySafe, true},
		{"comments", "/* DELETE */ -- UPDATE\nSELECT 1", "", "", SeveritySafe, true},
		{"multiple", "SELECT ';'; DELETE FROM t", "", "multiple_statements", SeverityCritical, false},
		{"drop", "DROP TABLE t", "", "drop_statement", SeverityCritical, false},
		{"truncate", "TRUNCATE t", "", "truncate_statement", SeverityCritical, false},
		{"alter", "ALTER TABLE t ADD x INT", "", "alter_statement", SeverityCritical, false},
		{"create index", "CREATE INDEX ix ON t (id)", "", "create_index", SeverityCritical, false},
		{"create unique index", "CREATE UNIQUE INDEX ix ON t (id)", "", "create_index", SeverityCritical, false},
		{"update no where", "UPDATE t SET note='WHERE'", "", "update_without_where", SeverityCritical, false},
		{"delete no where", "DELETE FROM t /* WHERE x */", "", "delete_without_where", SeverityCritical, false},
		{"update where", "UPDATE t SET x=1 WHERE id=2", "", "write_statement", SeverityWarning, false},
		{"other write", "INSERT INTO t VALUES (1)", "", "write_statement", SeverityWarning, false},
		{"outfile", "SELECT * FROM t INTO OUTFILE '/tmp/x'", "", "select_into_file", SeverityWarning, false},
		{"dumpfile", "SELECT x INTO DUMPFILE '/tmp/x' FROM t", "", "select_into_file", SeverityWarning, false},
		{"for update", "SELECT * FROM t FOR UPDATE", "", "select_for_update", SeverityWarning, false},
		{"nested cte read", "WITH c AS (WITH d AS (SELECT 1) SELECT * FROM d) SELECT * FROM c", "", "", SeveritySafe, true},
		{"cte delete", "WITH c AS (SELECT 1) DELETE FROM t WHERE id IN (SELECT * FROM c)", "prod", "restricted_environment_write", SeverityCritical, false},
		{"readonly", "UPDATE t SET x=1 WHERE id=2", "readonly", "restricted_environment_write", SeverityCritical, false},
		{"unrecognized", "FROBULATE t", "", "unrecognized_statement", SeverityWarning, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Analyze(tc.sql, tc.env)
			if err != nil {
				t.Fatal(err)
			}
			if r.Type != "danger" || r.Safe != tc.safe || r.Severity != tc.severity {
				t.Fatalf("result=%+v", r)
			}
			if tc.code != "" && !containsCode(r, tc.code) {
				t.Fatalf("missing %q: %+v", tc.code, r)
			}
		})
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	for _, sql := range []string{"", "  ", "-- only", "/* only */"} {
		if _, err := Analyze(sql, ""); err == nil {
			t.Fatalf("Analyze(%q) should fail", sql)
		}
	}
}

func containsCode(r Result, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
