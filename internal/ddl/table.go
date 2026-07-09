// Package ddl fetches MySQL table DDL (SHOW CREATE TABLE) for the dbx CLI.
package ddl

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxTableNameLen is MySQL's maximum identifier length.
const MaxTableNameLen = 64

// ValidateTableName checks a simple ASCII table identifier (no schema.table).
// Leading/trailing space is trimmed before validation.
func ValidateTableName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("invalid table name: must be a simple identifier (letters, digits, underscore; max 64)")
	}
	if len(name) > MaxTableNameLen {
		return fmt.Errorf("invalid table name: must be a simple identifier (letters, digits, underscore; max 64)")
	}
	for i, r := range name {
		if r > unicode.MaxASCII {
			return fmt.Errorf("invalid table name: must be a simple identifier (letters, digits, underscore; max 64)")
		}
		c := byte(r)
		if i == 0 {
			if !isIdentStart(c) {
				return fmt.Errorf("invalid table name: must be a simple identifier (letters, digits, underscore; max 64)")
			}
			continue
		}
		if !isIdentPart(c) {
			return fmt.Errorf("invalid table name: must be a simple identifier (letters, digits, underscore; max 64)")
		}
	}
	return nil
}

// QuoteIdentifier wraps a MySQL identifier in backticks and doubles internal backticks.
func QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func isIdentStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}
