// Package diff compares JSON values structurally for dbx snapshots.
package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Kind describes how a JSON value changed at Path.
type Kind string

const (
	KindAdded   Kind = "added"
	KindRemoved Kind = "removed"
	KindChanged Kind = "changed"
)

// Change is one structural difference between two JSON values. Before and
// After contain JSON values, not encoded JSON strings. The missing side of an
// addition or removal is nil and is omitted from JSON output.
type Change struct {
	Path   string          `json:"path"`
	Kind   Kind            `json:"kind"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

// Compare returns deterministic structural changes between before and after.
// Objects are compared by sorted key union and arrays positionally. Numbers
// are decoded as json.Number so their original lexical representation is not
// rounded through float64.
func Compare(before, after json.RawMessage) ([]Change, error) {
	b, err := decode(before)
	if err != nil {
		return nil, fmt.Errorf("decode before JSON: %w", err)
	}
	a, err := decode(after)
	if err != nil {
		return nil, fmt.Errorf("decode after JSON: %w", err)
	}

	var changes []Change
	compareValue(&changes, "$", b, a)
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func decode(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func compareValue(changes *[]Change, path string, before, after any) {
	beforeObject, beforeIsObject := before.(map[string]any)
	afterObject, afterIsObject := after.(map[string]any)
	if beforeIsObject || afterIsObject {
		if !beforeIsObject || !afterIsObject {
			appendChanged(changes, path, before, after)
			return
		}
		compareObjects(changes, path, beforeObject, afterObject)
		return
	}

	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeIsArray || afterIsArray {
		if !beforeIsArray || !afterIsArray {
			appendChanged(changes, path, before, after)
			return
		}
		compareArrays(changes, path, beforeArray, afterArray)
		return
	}

	if !scalarEqual(before, after) {
		appendChanged(changes, path, before, after)
	}
}

func compareObjects(changes *[]Change, path string, before, after map[string]any) {
	keys := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		b, inBefore := before[key]
		a, inAfter := after[key]
		childPath := appendKey(path, key)
		switch {
		case !inBefore:
			appendAdded(changes, childPath, a)
		case !inAfter:
			appendRemoved(changes, childPath, b)
		default:
			compareValue(changes, childPath, b, a)
		}
	}
}

func compareArrays(changes *[]Change, path string, before, after []any) {
	max := len(before)
	if len(after) > max {
		max = len(after)
	}
	for i := 0; i < max; i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(before):
			appendAdded(changes, childPath, after[i])
		case i >= len(after):
			appendRemoved(changes, childPath, before[i])
		default:
			compareValue(changes, childPath, before[i], after[i])
		}
	}
}

func appendAdded(changes *[]Change, path string, after any) {
	*changes = append(*changes, Change{Path: path, Kind: KindAdded, After: encodeValue(after)})
}

func appendRemoved(changes *[]Change, path string, before any) {
	*changes = append(*changes, Change{Path: path, Kind: KindRemoved, Before: encodeValue(before)})
}

func appendChanged(changes *[]Change, path string, before, after any) {
	*changes = append(*changes, Change{
		Path:   path,
		Kind:   KindChanged,
		Before: encodeValue(before),
		After:  encodeValue(after),
	})
}

func encodeValue(value any) json.RawMessage {
	// Values originate from valid JSON decoded with UseNumber, so Marshal cannot
	// fail. Keep this helper small so recursive comparison remains error-free.
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal decoded JSON value: %v", err))
	}
	return raw
}

func scalarEqual(before, after any) bool {
	return before == after
}

func appendKey(path, key string) string {
	if isSafeKey(key) {
		return path + "." + key
	}
	escaped := strings.ReplaceAll(key, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return path + "['" + escaped + "']"
}

func isSafeKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if i == 0 {
			if !(c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')) {
				return false
			}
			continue
		}
		if !(c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')) {
			return false
		}
	}
	return true
}
