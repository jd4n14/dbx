package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeData(t *testing.T) {
	raw, err := NormalizeData([]byte(`  [{"id": 1}]  `))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"id":1}]` {
		t.Fatalf("got %s", raw)
	}

	_, err = NormalizeData([]byte(``))
	if err == nil {
		t.Fatal("expected empty error")
	}
	_, err = NormalizeData([]byte(`{`))
	if err == nil {
		t.Fatal("expected invalid JSON")
	}

	// object ok
	raw, err = NormalizeData([]byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":1}` {
		t.Fatalf("got %s", raw)
	}
}

func TestNormalizeData_PreservesLargeIntegerLexically(t *testing.T) {
	for _, input := range []string{
		`{"id": 9007199254740993}`,
		`[{"id": 9007199254740993}]`,
	} {
		raw, err := NormalizeData([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `9007199254740993`) {
			t.Fatalf("large integer changed: %s", raw)
		}
	}
}

func TestEncodeSnapshot(t *testing.T) {
	at := time.Date(2026, 7, 8, 17, 20, 31, 0, time.UTC)
	data, _ := NormalizeData([]byte(`[{"id":1}]`))
	s := NewSnapshot("before_split_order", data, "local_wms", "select 1", at)
	b, err := EncodeSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Fatal("want trailing newline")
	}
	var got Snapshot
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeSnapshot || got.Name != "before_split_order" || got.Connection != "local_wms" {
		t.Fatalf("got %+v", got)
	}
	if !got.CreatedAt.Equal(at) {
		t.Fatalf("created_at %v", got.CreatedAt)
	}
}

func TestEncodeLastResult(t *testing.T) {
	data, _ := NormalizeData([]byte(`[]`))
	r := NewLastResult(data, "local", "select 1", time.Time{})
	b, err := EncodeLastResult(r)
	if err != nil {
		t.Fatal(err)
	}
	var got LastResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeLastResult {
		t.Fatalf("type %q", got.Type)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at should be set")
	}
}
