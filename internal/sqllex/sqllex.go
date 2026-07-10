// Package sqllex provides the small, conservative SQL lexer shared by the
// execution policy and advisory danger analysis. It is intentionally not a
// parser: it only identifies unquoted words, parentheses, and statement
// separators while honoring MySQL strings, identifiers, and comments.
package sqllex

import (
	"strings"
	"unicode"
)

// Token is an upper-cased ASCII SQL word and its parenthesis depth.
type Token struct {
	Word  string
	Depth int
}

// Scan is the lexical view of SQL used by callers.
type Scan struct {
	Tokens     []Token
	Semicolons []int
	HasContent bool
}

// Lex scans SQL without interpreting quoted or commented text.
func Lex(sql string) Scan {
	var out Scan
	depth := 0
	for i := 0; i < len(sql); {
		c := sql[i]
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if i+1 < len(sql) && c == '-' && sql[i+1] == '-' {
			i += 2
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if c == '#' {
			i++
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(sql) && c == '/' && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < len(sql) {
				i += 2
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			out.HasContent = true
			quote := c
			i++
			for i < len(sql) {
				ch := sql[i]
				i++
				if ch == '\\' && i < len(sql) {
					i++
					continue
				}
				if ch == quote {
					if i < len(sql) && sql[i] == quote {
						i++
						continue
					}
					break
				}
			}
			continue
		}
		switch c {
		case '(':
			out.HasContent = true
			depth++
			i++
			continue
		case ')':
			out.HasContent = true
			if depth > 0 {
				depth--
			}
			i++
			continue
		case ';':
			out.Semicolons = append(out.Semicolons, i)
			i++
			continue
		}
		if isIdentStart(c) {
			start := i
			i++
			for i < len(sql) && isIdentPart(sql[i]) {
				i++
			}
			out.HasContent = true
			out.Tokens = append(out.Tokens, Token{Word: strings.ToUpper(sql[start:i]), Depth: depth})
			continue
		}
		out.HasContent = true
		i++
	}
	return out
}

// HasMultipleStatements reports whether a semicolon is followed by SQL
// content other than whitespace or comments. A single trailing semicolon is
// accepted; semicolons inside quotes and comments are ignored.
func HasMultipleStatements(sql string) bool {
	s := Lex(sql)
	for _, pos := range s.Semicolons {
		if Lex(sql[pos+1:]).HasContent {
			return true
		}
	}
	return false
}

func isIdentStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c >= 0x80
}
