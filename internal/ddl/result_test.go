package ddl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeJSON_Envelope(t *testing.T) {
	t.Parallel()
	raw, err := EncodeJSON(Result{
		Type:       "ddl",
		Connection: "docker_mysql",
		Dialect:    "mysql",
		Table:      "orders",
		DDL:        "CREATE TABLE `orders` (`id` int)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw[len(raw)-1] != '\n' {
		t.Fatal("expected trailing newline")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "ddl" || m["connection"] != "docker_mysql" || m["dialect"] != "mysql" || m["table"] != "orders" {
		t.Fatalf("envelope fields: %#v", m)
	}
	if m["ddl"] != "CREATE TABLE `orders` (`id` int)" {
		t.Fatalf("ddl field: %#v", m["ddl"])
	}
	if !strings.Contains(string(raw), "\n  ") {
		t.Fatalf("expected pretty indent: %s", raw)
	}
}
