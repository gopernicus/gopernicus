package turso

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// TestQuoteIdentifier_Valid: allowed identifiers quote to double-quoted form,
// dotted names quote per segment.
func TestQuoteIdentifier_Valid(t *testing.T) {
	cases := map[string]string{
		"created_at":   `"created_at"`,
		"id":           `"id"`,
		"role_key":     `"role_key"`,
		"Name2":        `"Name2"`,
		"main.widgets": `"main"."widgets"`,
	}
	for in, want := range cases {
		got, err := QuoteIdentifier(in)
		if err != nil {
			t.Fatalf("QuoteIdentifier(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestQuoteIdentifier_Rejects: injection attempts, raw expressions, and
// malformed identifiers are rejected with an ErrInvalidInput-wrapping error —
// nothing quotes through. The `a || char(0) || b` case is the exact
// raw-expression tiebreak the roles store used to pass as a PK.
func TestQuoteIdentifier_Rejects(t *testing.T) {
	bad := []string{
		"created_at; DROP TABLE widgets",
		`name" OR "1"="1`,
		"col')",
		"a || char(0) || b",
		"1col",
		"",
		"a b",
		"col\\x",
		"main..widgets",
	}
	for _, in := range bad {
		got, err := QuoteIdentifier(in)
		if err == nil {
			t.Errorf("QuoteIdentifier(%q) = %q, want error", in, got)
			continue
		}
		if !errors.Is(err, errs.ErrInvalidInput) {
			t.Errorf("QuoteIdentifier(%q) err = %v, want ErrInvalidInput", in, err)
		}
	}
}
