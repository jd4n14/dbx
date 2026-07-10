package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RenderText renders changes for human inspection. It always returns a single
// trailing newline and has no output other than the diff itself.
func RenderText(changes []Change) ([]byte, error) {
	if len(changes) == 0 {
		return []byte("no differences\n"), nil
	}

	var out bytes.Buffer
	for i, change := range changes {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(change.Path)
		out.WriteByte('\n')
		if len(change.Before) > 0 {
			value, err := pretty(change.Before)
			if err != nil {
				return nil, fmt.Errorf("format before at %s: %w", change.Path, err)
			}
			out.WriteString("- ")
			out.Write(value)
			out.WriteByte('\n')
		}
		if len(change.After) > 0 {
			value, err := pretty(change.After)
			if err != nil {
				return nil, fmt.Errorf("format after at %s: %w", change.Path, err)
			}
			out.WriteString("+ ")
			out.Write(value)
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// RenderJSON renders a machine-readable diff whose before and after fields
// remain JSON values rather than JSON-encoded strings.
func RenderJSON(before, after json.RawMessage, changes []Change) ([]byte, error) {
	envelope := struct {
		Type    string          `json:"type"`
		Before  json.RawMessage `json:"before"`
		After   json.RawMessage `json:"after"`
		Changes []Change        `json:"changes"`
	}{
		Type:    "diff",
		Before:  before,
		After:   after,
		Changes: changes,
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode diff JSON: %w", err)
	}
	return append(out, '\n'), nil
}

func pretty(raw json.RawMessage) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
