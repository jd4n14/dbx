package jsonutil

import "encoding/json"

// RowsToObjects converts columnar row data into a slice of row objects.
//
// Column names are used as map keys as-is. Duplicate column names use
// last-wins semantics (limitation for queries like SELECT 1, 1).
// Empty rows yields a non-nil empty slice (marshals to []).
func RowsToObjects(columns []string, rows [][]any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		obj := make(map[string]any, len(columns))
		for i, col := range columns {
			var cell any
			if i < len(row) {
				cell = row[i]
			}
			// last-wins for duplicate column names
			obj[col] = NormalizeValue(cell)
		}
		out = append(out, obj)
	}
	return out, nil
}

// MarshalPretty encodes v as pretty-printed JSON with 2-space indent
// and a trailing newline (stdout contract for dbx query success).
func MarshalPretty(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// RowsToPrettyJSON converts columns+rows to a pretty JSON array of objects.
// Convenience for callers that do not need the intermediate objects.
func RowsToPrettyJSON(columns []string, rows [][]any) ([]byte, error) {
	objs, err := RowsToObjects(columns, rows)
	if err != nil {
		return nil, err
	}
	return MarshalPretty(objs)
}
