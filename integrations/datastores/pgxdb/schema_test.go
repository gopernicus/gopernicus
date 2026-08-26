package pgxdb

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestSchema pins the validated schema value type: which names NewSchema
// accepts, that every rejection wraps sdk.ErrInvalidInput, that the zero
// Schema renders bare names byte-for-byte, and that quoting preserves case.
func TestSchema(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		for _, name := range []string{
			"auth",
			"public",
			"a",
			"Auth",
			"gps_360",
			"s1",
			"tenant_0001",
			strings.Repeat("a", 63), // exactly the Postgres identifier limit
		} {
			s, err := NewSchema(name)
			if err != nil {
				t.Errorf("NewSchema(%q) = %v, want no error", name, err)
				continue
			}
			if s.IsZero() {
				t.Errorf("NewSchema(%q).IsZero() = true, want false", name)
			}
			if s.String() != name {
				t.Errorf("NewSchema(%q).String() = %q, want %q", name, s.String(), name)
			}
		}
	})

	t.Run("invalid", func(t *testing.T) {
		cases := []struct {
			label string
			name  string
		}{
			{"empty", ""},
			{"64 bytes", strings.Repeat("a", 64)},
			{"leading digit", "1auth"},
			{"leading underscore", "_auth"},
			{"dotted", "auth.users"},
			{"dash", "auth-schema"},
			{"space", "auth schema"},
			{"quote", `au"th`},
			{"semicolon", "auth;drop"},
			{"paren", "auth()"},
			{"backslash", `auth\x`},
			{"unicode", "authé"},
			{"reserved pg_ prefix", "pg_temp"},
			{"reserved pg_ prefix uppercase", "PG_CATALOG"},
			{"reserved pg_ prefix mixed", "Pg_Toast"},
			{"reserved pg_ exact", "pg_"},
			{"information_schema", "information_schema"},
			{"information_schema uppercase", "INFORMATION_SCHEMA"},
			{"information_schema mixed", "Information_Schema"},
		}
		for _, tc := range cases {
			s, err := NewSchema(tc.name)
			if err == nil {
				t.Errorf("%s: NewSchema(%q) = %v, want error", tc.label, tc.name, s)
				continue
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("%s: NewSchema(%q) err = %v, want wrapping sdk.ErrInvalidInput", tc.label, tc.name, err)
			}
			if !s.IsZero() {
				t.Errorf("%s: NewSchema(%q) returned a non-zero Schema on failure", tc.label, tc.name)
			}
		}
	})

	t.Run("zero renders bare", func(t *testing.T) {
		var zero Schema
		if !zero.IsZero() {
			t.Fatal("zero Schema IsZero() = false, want true")
		}
		if zero.String() != "" {
			t.Errorf("zero Schema String() = %q, want empty", zero.String())
		}
		for _, table := range []string{"users", "schema_migrations", "entry_terms"} {
			if got := zero.Table(table); got != table {
				t.Errorf("zero Schema Table(%q) = %q, want %q byte-for-byte", table, got, table)
			}
		}
	})

	t.Run("qualified rendering", func(t *testing.T) {
		s, err := NewSchema("auth")
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}
		if got, want := s.Table("users"), `"auth".users`; got != want {
			t.Errorf("Table(users) = %q, want %q", got, want)
		}
		if got, want := s.Table("schema_migrations"), `"auth".schema_migrations`; got != want {
			t.Errorf("Table(schema_migrations) = %q, want %q", got, want)
		}
	})

	t.Run("case preserved", func(t *testing.T) {
		upper, err := NewSchema("Auth")
		if err != nil {
			t.Fatalf("NewSchema(Auth): %v", err)
		}
		lower, err := NewSchema("auth")
		if err != nil {
			t.Fatalf("NewSchema(auth): %v", err)
		}
		if upper.Table("users") == lower.Table("users") {
			t.Error(`"Auth" and "auth" render identically; quoting must preserve case`)
		}
		if got, want := upper.Table("users"), `"Auth".users`; got != want {
			t.Errorf("Table = %q, want %q", got, want)
		}
	})
}
