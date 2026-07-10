package snapshot

import "testing"

func TestValidateName(t *testing.T) {
	ok := []string{
		"before_split_order",
		"after_packing",
		"_tmp",
		"A",
		"order-123",
		"a" + string(make([]byte, 63)), // 64 chars: a + 63 zeros won't work for name
	}
	// Fix last case: 64 letter/digit name
	ok[len(ok)-1] = "a" + repeat("b", 63)

	for _, name := range ok {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}

	bad := []string{
		"",
		"   ",
		"1starts_digit",
		"-dash",
		"has.dot",
		"has/slash",
		"has space",
		"ü",
		repeat("x", 65),
		"..",
		".",
	}
	for _, name := range bad {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", name)
		}
	}
}

func TestValidateName_Trims(t *testing.T) {
	if err := ValidateName("  before_split  "); err != nil {
		t.Fatalf("trim: %v", err)
	}
}

func repeat(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}
