// Package query validates and executes read/inspect SQL.
//
// Safety model (plan §4.1.B):
//
//	The SQL allowlist in ValidateQuery is the write barrier for this MVP.
//	database/sql QueryContext is NOT a write barrier — MySQL can still execute
//	non-SELECT statements (including DML) when sent via Query in many cases.
//	Driver-level multiStatements=false is defense-in-depth only.
//
// Allowlist (first keyword after leading whitespace/comments):
//
//	SELECT, WITH, SHOW, DESCRIBE/DESC, EXPLAIN
//
// WITH requires a secondary check: top-level (paren-depth 0) DML/DDL keywords
// after the CTE preamble are denied (e.g. WITH c AS (SELECT 1) DELETE FROM t).
//
// Multi-statement input is rejected (naive semicolon split; single trailing
// semicolon allowed). Known MVP limitation: ';' inside string/JSON literals
// false-positives — fail-closed is intentional.
//
// Residual risks (not blocked by this policy):
//
//	SELECT … INTO OUTFILE / DUMPFILE, SELECT … FOR UPDATE, SELECT … INTO @var
package query

import (
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/sqllex"
)

// Deny keywords for WITH secondary scan and for error messaging on bare DML.
// Case-insensitive whole-word tokens at paren depth 0 (after comment strip).
var withDenyKeywords = map[string]struct{}{
	"INSERT":   {},
	"UPDATE":   {},
	"DELETE":   {},
	"REPLACE":  {},
	"LOAD":     {},
	"CALL":     {},
	"DROP":     {},
	"TRUNCATE": {},
	"ALTER":    {},
	"CREATE":   {},
	"RENAME":   {},
	"GRANT":    {},
	"REVOKE":   {},
	"SET":      {},
	"LOCK":     {},
	"UNLOCK":   {},
	"HANDLER":  {},
	"DO":       {},
}

var allowedFirstKeywords = map[string]struct{}{
	"SELECT":   {},
	"WITH":     {},
	"SHOW":     {},
	"DESCRIBE": {},
	"DESC":     {},
	"EXPLAIN":  {},
}

// ValidateQuery enforces the read/inspect SQL policy.
//
// This function is the write barrier. Call it before Open/QueryContext.
func ValidateQuery(sql string) error {
	scan := sqllex.Lex(sql)
	if !scan.HasContent || len(scan.Tokens) == 0 {
		return fmt.Errorf("query is empty")
	}

	if err := rejectMultiStatement(sql); err != nil {
		return err
	}
	kw := scan.Tokens[0].Word
	if kw == "" {
		return fmt.Errorf("query only allows read/inspect statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN); refused: (unrecognized)")
	}

	if _, ok := allowedFirstKeywords[kw]; !ok {
		return fmt.Errorf("query only allows read/inspect statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN); refused: %s", kw)
	}

	if kw == "WITH" {
		if denied := findTopLevelDenyKeyword(scan.Tokens); denied != "" {
			return fmt.Errorf("query only allows read/inspect statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN); refused: WITH ... %s", denied)
		}
	}

	return nil
}

// rejectMultiStatement allows at most one statement (optional single trailing ';').
// Naive: after trim, strip one trailing ';', then any remaining ';' is multi-statement.
// Known MVP limitation: false-positive on ';' inside string/JSON literals (fail-closed).
func rejectMultiStatement(s string) error {
	body := strings.TrimSpace(s)
	if strings.HasSuffix(body, ";") {
		body = strings.TrimSpace(body[:len(body)-1])
	}
	if strings.Contains(body, ";") {
		return fmt.Errorf("query must be a single statement (multiple statements are not allowed)")
	}
	return nil
}

// findTopLevelDenyKeyword walks SQL at paren depth 0 and returns the first
// deny-list keyword, or "" if none. Used for WITH … DML/DDL.
func findTopLevelDenyKeyword(tokens []sqllex.Token) string {
	for _, token := range tokens[1:] {
		if token.Depth == 0 {
			if _, deny := withDenyKeywords[token.Word]; deny {
				return token.Word
			}
		}
	}
	return ""
}
