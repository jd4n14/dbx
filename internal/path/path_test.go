package path

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestEvaluateAllowedSelectors(t *testing.T) {
	data := []byte(`[
		{"metadata":{"fulfillment":{"status":"created"}},"items":[{"id":9007199254740993},{"id":2}]},
		{"metadata":{"fulfillment":{"status":"pending"}},"items":[{"id":3}]}
	]`)

	cases := []struct {
		selector string
		want     string
	}{
		{"metadata.fulfillment.status", `["created","pending"]`},
		{"items[0]", `[{"id":9007199254740993},{"id":3}]`},
		{"items[*].id", `[9007199254740993,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.selector, func(t *testing.T) {
			matches, err := Evaluate(data, tc.selector)
			if err != nil {
				t.Fatal(err)
			}
			got, err := MarshalPretty(matches)
			if err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(got, []byte(tc.want)) {
				t.Fatalf("result = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEvaluateImplicitRootMapping(t *testing.T) {
	matches, err := Evaluate([]byte(`[{"metadata":{"status":"x"}}]`), "metadata.status")
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalPretty(matches)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, []byte(`["x"]`)) {
		t.Fatalf("result = %s", got)
	}
}

func TestEvaluateBranchFailureProducesNoMatch(t *testing.T) {
	matches, err := Evaluate([]byte(`[
		{"items":[{"id":1},{}]},
		{"items":"wrong"},
		{"items":[{"id":2}]}
	]`), "items[*].id")
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalPretty(matches)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, []byte(`[1,2]`)) {
		t.Fatalf("result = %s", got)
	}
}

func TestEvaluateNoMatch(t *testing.T) {
	matches, err := Evaluate([]byte(`[{"items":[]}]`), "items[0]")
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalPretty(matches)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]\n" {
		t.Fatalf("result = %q, want []", got)
	}
}

func TestEvaluatePreservesLargeInteger(t *testing.T) {
	matches, err := Evaluate([]byte(`[{"id":9007199254740993}]`), "id")
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalPretty(matches)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("9007199254740993")) {
		t.Fatalf("large integer changed: %s", got)
	}
}

func TestParseRejectsUnsupportedSyntax(t *testing.T) {
	cases := []string{
		"", ".metadata", "metadata.", "metadata..status", "items[-1]",
		`items["id"]`, "$", "$.metadata", "items[?(@.id)]", "items[(1)]",
		"items[]", "items[0", "items[0]id", "metadata status",
	}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatalf("Parse(%q) succeeded", input)
			}
		})
	}
}

func jsonEqual(a, b []byte) bool {
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &right); err != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
