// Package path evaluates the deliberately small selector language used by dbx.
//
// It is not a JSONPath implementation. Selectors consist of object fields,
// non-negative array indexes, and array wildcards, for example:
//
//	metadata.fulfillment.status
//	items[0]
//	items[*].id
package path

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

type stepKind uint8

const (
	fieldStep stepKind = iota
	indexStep
	wildcardStep
)

type step struct {
	kind  stepKind
	field string
	index int
}

// Selector is a parsed bounded dbx selector.
type Selector struct {
	steps []step
}

// Parse accepts only dotted object fields, array indexes, and array wildcards.
// It intentionally rejects JSONPath roots, filters, scripts, quoted keys, and
// every other syntax outside that small contract.
func Parse(input string) (Selector, error) {
	if input == "" {
		return Selector{}, fmt.Errorf("invalid path: selector is empty")
	}

	var steps []step
	for i := 0; i < len(input); {
		start := i
		for i < len(input) && input[i] != '.' && input[i] != '[' {
			i++
		}
		field := input[start:i]
		if !validField(field) {
			return Selector{}, fmt.Errorf("invalid path near %q: expected field name", input[start:])
		}
		steps = append(steps, step{kind: fieldStep, field: field})

		for i < len(input) && input[i] == '[' {
			end := i + 1
			for end < len(input) && input[end] != ']' {
				end++
			}
			if end == len(input) {
				return Selector{}, fmt.Errorf("invalid path near %q: unclosed bracket", input[i:])
			}
			content := input[i+1 : end]
			switch {
			case content == "*":
				steps = append(steps, step{kind: wildcardStep})
			case validIndex(content):
				index, err := strconv.Atoi(content)
				if err != nil {
					return Selector{}, fmt.Errorf("invalid path near %q: array index is too large", input[i:end+1])
				}
				steps = append(steps, step{kind: indexStep, index: index})
			default:
				return Selector{}, fmt.Errorf("invalid path near %q: expected array index or *", input[i:end+1])
			}
			i = end + 1
		}

		if i == len(input) {
			break
		}
		if input[i] != '.' {
			return Selector{}, fmt.Errorf("invalid path near %q", input[i:])
		}
		i++
		if i == len(input) {
			return Selector{}, fmt.Errorf("invalid path: trailing dot")
		}
	}

	return Selector{steps: steps}, nil
}

func validField(field string) bool {
	if field == "" || !isFieldStart(field[0]) {
		return false
	}
	for i := 1; i < len(field); i++ {
		if !isFieldPart(field[i]) {
			return false
		}
	}
	return true
}

func isFieldStart(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func isFieldPart(c byte) bool {
	return isFieldStart(c) || c >= '0' && c <= '9' || c == '-'
}

func validIndex(index string) bool {
	if index == "" {
		return false
	}
	for i := 0; i < len(index); i++ {
		if index[i] < '0' || index[i] > '9' {
			return false
		}
	}
	return true
}

// Evaluate applies selector to data and returns one raw JSON value per match.
// Numeric tokens use json.Number, so identifiers larger than float64's exact
// range are not rounded while they travel through the evaluator.
func Evaluate(data []byte, selector string) ([]json.RawMessage, error) {
	parsed, err := Parse(selector)
	if err != nil {
		return nil, err
	}
	return parsed.Evaluate(data)
}

// Evaluate applies a parsed Selector to JSON data. When the root is an array,
// its entries are the starting branches; this is the documented implicit row
// mapping used for query result arrays.
func (s Selector) Evaluate(data []byte) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode JSON data: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode JSON data: multiple values")
		}
		return nil, fmt.Errorf("decode JSON data: %w", err)
	}

	values := []any{root}
	if rows, ok := root.([]any); ok {
		values = rows
	}
	for _, st := range s.steps {
		values = applyStep(values, st)
		if len(values) == 0 {
			break
		}
	}

	matches := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode path match: %w", err)
		}
		matches = append(matches, json.RawMessage(raw))
	}
	return matches, nil
}

func applyStep(values []any, st step) []any {
	next := make([]any, 0, len(values))
	for _, value := range values {
		switch st.kind {
		case fieldStep:
			if object, ok := value.(map[string]any); ok {
				if child, exists := object[st.field]; exists {
					next = append(next, child)
				}
			}
		case indexStep:
			if array, ok := value.([]any); ok && st.index < len(array) {
				next = append(next, array[st.index])
			}
		case wildcardStep:
			if array, ok := value.([]any); ok {
				next = append(next, array...)
			}
		}
	}
	return next
}

// MarshalPretty returns the stable JSON-array representation for matches.
func MarshalPretty(matches []json.RawMessage) ([]byte, error) {
	result, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode path matches: %w", err)
	}
	return append(result, '\n'), nil
}
