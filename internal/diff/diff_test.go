package diff

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name   string
		before string
		after  string
		want   []Change
	}{
		{
			name:   "nested objects and unsafe keys",
			before: `{"rows":[{"status":"created","meta":{"x":1}}],"a b":true}`,
			after:  `{"rows":[{"status":"pending","meta":{"x":2}}],"a b":false}`,
			want: []Change{
				changed(`$.rows[0].meta.x`, `1`, `2`),
				changed(`$.rows[0].status`, `"created"`, `"pending"`),
				changed(`$['a b']`, `true`, `false`),
			},
		},
		{
			name:   "additions and removals",
			before: `{"removed":{"x":1},"same":null}`,
			after:  `{"added":[1,2],"same":null}`,
			want: []Change{
				added(`$.added`, `[1,2]`),
				removed(`$.removed`, `{"x":1}`),
			},
		},
		{
			name:   "arrays are positional",
			before: `["a", "b", "c"]`,
			after:  `["a", "x"]`,
			want: []Change{
				changed(`$[1]`, `"b"`, `"x"`),
				removed(`$[2]`, `"c"`),
			},
		},
		{
			name:   "changed types",
			before: `{"value":null}`,
			after:  `{"value":[]}`,
			want:   []Change{changed(`$.value`, `null`, `[]`)},
		},
		{
			name:   "object key order is irrelevant",
			before: `{"b":2,"a":{"y":true,"x":false}}`,
			after:  `{"a":{"x":false,"y":true},"b":2}`,
			want:   nil,
		},
		{
			name:   "large integer remains lexical",
			before: `{"id":9007199254740993}`,
			after:  `{"id":9007199254740994}`,
			want: []Change{
				changed(`$.id`, `9007199254740993`, `9007199254740994`),
			},
		},
		{
			name:   "unsafe key escapes backslash and quote",
			before: `{"a\\b'c":1}`,
			after:  `{"a\\b'c":2}`,
			want: []Change{
				changed(`$['a\\b\'c']`, `1`, `2`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compare(json.RawMessage(tt.before), json.RawMessage(tt.after))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("changes = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCompareRejectsInvalidJSON(t *testing.T) {
	if _, err := Compare(json.RawMessage(`{`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected invalid before JSON error")
	}
}

func TestRenderText(t *testing.T) {
	changes := []Change{
		changed(`$.rows[0].status`, `"created"`, `"pending"`),
		added(`$.rows[1]`, `{"id":2}`),
		removed(`$.removed`, `null`),
	}
	got, err := RenderText(changes)
	if err != nil {
		t.Fatal(err)
	}
	want := "$.rows[0].status\n- \"created\"\n+ \"pending\"\n\n$.rows[1]\n+ {\n  \"id\": 2\n}\n\n$.removed\n- null\n"
	if string(got) != want {
		t.Fatalf("text = %q, want %q", got, want)
	}

	got, err = RenderText(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "no differences\n" {
		t.Fatalf("equal text = %q", got)
	}
}

func TestRenderJSON(t *testing.T) {
	before := json.RawMessage(`{"rows":[{"id":1,"status":"created"}]}`)
	after := json.RawMessage(`{"rows":[{"id":1,"status":"pending"}]}`)
	changes := []Change{changed(`$.rows[0].status`, `"created"`, `"pending"`)}

	got, err := RenderJSON(before, after, changes)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"type\": \"diff\",\n  \"before\": {\n    \"rows\": [\n      {\n        \"id\": 1,\n        \"status\": \"created\"\n      }\n    ]\n  },\n  \"after\": {\n    \"rows\": [\n      {\n        \"id\": 1,\n        \"status\": \"pending\"\n      }\n    ]\n  },\n  \"changes\": [\n    {\n      \"path\": \"$.rows[0].status\",\n      \"kind\": \"changed\",\n      \"before\": \"created\",\n      \"after\": \"pending\"\n    }\n  ]\n}\n"
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("JSON = %s\nwant = %s", got, want)
	}

	var decoded struct {
		Before  map[string]any `json:"before"`
		After   map[string]any `json:"after"`
		Changes []Change       `json:"changes"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Before["rows"] == nil || decoded.After["rows"] == nil {
		t.Fatal("before and after must be JSON values, not strings")
	}
}

func changed(path, before, after string) Change {
	return Change{Path: path, Kind: KindChanged, Before: json.RawMessage(before), After: json.RawMessage(after)}
}

func added(path, after string) Change {
	return Change{Path: path, Kind: KindAdded, After: json.RawMessage(after)}
}

func removed(path, before string) Change {
	return Change{Path: path, Kind: KindRemoved, Before: json.RawMessage(before)}
}
