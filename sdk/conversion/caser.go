package conversion

import (
	"strings"
	"unicode"
)

// defaultCaser is the package-level immutable Caser that the package-level case
// functions delegate to. It carries only the built-in acronym table, so the
// package funcs behave exactly as they did before the Caser seam existed.
var defaultCaser = NewCaser()

// CaserOption configures a Caser at construction. An option applies only to the
// Caser being built — it never mutates the package default or any other Caser.
type CaserOption func(*Caser)

// Caser performs identifier case conversion with a per-instance acronym table.
// It is immutable: NewCaser copies the built-in table before applying options
// and no method mutates the instance, so a Caser is safe to share across
// goroutines and custom acronyms never leak into the package default. This is
// the seam that replaces the original package's mutable AddAcronym global.
type Caser struct {
	acronyms map[string]string
}

// WithAcronyms registers additional acronyms on the Caser under construction.
// Each value is the canonical uppercase form (e.g. "K8S", "XML", "JWT") and is
// matched case-insensitively against snake_case parts by ToPascalCase/ToCamelCase.
func WithAcronyms(upper ...string) CaserOption {
	return func(c *Caser) {
		for _, u := range upper {
			c.acronyms[strings.ToLower(u)] = u
		}
	}
}

// NewCaser returns an immutable Caser seeded with the built-in acronym table
// plus any acronyms the options add. The built-in table is copied, so options
// mutate only the fresh instance.
func NewCaser(opts ...CaserOption) Caser {
	c := Caser{acronyms: make(map[string]string, len(acronyms))}
	for k, v := range acronyms {
		c.acronyms[k] = v
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// ToPascalCase converts a snake_case string to PascalCase, keeping this Caser's
// registered acronyms uppercase.
func (c Caser) ToPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		if upper, ok := c.acronyms[strings.ToLower(part)]; ok {
			parts[i] = upper
		} else {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

// ToCamelCase converts a snake_case string to camelCase, lowercasing a leading
// acronym.
func (c Caser) ToCamelCase(s string) string {
	pascal := c.ToPascalCase(s)
	if len(pascal) == 0 {
		return pascal
	}
	for i, r := range pascal {
		if i == 0 {
			continue
		}
		if r >= 'a' && r <= 'z' {
			if i == 1 {
				return strings.ToLower(pascal[:1]) + pascal[1:]
			}
			return strings.ToLower(pascal[:i-1]) + pascal[i-1:]
		}
	}
	return strings.ToLower(pascal)
}

// ToSnakeCase converts a PascalCase or camelCase string to snake_case, treating
// consecutive capitals as a single word. It is acronym-table independent.
func (c Caser) ToSnakeCase(s string) string {
	var result strings.Builder
	runes := []rune(s)

	for i := range runes {
		r := runes[i]

		if i > 0 && unicode.IsUpper(r) {
			prevIsLower := unicode.IsLower(runes[i-1])
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])

			if prevIsLower || nextIsLower {
				result.WriteRune('_')
			}
		}

		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// ToKebabCase converts a snake_case string to kebab-case.
func (c Caser) ToKebabCase(s string) string {
	return strings.ReplaceAll(c.ToSnakeCase(s), "_", "-")
}

// ToLowerSpaced converts PascalCase or camelCase to lowercase with spaces.
func (c Caser) ToLowerSpaced(s string) string {
	return strings.ReplaceAll(c.ToSnakeCase(s), "_", " ")
}
