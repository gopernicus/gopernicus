package conversion

import (
	"strings"
	"unicode"
)

// acronyms maps lowercase forms to their correct uppercase representation.
// Extend via AddAcronym.
var acronyms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"api":  "API",
	"http": "HTTP",
	"json": "JSON",
	"sql":  "SQL",
	"uuid": "UUID",
}

// AddAcronym registers an additional acronym for ToPascalCase and ToCamelCase.
// The value should be the canonical uppercase form (e.g. "XML", "JWT").
func AddAcronym(upper string) {
	acronyms[strings.ToLower(upper)] = upper
}

// ToPascalCase converts a snake_case string to PascalCase.
// Registered acronyms are kept uppercase.
//
//	ToPascalCase("user_id")     => "UserID"
//	ToPascalCase("api_key")     => "APIKey"
//	ToPascalCase("created_at")  => "CreatedAt"
func ToPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		if upper, ok := acronyms[strings.ToLower(part)]; ok {
			parts[i] = upper
		} else {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

// ToCamelCase converts a snake_case string to camelCase.
// Leading acronyms are lowercased.
//
//	ToCamelCase("user_id")     => "userID"
//	ToCamelCase("api_key")     => "apiKey"
//	ToCamelCase("created_at")  => "createdAt"
func ToCamelCase(s string) string {
	pascal := ToPascalCase(s)
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

// ToSnakeCase converts a PascalCase or camelCase string to snake_case.
// Handles consecutive capitals (acronyms) as a single word.
//
//	ToSnakeCase("AuthAPIKey")  => "auth_api_key"
//	ToSnakeCase("UserID")      => "user_id"
//	ToSnakeCase("createdAt")   => "created_at"
func ToSnakeCase(s string) string {
	var result strings.Builder
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
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
//
//	ToKebabCase("auth_sessions")  => "auth-sessions"
//	ToKebabCase("api_keys")       => "api-keys"
func ToKebabCase(s string) string {
	return strings.ReplaceAll(ToSnakeCase(s), "_", "-")
}

// ToLowerSpaced converts PascalCase or camelCase to lowercase with spaces.
//
//	ToLowerSpaced("ContentTag")  => "content tag"
//	ToLowerSpaced("UserID")      => "user id"
func ToLowerSpaced(s string) string {
	return strings.ReplaceAll(ToSnakeCase(s), "_", " ")
}
