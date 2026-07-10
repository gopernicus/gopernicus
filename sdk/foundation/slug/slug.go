// Package slug provides URL-safe slug generation. Pure algorithm, no domain
// knowledge — framework-generic, so it lives in sdk per the architecture rule
// for pure algorithms (slug codec, cursor encode, …).
package slug

import "strings"

// accentFold maps common Latin-1 accented letters to their ASCII base letter,
// so Make transliterates them instead of dropping them as separators. It is the
// hand-rolled rune table ported from the original repo's URL slugger — stdlib
// only, no golang.org/x/text. ß folds to a single "s", matching that table
// exactly.
var accentFold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
	'ý': 'y', 'ÿ': 'y',
	'ñ': 'n', 'ç': 'c',
	'ß': 's',
}

// Make converts a string into a URL-safe slug: lowercased, common Latin-1
// accented letters folded to their ASCII base (é→e, ñ→n, ç→c), runs of the
// remaining non-alphanumeric characters collapsed to single hyphens, and
// leading/trailing hyphens trimmed. So Make("Café Résumé") == "cafe-resume".
//
// BEHAVIOR CHANGE (sdk-parity D-5): accent folding runs before the keep-[a-z0-9]
// pass; previously accented letters were dropped as separators (Make("Café")
// was "caf", now "cafe"). Persisted slugs are unaffected — cms entry, term, and
// menu slugs are computed once at write time and stored, so no existing URL
// moves. The one recompute path is features/cms/domain/content/schema.go, which
// derives content-type route segments from a registered type's Plural at
// runtime; a host that registered a non-ASCII Plural sees its route segment
// change on upgrade. Shipped seed types (Article, Page) are ASCII and unaffected.
func Make(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if folded, ok := accentFold[r]; ok {
			r = folded
		}
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
