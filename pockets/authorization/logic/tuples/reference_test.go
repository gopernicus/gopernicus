package tuples_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestValidateRefField(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{"opaque", "User:abc#relation", true},
		{"whitespace preserved", "  role  ", true},
		{"space only", " ", true},
		{"unicode", "é東京", true},
		{"combining mark", "e\u0301", true},
		{"format character", "a\u200db", true},
		{"maximum bytes", strings.Repeat("a", tuples.MaxRefFieldLen), true},
		{"maximum multibyte", strings.Repeat("é", tuples.MaxRefFieldLen/2), true},
		{"empty", "", false},
		{"over maximum bytes", strings.Repeat("a", tuples.MaxRefFieldLen+1), false},
		{"multibyte exceeds bytes", strings.Repeat("é", tuples.MaxRefFieldLen/2) + "a", false},
		{"nul", "a\x00b", false},
		{"newline", "a\nb", false},
		{"tab", "a\tb", false},
		{"delete", "a\x7fb", false},
		{"unicode control", "a\u0085b", false},
		{"invalid utf8", "a\xff", false},
		{"truncated utf8", "\xc3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tuples.ValidateRefField("test field", tt.value)
			if tt.valid {
				if err != nil {
					t.Fatalf("ValidateRefField(%q): %v", tt.value, err)
				}
				return
			}
			requireInvalidRef(t, err)
			if !strings.Contains(err.Error(), "test field") {
				t.Fatalf("error does not name the invalid field: %v", err)
			}
		})
	}
}

func requireInvalidRef(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, tuples.ErrInvalidRef) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidRef wrapping sdk.ErrInvalidInput, got %v", err)
	}
}
