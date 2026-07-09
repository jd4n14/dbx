// Package jsonutil converts columnar query results into JSON-friendly values.
//
// Type-mapping rules (Phase 2 / plan §4 defaults):
//
//	nil              → null
//	bool             → boolean
//	integer types    → number (int64 / uint64 as appropriate)
//	float32/float64  → number (encoding/json defaults)
//	string           → string, then auto-JSON (object/array only)
//	[]byte           → see normalize order below
//	time.Time        → RFC3339 in UTC; RFC3339Nano when fractional seconds present
//	json.RawMessage  → re-normalized via Unmarshal (nested object/array/primitive)
//	map/slice        → passed through as nested JSON structures
//
// []byte normalize order:
//  1. If valid UTF-8 → treat as string (continue to auto-JSON).
//  2. Else → base64-encoded string (stop; no auto-JSON).
//  3. On the string path → auto-JSON only when the full trimmed value is a
//     JSON object or array. Bare numbers (e.g. DECIMAL "12.34"), booleans,
//     and quoted JSON strings remain JSON strings (precision-safe for DECIMAL).
//  4. Empty string stays "" (not null).
package jsonutil

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// NormalizeValue converts a single scanned cell into a JSON-friendly Go value.
// The result is suitable for encoding/json (nil → null, etc.).
func NormalizeValue(v any) any {
	if v == nil {
		return nil
	}

	switch x := v.(type) {
	case bool:
		return x
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint:
		return uint64(x)
	case uint8:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint32:
		return uint64(x)
	case uint64:
		return x
	case float32:
		return float64(x)
	case float64:
		return x
	case string:
		return normalizeString(x)
	case []byte:
		return normalizeBytes(x)
	case json.RawMessage:
		return normalizeRawMessage(x)
	case time.Time:
		return formatTime(x)
	case map[string]any:
		return x
	case []any:
		return x
	}

	// Pointers: dereference once if non-nil.
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return NormalizeValue(rv.Elem().Interface())
	}

	// Fallback: leave as-is for encoding/json (or future typed edges).
	return v
}

// normalizeBytes implements the documented []byte path.
func normalizeBytes(b []byte) any {
	if len(b) == 0 {
		// Empty blob → empty string (not null); no auto-JSON on empty.
		return ""
	}
	if utf8.Valid(b) {
		return normalizeString(string(b))
	}
	// Non-UTF-8 binary → plain base64 string; no auto-JSON.
	return base64.StdEncoding.EncodeToString(b)
}

func normalizeString(s string) any {
	if parsed, ok := tryAutoJSON(s); ok {
		return parsed
	}
	return s
}

func normalizeRawMessage(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Invalid raw → fall back to string form of the bytes.
		return normalizeBytes([]byte(raw))
	}
	return v
}

// tryAutoJSON parses s as JSON only when the full value is an object or array.
// Primitives (true, 123, "x") and invalid JSON leave the original string.
func tryAutoJSON(s string) (any, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, false
	}
	// Fast reject: only objects/arrays are candidates.
	first := trimmed[0]
	if first != '{' && first != '[' {
		return nil, false
	}

	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return v, true
	default:
		// Should not happen given leading { / [, but be strict.
		return nil, false
	}
}

func formatTime(t time.Time) string {
	u := t.UTC()
	if u.Nanosecond() != 0 {
		return u.Format(time.RFC3339Nano)
	}
	return u.Format(time.RFC3339)
}
