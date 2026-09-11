package sdk

import "strings"

// accentFold maps common Latin-1 accented letters to their ASCII base letter.
// ß folds to a single "s". Unmapped non-ASCII characters become separators.
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

// Slugify converts a string into a URL-safe slug: lowercased, common Latin-1
// accented letters folded to their ASCII base (é→e, ñ→n, ç→c), runs of the
// remaining characters outside [a-z0-9] collapsed to single hyphens, and
// leading/trailing hyphens trimmed. So Slugify("Café Résumé") == "cafe-resume".
//
// Slugify does not normalize Unicode: combining marks remain separators. A value
// with no retained letters or digits produces an empty slug. Callers decide
// whether an empty result is acceptable and enforce uniqueness.
//
// Regenerating a stored slug after an algorithm change can change its URL even
// if the source text is unchanged. Consumers own when to regenerate identifiers
// and how to preserve existing URLs.
func Slugify(s string) string {
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
