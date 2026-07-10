// Package snapshot stores named JSON result envelopes under .dbx/snapshots/.
package snapshot

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxNameLen is the maximum length of a snapshot name.
const MaxNameLen = 64

// ValidateName checks a human snapshot name safe for use as a filename.
// Leading/trailing space is trimmed. Allowed: letters, digits, underscore, hyphen;
// must start with a letter or underscore; max 64 chars.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("invalid snapshot name: must be letters, digits, underscore, or hyphen (max 64); start with letter or underscore")
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("invalid snapshot name: must be letters, digits, underscore, or hyphen (max 64); start with letter or underscore")
	}
	for i, r := range name {
		if r > unicode.MaxASCII {
			return fmt.Errorf("invalid snapshot name: must be letters, digits, underscore, or hyphen (max 64); start with letter or underscore")
		}
		c := byte(r)
		if i == 0 {
			if !isNameStart(c) {
				return fmt.Errorf("invalid snapshot name: must be letters, digits, underscore, or hyphen (max 64); start with letter or underscore")
			}
			continue
		}
		if !isNamePart(c) {
			return fmt.Errorf("invalid snapshot name: must be letters, digits, underscore, or hyphen (max 64); start with letter or underscore")
		}
	}
	return nil
}

func isNameStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func isNamePart(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9' || c == '-'
}
