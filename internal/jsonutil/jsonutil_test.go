package jsonutil

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeValue_NullAndScalars(t *testing.T) {
	t.Parallel()

	if got := NormalizeValue(nil); got != nil {
		t.Fatalf("nil: got %#v, want nil", got)
	}
	if got := NormalizeValue(true); got != true {
		t.Fatalf("bool: got %#v", got)
	}
	if got := NormalizeValue(int64(42)); got != int64(42) {
		t.Fatalf("int64: got %#v", got)
	}
	if got := NormalizeValue(int(7)); got != int64(7) {
		t.Fatalf("int→int64: got %#v", got)
	}
	if got := NormalizeValue(float64(1.5)); got != float64(1.5) {
		t.Fatalf("float64: got %#v", got)
	}
	if got := NormalizeValue("hello"); got != "hello" {
		t.Fatalf("plain string: got %#v", got)
	}
	if got := NormalizeValue(""); got != "" {
		t.Fatalf("empty string: got %#v, want empty string", got)
	}
}

func TestNormalizeValue_TimeUTC(t *testing.T) {
	t.Parallel()

	// Whole-second UTC → RFC3339 with Z.
	ts := time.Date(2026, 7, 8, 17, 20, 31, 0, time.UTC)
	got, ok := NormalizeValue(ts).(string)
	if !ok {
		t.Fatalf("time.Time type: got %T", NormalizeValue(ts))
	}
	want := "2026-07-08T17:20:31Z"
	if got != want {
		t.Fatalf("time whole second: got %q, want %q", got, want)
	}

	// Non-UTC input is converted to UTC (17:20:31-07:00 → 00:20:31Z next day).
	tsLocal := time.Date(2026, 7, 8, 17, 20, 31, 0, time.FixedZone("PDT", -7*3600))
	gotLocal, ok := NormalizeValue(tsLocal).(string)
	if !ok {
		t.Fatalf("time local type: got %T", NormalizeValue(tsLocal))
	}
	if gotLocal != "2026-07-09T00:20:31Z" {
		t.Fatalf("time local→UTC: got %q", gotLocal)
	}

	// Fractional → RFC3339Nano.
	tsNano := time.Date(2026, 7, 8, 17, 20, 31, 123456789, time.UTC)
	gotNano, ok := NormalizeValue(tsNano).(string)
	if !ok {
		t.Fatalf("time nano type: got %T", NormalizeValue(tsNano))
	}
	if !strings.HasPrefix(gotNano, "2026-07-08T17:20:31.") || !strings.HasSuffix(gotNano, "Z") {
		t.Fatalf("time nano: got %q", gotNano)
	}
}

