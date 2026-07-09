package ddl

import "encoding/json"

// Result is the --json envelope for dbx ddl.
type Result struct {
	Type       string `json:"type"`
	Connection string `json:"connection"`
	Dialect    string `json:"dialect"`
	Table      string `json:"table"`
	DDL        string `json:"ddl"`
}

// EncodeJSON pretty-prints Result (2-space indent + trailing newline).
func EncodeJSON(r Result) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
