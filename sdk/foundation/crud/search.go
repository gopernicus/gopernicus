package crud

import "strings"

// SearchField describes a searchable text column — the deliberate twin of
// OrderField (crud-search-upstream T1). A host declares which columns a list may
// be searched over exactly the way it declares which columns it may be sorted by,
// so neither the transport edge nor the store learns a second idiom.
//
// Only vetted column names may reach a store's WHERE clause. An empty
// SearchFields slice means the list is NOT searchable, and a non-blank search
// term against it is an error rather than a silently unfiltered page.
type SearchField struct {
	Column string
}

// MatchesSearch reports whether haystack matches term: a case-insensitive
// LITERAL substring.
//
// LITERAL is the load-bearing word. A search term is something a human typed, so
// `%`, `_`, `\`, and any other byte are ordinary characters. The v1 generator
// built its pattern as `"%" + term + "%"` with no escaping, which meant someone
// typing `100%` matched every row and someone typing `a_c` matched `abc`. That is
// not restored.
//
// # Case-folding contract
//
// The fold is ASCII-ONLY and deliberately narrow, because it must be reproducible
// in three places that cannot share an implementation: this function, PostgreSQL's
// ILIKE, and SQLite/libSQL's default LIKE. All three agree on ASCII letters and
// all three leave non-ASCII code points alone, so:
//
//   - ASCII letters compare case-insensitively ("Foo" matches "foo");
//   - every non-ASCII code point compares EXACTLY ("É" does not match "é", and
//     "Straße" does not match "STRASSE").
//
// Go's strings.ToLower would fold Unicode too, which would make this function
// disagree with both SQL dialects. Moving to full Unicode folding later is a
// deliberate, cross-dialect contract change — the oracle table in the tests
// includes accented and non-Latin rows precisely so such a change cannot happen
// by accident in one backend.
//
// term is trimmed; a blank term matches everything (no search is not a filter).
func MatchesSearch(haystack, term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return true
	}
	return strings.Contains(foldASCII(haystack), foldASCII(term))
}

// foldASCII lowercases ASCII letters and leaves every other byte untouched — the
// exact fold PostgreSQL's ILIKE and SQLite's LIKE both perform by default.
func foldASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
