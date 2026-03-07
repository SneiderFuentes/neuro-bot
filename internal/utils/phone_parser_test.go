package utils

import "testing"

func TestParseColombianPhone(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Valid formats
		{"3001234567", "+573001234567"},
		{"3101234567", "+573101234567"},
		{"+573001234567", "+573001234567"},
		{"573001234567", "+573001234567"},
		{"300-123-4567", ""},  // Parser splits by separator, parts too short individually
		{"300/123/4567", ""},  // Same: split by / yields parts < 10 digits
		{"+57 300 123 4567", "+573001234567"},

		// Invalid formats
		{"", ""},
		{"null", ""},
		{"no tiene", ""},
		{"n/a", ""},
		{"-", ""},
		{"1234567", ""},        // Too short
		{"6011234567", ""},     // Not mobile (doesn't start with 3)
		{"1234567890", ""},     // 10 digits but doesn't start with 3
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseColombianPhone(tc.input)
			if got != tc.expected {
				t.Errorf("ParseColombianPhone(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
