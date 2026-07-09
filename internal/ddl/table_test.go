package ddl

import (
	"strings"
	"testing"
)

func TestValidateTableName_OK(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"orders", "Orders", "t", "a1", "_x", "order_items"} {
		if err := ValidateTableName(name); err != nil {
			t.Errorf("ValidateTableName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateTableName_TrimSpace(t *testing.T) {
	t.Parallel()
	if err := ValidateTableName("  orders  "); err != nil {
		t.Fatalf("trimmed valid name: %v", err)
	}
}

func TestValidateTableName_Reject(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"   ",
		"a.b",
		"orders;drop",
		"1orders",
		"order-items",
		"orders`",
		"orders table",
		strings.Repeat("a", 65),
	}
	for _, name := range cases {
		if err := ValidateTableName(name); err == nil {
			t.Errorf("ValidateTableName(%q) = nil, want error", name)
		}
	}
}

func TestQuoteIdentifier(t *testing.T) {
	t.Parallel()
	if got := QuoteIdentifier("orders"); got != "`orders`" {
		t.Fatalf("got %q", got)
	}
	if got := QuoteIdentifier("a`b"); got != "`a``b`" {
		t.Fatalf("escaped backtick: got %q", got)
	}
}
