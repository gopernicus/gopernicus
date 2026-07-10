// Package conversion holds small, dependency-free helpers for converting
// between representations: pointer <-> value, identifier case styles,
// datetime parsing, JSON null-safety, and slice overlap.
//
// Two helpers from the original package are handled differently here:
//   - AddAcronym: the original mutated a package-global acronym registry, which
//     was a data race. Custom acronyms now live on an immutable Caser
//     (NewCaser(WithAcronyms("K8S", ...))). The package-level case funcs
//     delegate to a default Caser carrying the built-in table, so no global is
//     mutable and custom acronyms never leak between casers.
//   - ToURLSlug: URL slugging (including accent folding) is owned by sdk/foundation/slug,
//     the one canonical slugger.
package conversion

// acronyms is the built-in table NewCaser seeds every Caser from (including the
// package default). It maps lowercase forms to their canonical uppercase form.
// It is never mutated; per-instance additions go through WithAcronyms.
var acronyms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"api":  "API",
	"http": "HTTP",
	"json": "JSON",
	"sql":  "SQL",
	"uuid": "UUID",
}

// ToPascalCase converts a snake_case string to PascalCase.
// Registered acronyms are kept uppercase.
//
//	ToPascalCase("user_id")     => "UserID"
//	ToPascalCase("api_key")     => "APIKey"
//	ToPascalCase("created_at")  => "CreatedAt"
func ToPascalCase(s string) string { return defaultCaser.ToPascalCase(s) }

// ToCamelCase converts a snake_case string to camelCase.
// Leading acronyms are lowercased.
//
//	ToCamelCase("user_id")     => "userID"
//	ToCamelCase("api_key")     => "apiKey"
//	ToCamelCase("created_at")  => "createdAt"
func ToCamelCase(s string) string { return defaultCaser.ToCamelCase(s) }

// ToSnakeCase converts a PascalCase or camelCase string to snake_case.
// Handles consecutive capitals (acronyms) as a single word.
//
//	ToSnakeCase("AuthAPIKey")  => "auth_api_key"
//	ToSnakeCase("UserID")      => "user_id"
//	ToSnakeCase("createdAt")   => "created_at"
func ToSnakeCase(s string) string { return defaultCaser.ToSnakeCase(s) }

// ToKebabCase converts a snake_case string to kebab-case.
//
//	ToKebabCase("auth_sessions")  => "auth-sessions"
//	ToKebabCase("api_keys")       => "api-keys"
func ToKebabCase(s string) string { return defaultCaser.ToKebabCase(s) }

// ToLowerSpaced converts PascalCase or camelCase to lowercase with spaces.
//
//	ToLowerSpaced("ContentTag")  => "content tag"
//	ToLowerSpaced("UserID")      => "user id"
func ToLowerSpaced(s string) string { return defaultCaser.ToLowerSpaced(s) }
