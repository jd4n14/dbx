package snapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Type markers for on-disk envelopes.
const (
	TypeSnapshot   = "snapshot"
	TypeLastResult = "last_result"
)

// Snapshot is the on-disk envelope for a named snapshot.
type Snapshot struct {
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	CreatedAt  time.Time       `json:"created_at"`
	Connection string          `json:"connection,omitempty"`
	SQL        string          `json:"sql,omitempty"`
	Data       json.RawMessage `json:"data"`
}

// LastResult is the cache written by dbx query for subsequent snapshot saves.
type LastResult struct {
	Type       string          `json:"type"`
	CreatedAt  time.Time       `json:"created_at"`
	Connection string          `json:"connection,omitempty"`
	SQL        string          `json:"sql,omitempty"`
	Data       json.RawMessage `json:"data"`
}

// EncodeSnapshot pretty-prints a Snapshot (2-space indent + trailing newline).
func EncodeSnapshot(s Snapshot) ([]byte, error) {
	if s.Type == "" {
		s.Type = TypeSnapshot
	}
	return marshalPretty(s)
}

// EncodeLastResult pretty-prints a LastResult (2-space indent + trailing newline).
func EncodeLastResult(r LastResult) ([]byte, error) {
	if r.Type == "" {
		r.Type = TypeLastResult
	}
	return marshalPretty(r)
}

// NormalizeData validates that raw is valid JSON and returns compact form
// suitable for embedding in an envelope's data field.
//
// It deliberately operates on raw JSON bytes rather than decoding into Go
// values. Decoding arbitrary JSON through interface{} turns JSON numbers into
// float64 and can silently corrupt large database identifiers.
// Accepts any JSON value (array, object, etc.).
func NormalizeData(raw []byte) (json.RawMessage, error) {
	raw = trimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("invalid JSON: empty input")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, fmt.Errorf("compact JSON: %w", err)
	}
	return json.RawMessage(b.Bytes()), nil
}

// NewSnapshot builds a snapshot envelope with UTC created_at.
func NewSnapshot(name string, data json.RawMessage, connection, sql string, at time.Time) Snapshot {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	return Snapshot{
		Type:       TypeSnapshot,
		Name:       name,
		CreatedAt:  at,
		Connection: connection,
		SQL:        sql,
		Data:       data,
	}
}

// NewLastResult builds a last_result envelope with UTC created_at.
func NewLastResult(data json.RawMessage, connection, sql string, at time.Time) LastResult {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	return LastResult{
		Type:       TypeLastResult,
		CreatedAt:  at,
		Connection: connection,
		SQL:        sql,
		Data:       data,
	}
}

func marshalPretty(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func trimSpace(b []byte) []byte {
	// Avoid importing strings for a tiny helper; use bytes semantics.
	i, j := 0, len(b)
	for i < j && isSpace(b[i]) {
		i++
	}
	for j > i && isSpace(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
