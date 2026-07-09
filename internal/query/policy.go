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
	"unicode"
)

// Deny keywords for WITH secondary scan and for error messaging on bare DML.
// Case-insensitive whole-word tokens at paren depth 0 (after comment strip).
var withDenyKeywords = map[string]struct{}{
	"INSERT":  {},
	"UPDATE":  {},
	"DELETE":  {},
	"REPLACE": {},
	"LOAD":    {},
	"CALL":    {},
	"DROP":    {},
	"TRUNCATE": {},
	"ALTER":   {},
	"CREATE":  {},
	"RENAME":  {},
	"GRANT":   {},
	"REVOKE":  {},
	"SET":     {},
	"LOCK":    {},
	"UNLOCK":  {},
	"HANDLER": {},
	"DO":      {},
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
	s := stripLeadingTrivia(sql)
	if s == "" {
		return fmt.Errorf("query is empty")
	}

	if err := rejectMultiStatement(s); err != nil {
		return err
	}

	// Normalize for keyword analysis: drop a single trailing semicolon.
	body := strings.TrimSpace(s)
	if strings.HasSuffix(body, ";") {
		body = strings.TrimSpace(body[:len(body)-1])
	}
	if body == "" {
		return fmt.Errorf("query is empty")
	}

	// Strip comments for keyword/token analysis (fail-closed multi-statement
	// check above used the raw leading-stripped text so ';' in comments still
	// reject — acceptable MVP limitation).
	scanBody := stripSQLComments(body)
	scanBody = strings.TrimSpace(scanBody)
	if scanBody == "" {
		return fmt.Errorf("query is empty")
	}

	kw := firstKeyword(scanBody)
	if kw == "" {
		return fmt.Errorf("query only allows read/inspect statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN); refused: (unrecognized)")
	}

	if _, ok := allowedFirstKeywords[kw]; !ok {
		return fmt.Errorf("query only allows read/inspect statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN); refused: %s", kw)
	}

	if kw == "WITH" {
		if denied := findTopLevelDenyKeyword(scanBody); denied != "" {
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

// stripLeadingTrivia removes leading whitespace and simple leading -- / /* */ comments.
func stripLeadingTrivia(s string) string {
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		if s == "" {
			return s
		}
		if strings.HasPrefix(s, "--") {
			// Line comment through newline.
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			// Unclosed block comment — fail-closed empty.
			return ""
		}
		return s
	}
}

// stripSQLComments removes -- line and /* */ block comments for keyword scans.
// Not a full lexer; string literals are not preserved (MVP fail-closed OK).
func stripSQLComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			// Line comment.
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				break
			}
			i = i + 2 + end + 2
			continue
		}
		// Minimal string skip so keywords inside quotes are less likely to trip
		// WITH secondary scan (still not a full SQL lexer).
		if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
			quote := s[i]
			b.WriteByte(quote)
			i++
			for i < len(s) {
				c := s[i]
				b.WriteByte(c)
				i++
				if c == '\\' && i < len(s) {
					b.WriteByte(s[i])
					i++
					continue
				}
				if c == quote {
					// MySQL escaped quote by doubling.
					if i < len(s) && s[i] == quote {
						b.WriteByte(s[i])
						i++
						continue
					}
					break
				}
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// firstKeyword returns the uppercased first SQL keyword token (ASCII letters/underscore).
func firstKeyword(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	if s == "" {
		return ""
	}
	end := 0
	for end < len(s) && isIdentPart(s[end]) {
		// SQL keywords are ASCII; digits allowed after first char only.
		if end == 0 && !isIdentStart(s[end]) {
			break
		}
		end++
	}
	if end == 0 || !isIdentStart(s[0]) {
		return ""
	}
	return strings.ToUpper(s[:end])
}

// findTopLevelDenyKeyword walks SQL at paren depth 0 and returns the first
// deny-list keyword (uppercased), or "" if none. Used for WITH … DML/DDL.
func findTopLevelDenyKeyword(s string) string {
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		case '\'', '"', '`':
			// Skip quoted spans (same rules as stripSQLComments).
			quote := c
			i++
			for i < len(s) {
				ch := s[i]
				i++
				if ch == '\\' && i < len(s) {
					i++
					continue
				}
				if ch == quote {
					if i < len(s) && s[i] == quote {
						i++
						continue
					}
					break
				}
			}
			continue
		}

		if depth == 0 && isIdentStart(c) {
			start := i
			i++
			for i < len(s) && isIdentPart(s[i]) {
				i++
			}
			word := strings.ToUpper(s[start:i])
			if _, deny := withDenyKeywords[word]; deny {
				return word
			}
			continue
		}
		i++
	}
	return ""
}

func isIdentStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}