func TestNormalizeValue_DecimalAsBytesRemainsString(t *testing.T) {
	t.Parallel()

	// MySQL DECIMAL commonly arrives as []byte of numeric text.
	got := NormalizeValue([]byte("12.34"))
	s, ok := got.(string)
	if !ok {
		t.Fatalf("DECIMAL []byte: got %T %#v, want string", got, got)
	}
	if s != "12.34" {
		t.Fatalf("DECIMAL value: got %q, want %q", s, "12.34")
	}

	// Ensure it is not a JSON number when marshaled inside a row.
	cols := []string{"amount"}
	rows := [][]any{{[]byte("12.34")}}
	b, err := RowsToPrettyJSON(cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"amount": "12.34"`) {
		t.Fatalf("DECIMAL must be JSON string, got:\n%s", b)
	}
	if strings.Contains(string(b), `"amount": 12.34`) {
		t.Fatalf("DECIMAL must not be JSON number, got:\n%s", b)
	}
}

func TestNormalizeValue_AutoJSONObjectAndArray(t *testing.T) {
	t.Parallel()

	obj := NormalizeValue(`{"a":1}`)
	m, ok := obj.(map[string]any)
	if !ok {
		t.Fatalf("object: got %T %#v", obj, obj)
	}
	// json.Unmarshal numbers → float64
	if m["a"] != float64(1) {
		t.Fatalf("object.a: got %#v", m["a"])
	}

	arr := NormalizeValue(`[1,2]`)
	a, ok := arr.([]any)
	if !ok {
		t.Fatalf("array: got %T %#v", arr, arr)
	}
	if len(a) != 2 || a[0] != float64(1) || a[1] != float64(2) {
		t.Fatalf("array contents: %#v", a)
	}

	// UTF-8 []byte path also auto-parses.
	fromBytes := NormalizeValue([]byte(`{"x":true}`))
	mb, ok := fromBytes.(map[string]any)
	if !ok || mb["x"] != true {
		t.Fatalf("[]byte object: got %#v", fromBytes)
	}

	// Leading/trailing space allowed.
	spaced := NormalizeValue("  [1]  ")
	if _, ok := spaced.([]any); !ok {
		t.Fatalf("trimmed array: got %#v", spaced)
	}
}

func TestNormalizeValue_AutoJSONRejectsPrimitives(t *testing.T) {
	t.Parallel()

	cases := []string{
		"true",
		"false",
		"null",
		"123",
		"12.34",
		`"quoted"`,
		"hello {",
		"{not json",
		"[unterminated",
	}
	for _, s := range cases {
		got := NormalizeValue(s)
		if got != s {
			t.Errorf("auto-JSON should leave %q as string, got %#v", s, got)
		}
	}
}

func TestNormalizeValue_BinaryNonUTF8Base64(t *testing.T) {
	t.Parallel()

	// Invalid UTF-8 sequence.
	raw := []byte{0xff, 0xfe, 0xfd}
	got := NormalizeValue(raw)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("binary: got %T %#v, want base64 string", got, got)
	}
	want := base64.StdEncoding.EncodeToString(raw)
	if s != want {
		t.Fatalf("base64: got %q, want %q", s, want)
	}
	// Must not panic and must not attempt auto-JSON on binary.
}

func TestNormalizeValue_EmptyBytes(t *testing.T) {
	t.Parallel()

	got := NormalizeValue([]byte{})
	if got != "" {
		t.Fatalf("empty []byte: got %#v, want empty string", got)
	}
}

func TestNormalizeValue_RawMessage(t *testing.T) {
	t.Parallel()

	got := NormalizeValue(json.RawMessage(`{"k":"v"}`))
	m, ok := got.(map[string]any)
	if !ok || m["k"] != "v" {
		t.Fatalf("RawMessage: got %#v", got)
	}
}

func TestRowsToObjects_EmptyAndBasic(t *testing.T) {
	t.Parallel()

	objs, err := RowsToObjects([]string{"id", "name"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if objs == nil {
		t.Fatal("empty rows must yield non-nil empty slice")
	}
	if len(objs) != 0 {
		t.Fatalf("len: got %d", len(objs))
	}

	objs, err = RowsToObjects(
		[]string{"id", "name", "meta"},
		[][]any{
			{int64(1), "alice", nil},
			{int64(2), "bob", `{"role":"admin"}`},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("len: got %d", len(objs))
	}
	if objs[0]["id"] != int64(1) || objs[0]["name"] != "alice" || objs[0]["meta"] != nil {
		t.Fatalf("row0: %#v", objs[0])
	}
	meta, ok := objs[1]["meta"].(map[string]any)
	if !ok || meta["role"] != "admin" {
		t.Fatalf("row1 meta nested: %#v", objs[1]["meta"])
	}
}

func TestRowsToObjects_DuplicateColumnsLastWins(t *testing.T) {
	t.Parallel()

	objs, err := RowsToObjects(
		[]string{"c", "c"},
		[][]any{{"first", "second"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if objs[0]["c"] != "second" {
		t.Fatalf("last-wins: got %#v", objs[0]["c"])
	}
}

func TestMarshalPretty_EmptyArrayAndIndent(t *testing.T) {
	t.Parallel()

	b, err := MarshalPretty([]map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]\n" {
		t.Fatalf("empty: got %q, want %q", b, "[]\n")
	}

	// Also via RowsToPrettyJSON.
	b, err = RowsToPrettyJSON([]string{"id"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]\n" {
		t.Fatalf("RowsToPrettyJSON empty: got %q", b)
	}

	// 2-space indent + trailing newline.
	b, err = RowsToPrettyJSON(
		[]string{"id", "status"},
		[][]any{{int64(123), "pending"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"id\": 123,\n    \"status\": \"pending\"\n  }\n]\n"
	if string(b) != want {
		t.Fatalf("pretty JSON:\ngot:\n%s\nwant:\n%s", b, want)
	}
	// Must be valid JSON when trailing newline stripped for Unmarshal.
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal pretty output: %v", err)
	}
}

func TestRowsToPrettyJSON_FullMatrix(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 7, 8, 17, 20, 31, 0, time.UTC)
	cols := []string{"id", "flag", "score", "label", "amount", "meta", "tags", "created_at", "blob", "nullable"}
	rows := [][]any{{
		int64(123),
		true,
		float64(1.5),
		"hello",
		[]byte("12.34"),                 // DECIMAL
		`{"source":"shopify"}`,          // auto-JSON object
		`[1,2]`,                         // auto-JSON array
		ts,                              // time
		[]byte{0xff, 0xfe},              // binary
		nil,                             // NULL
	}}

	b, err := RowsToPrettyJSON(cols, rows)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	if len(decoded) != 1 {
		t.Fatalf("rows: %d", len(decoded))
	}
	row := decoded[0]

	if row["id"] != float64(123) { // JSON numbers → float64
		t.Errorf("id: %#v", row["id"])
	}
	if row["flag"] != true {
		t.Errorf("flag: %#v", row["flag"])
	}
	if row["score"] != float64(1.5) {
		t.Errorf("score: %#v", row["score"])
	}
	if row["label"] != "hello" {
		t.Errorf("label: %#v", row["label"])
	}
	if row["amount"] != "12.34" {
		t.Errorf("amount DECIMAL string: %#v", row["amount"])
	}
	meta, ok := row["meta"].(map[string]any)
	if !ok || meta["source"] != "shopify" {
		t.Errorf("meta nested: %#v", row["meta"])
	}
	tags, ok := row["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Errorf("tags nested: %#v", row["tags"])
	}
	if row["created_at"] != "2026-07-08T17:20:31Z" {
		t.Errorf("created_at: %#v", row["created_at"])
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe})
	if row["blob"] != wantB64 {
		t.Errorf("blob base64: %#v want %q", row["blob"], wantB64)
	}
	if row["nullable"] != nil {
		t.Errorf("nullable: %#v", row["nullable"])
	}

	// Pretty: 2-space indent, trailing newline.
	if !strings.HasSuffix(string(b), "\n") {
		t.Error("missing trailing newline")
	}
	if !strings.Contains(string(b), "\n  {\n") {
		t.Errorf("expected 2-space indent, got:\n%s", b)
	}
}

func TestNormalizeValue_PointerNilAndValue(t *testing.T) {
	t.Parallel()

	var p *string
	if got := NormalizeValue(p); got != nil {
		t.Fatalf("nil *string: got %#v", got)
	}
	s := "x"
	if got := NormalizeValue(&s); got != "x" {
		t.Fatalf("*string: got %#v", got)
	}
}
