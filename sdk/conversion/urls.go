package conversion

import (
	"regexp"
	"strings"
)

var (
	separatorsRe  = regexp.MustCompile(`[\s\-_]+`)
	nonAlphaNumRe = regexp.MustCompile(`[^a-z0-9\-]`)
	multiHyphenRe = regexp.MustCompile(`-+`)
)

// ToURLSlug converts a string to a URL-safe slug.
//
//	ToURLSlug("Hello World!")  => "hello-world"
//	ToURLSlug("Café Résumé")  => "cafe-resume"
func ToURLSlug(s string) string {
	s = strings.ToLower(s)
	s = removeAccents(s)
	s = separatorsRe.ReplaceAllString(s, "-")
	s = nonAlphaNumRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	s = multiHyphenRe.ReplaceAllString(s, "-")
	return s
}

func removeAccents(s string) string {
	accentMap := map[rune]rune{
		'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
		'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
		'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
		'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
		'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
		'ý': 'y', 'ÿ': 'y',
		'ñ': 'n', 'ç': 'c',
		'ß': 's',
	}

	var result strings.Builder
	for _, r := range s {
		if replacement, exists := accentMap[r]; exists {
			result.WriteRune(replacement)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
