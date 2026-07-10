package query

import (
	"strings"
	"testing"
)

func TestValidateQuery_Allow(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"select", "SELECT 1"},
		{"select_lower", "select id from t"},
		{"select_trailing_semi", "SELECT 1;"},
		{"select_leading_ws", "  \n  SELECT 1 AS n"},
		{"select_line_comment", "-- comment\nSELECT 1"},
		{"select_block_comment", "/* c */ SELECT 1"},
		{"with_select", "WITH c AS (SELECT 1) SELECT * FROM c"},
		{"with_select_lower", "with c as (select 1) select * from c"},
		{"with_trailing_semi", "WITH c AS (SELECT 1) SELECT * FROM c;"},
		{"show", "SHOW TABLES"},
		{"show_create", "SHOW CREATE TABLE t"},
		{"desc", "DESC t"},
		{"describe", "DESCRIBE t"},
		{"explain", "EXPLAIN SELECT 1"},
		{"explain_update", "EXPLAIN UPDATE t SET x=1"},
		{"quoted_keyword", "SELECT 'DELETE', \"UPDATE\", `DROP` FROM t"},
		{"commented_keyword", "SELECT 1 /* DELETE FROM t */"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateQuery(tc.sql); err != nil {
				t.Fatalf("ValidateQuery(%q) unexpected error: %v", tc.sql, err)
			}
		})
	}
}

func TestValidateQuery_DenyDMLAndDDL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string // substring in error
	}{
		{"update", "UPDATE t SET x=1", "UPDATE"},
		{"delete", "DELETE FROM t", "DELETE"},
		{"insert", "INSERT INTO t VALUES (1)", "INSERT"},
		{"drop", "DROP TABLE t", "DROP"},
		{"truncate", "TRUNCATE TABLE t", "TRUNCATE"},
		{"alter", "ALTER TABLE t ADD c INT", "ALTER"},
		{"create", "CREATE TABLE t (id INT)", "CREATE"},
		{"replace", "REPLACE INTO t VALUES (1)", "REPLACE"},
		{"call", "CALL sp()", "CALL"},
		{"set", "SET @x = 1", "SET"},
		{"use", "USE otherdb", "USE"},
		{"load", "LOAD DATA INFILE 'f' INTO TABLE t", "LOAD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQuery(tc.sql)
			if err == nil {
				t.Fatalf("ValidateQuery(%q) want error", tc.sql)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err.Error(), tc.want)
			}
			if !strings.Contains(err.Error(), "read/inspect") {
				t.Errorf("error should mention read/inspect policy: %v", err)
			}
		})
	}
}

func TestValidateQuery_DenyCTEWithDML(t *testing.T) {
	// Required Phase 3a cases (plan-review B1).
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "with_delete",
			sql:  "WITH c AS (SELECT 1) DELETE FROM t",
			want: "DELETE",
		},
		{
			name: "with_update",
			sql:  "WITH c AS (SELECT 1) UPDATE t SET x=1",
			want: "UPDATE",
		},
		{
			name: "with_insert",
			sql:  "WITH c AS (SELECT 1) INSERT INTO t VALUES (1)",
			want: "INSERT",
		},
		{
			name: "with_drop",
			sql:  "WITH c AS (SELECT 1) DROP TABLE t",
			want: "DROP",
		},
		{
			name: "with_delete_lower",
			sql:  "with c as (select 1) delete from t",
			want: "DELETE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQuery(tc.sql)
			if err == nil {
				t.Fatalf("ValidateQuery(%q) want deny", tc.sql)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateQuery_AllowCTERead(t *testing.T) {
	sql := "WITH c AS (SELECT 1) SELECT * FROM c"
	if err := ValidateQuery(sql); err != nil {
		t.Fatalf("read CTE must be allowed: %v", err)
	}
}

func TestValidateQuery_Empty(t *testing.T) {
	cases := []string{"", "   ", "\n\t", "-- only comment", "/* only */"}
	for _, sql := range cases {
		err := ValidateQuery(sql)
		if err == nil {
			t.Fatalf("ValidateQuery(%q) want empty error", sql)
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error should mention empty: %v", err)
		}
	}
}

func TestValidateQuery_MultiStatement(t *testing.T) {
	cases := []string{
		"SELECT 1; DROP TABLE x",
		"SELECT 1; SELECT 2",
		"SELECT 1; DROP TABLE x;",
		"WITH c AS (SELECT 1) SELECT * FROM c; DELETE FROM t",
	}
	for _, sql := range cases {
		err := ValidateQuery(sql)
		if err == nil {
			t.Fatalf("ValidateQuery(%q) want multi-statement error", sql)
		}
		if !strings.Contains(err.Error(), "single statement") {
			t.Errorf("error should mention single statement: %v", err)
		}
	}
}

func TestValidateQuery_LeadingCommentThenDML(t *testing.T) {
	err := ValidateQuery("-- safe looking\nDELETE FROM t")
	if err == nil {
		t.Fatal("want deny for DELETE after comment")
	}
	if !strings.Contains(err.Error(), "DELETE") {
		t.Errorf("error: %v", err)
	}
}
