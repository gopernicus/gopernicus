package crud

import "testing"

// searchOracle is the SHARED matching oracle (crud-search-upstream T1). Every row
// is either a case a wildcard-aware implementation would get wrong, a case a
// Unicode-wide fold would get wrong, or a plain case that must keep working.
//
// The same table is re-run by the pgxdb and turso live tests against PostgreSQL's
// ILIKE and SQLite's LIKE, so a dialect that disagrees with this function fails
// there rather than silently returning different rows in production.
var searchOracle = []struct {
	name     string
	haystack string
	term     string
	want     bool
}{
	// --- plain substring behavior ---
	{"exact", "invoices", "invoices", true},
	{"prefix", "invoices", "inv", true},
	{"suffix", "invoices", "ices", true},
	{"middle", "invoices", "voic", true},
	{"absent", "invoices", "receipts", false},
	{"blank term matches everything", "invoices", "", true},
	{"whitespace-only term matches everything", "invoices", "   ", true},
	{"term is trimmed", "invoices", "  inv  ", true},
	{"empty haystack, non-blank term", "", "inv", false},

	// --- ASCII case folding ---
	{"upper term, lower haystack", "invoices", "INV", true},
	{"lower term, upper haystack", "INVOICES", "inv", true},
	{"mixed case both sides", "InVoIcEs", "vOiC", true},

	// --- the wildcard rows: a LIKE pattern built without escaping gets these wrong ---
	{"percent is literal and present", "discount 100% off", "100%", true},
	{"percent is literal and absent", "discount 50 off", "100%", false},
	{"percent alone does not match everything", "invoices", "%", false},
	{"underscore is literal and present", "a_c naming", "a_c", true},
	{"underscore does not match any character", "abc naming", "a_c", false},
	{"underscore alone does not match everything", "invoices", "_", false},
	{"backslash is literal and present", `path\to\file`, `\to`, true},
	{"backslash is literal and absent", "path/to/file", `\to`, false},
	{"escape sequence is literal", `100\%`, `\%`, true},
	{"bracket-ish characters are literal", "a[b]c", "[b]", true},

	// --- the Unicode rows: a strings.ToLower implementation gets these wrong ---
	{"accented exact match", "café", "café", true},
	{"accented case does NOT fold", "CAFÉ", "café", false},
	{"accented case does NOT fold (reverse)", "café", "CAFÉ", false},
	{"ASCII half of a mixed string still folds", "Café Latte", "LATTE", true},
	{"eszett does not expand", "Straße", "STRASSE", false},
	{"non-Latin exact match", "発注書", "発注", true},
	{"non-Latin absent", "発注書", "請求", false},
	{"cyrillic case does NOT fold", "ПРИВЕТ", "привет", false},
	{"cyrillic exact match", "привет", "привет", true},
}

func TestMatchesSearch(t *testing.T) {
	for _, tt := range searchOracle {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesSearch(tt.haystack, tt.term); got != tt.want {
				t.Errorf("MatchesSearch(%q, %q) = %v, want %v", tt.haystack, tt.term, got, tt.want)
			}
		})
	}
}

// TestParseListRequestSearch pins the transport-edge parsing rules: a term is
// trimmed, blank means absent, and NO term content is ever rejected.
func TestParseListRequestSearch(t *testing.T) {
	tests := []struct {
		name  string
		param string
		want  string
	}{
		{"absent", "", ""},
		{"whitespace only becomes absent", "   ", ""},
		{"trimmed", "  invoices  ", "invoices"},
		{"inner whitespace preserved", "  two words  ", "two words"},
		{"percent is legal input", "100%", "100%"},
		{"underscore is legal input", "a_c", "a_c"},
		{"backslash is legal input", `a\b`, `a\b`},
		{"non-ASCII is legal input", "発注書", "発注書"},
		{"quote is legal input", `o'brien`, `o'brien`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseListRequest(ListParams{Search: tt.param})
			if err != nil {
				t.Fatalf("ParseListRequest(Search=%q) error = %v; a search term's CONTENTS are never invalid", tt.param, err)
			}
			if req.Search != tt.want {
				t.Errorf("Search = %q, want %q", req.Search, tt.want)
			}
		})
	}
}

// TestParseListRequestSearchIsOrthogonal proves search does not disturb the
// existing page/strategy/count parsing.
func TestParseListRequestSearchIsOrthogonal(t *testing.T) {
	req, err := ParseListRequest(ListParams{
		Limit:  "5",
		Offset: "10",
		Count:  "true",
		Search: " widgets ",
	})
	if err != nil {
		t.Fatalf("ParseListRequest: %v", err)
	}
	if req.Limit != 5 || req.Offset != 10 || !req.WithCount || req.ResolvedStrategy() != StrategyOffset {
		t.Errorf("page params disturbed by search: %+v", req)
	}
	if req.Search != "widgets" {
		t.Errorf("Search = %q, want %q", req.Search, "widgets")
	}
}
