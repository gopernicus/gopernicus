// Package validation provides simple, reflection-free validation functions.
//
// All validators follow the same pattern: they take a field name, the value to
// validate, and return nil if valid or an error describing the problem.
//
// Empty/nil values pass all validators except Required. Compose with Required
// to enforce presence:
//
//	errs.Add(validation.Required("email", req.Email))
//	errs.Add(validation.Email("email", req.Email))
//
// For optional pointer fields, use the Ptr variants (skip validation when nil):
//
//	errs.Add(validation.EmailPtr("email", req.Email))
//
// For custom validators on optional fields, use IfSet:
//
//	errs.Add(validation.IfSet(req.Nickname, func(v string) error {
//	    return validation.MinLength("nickname", v, 3)
//	}))
package validation

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// IfSet runs the validator only if the pointer is non-nil.
// Use this for custom validators on optional fields to avoid writing Ptr variants.
func IfSet[T any](p *T, fn func(T) error) error {
	if p == nil {
		return nil
	}
	return fn(*p)
}

// =============================================================================
// String Validators
// =============================================================================

// Required checks that a string is not empty (after trimming whitespace).
func Required(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// RequiredPtr checks that a pointer is non-nil and the value is not empty.
func RequiredPtr(field string, value *string) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// MinLength checks that a string has at least min characters.
// Empty values pass — use Required separately to enforce presence.
func MinLength(field, value string, min int) error {
	if value == "" {
		return nil
	}
	if len(value) < min {
		return fmt.Errorf("%s must be at least %d characters", field, min)
	}
	return nil
}

// MinLengthPtr checks that a pointer value has at least min characters.
func MinLengthPtr(field string, value *string, min int) error {
	if value != nil && len(*value) < min {
		return fmt.Errorf("%s must be at least %d characters", field, min)
	}
	return nil
}

// MaxLength checks that a string does not exceed max characters.
// Empty values pass — use Required separately to enforce presence.
func MaxLength(field, value string, max int) error {
	if value == "" {
		return nil
	}
	if len(value) > max {
		return fmt.Errorf("%s must be at most %d characters", field, max)
	}
	return nil
}

// MaxLengthPtr checks that a pointer value does not exceed max characters.
func MaxLengthPtr(field string, value *string, max int) error {
	if value != nil && len(*value) > max {
		return fmt.Errorf("%s must be at most %d characters", field, max)
	}
	return nil
}

// OneOf checks that a string is one of the allowed values.
// Empty values pass — use Required separately to enforce presence.
func OneOf(field, value string, allowed ...string) error {
	if value == "" {
		return nil
	}
	if !slices.Contains(allowed, value) {
		return fmt.Errorf("%s must be one of: %s", field, strings.Join(allowed, ", "))
	}
	return nil
}

// OneOfPtr checks that a pointer value is one of the allowed values.
func OneOfPtr(field string, value *string, allowed ...string) error {
	if value == nil {
		return nil
	}
	return OneOf(field, *value, allowed...)
}

// Email checks that a string is a valid email address.
// Empty values pass — use Required separately to enforce presence.
func Email(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := mail.ParseAddress(value); err != nil {
		return fmt.Errorf("%s must be a valid email address", field)
	}
	return nil
}

// EmailPtr checks that a pointer value is a valid email address.
func EmailPtr(field string, value *string) error {
	if value == nil {
		return nil
	}
	return Email(field, *value)
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// UUID checks that a string is a valid UUID format.
// Empty values pass — use Required separately to enforce presence.
func UUID(field, value string) error {
	if value == "" {
		return nil
	}
	if !uuidRegex.MatchString(value) {
		return fmt.Errorf("%s must be a valid UUID", field)
	}
	return nil
}

// UUIDPtr checks that a pointer value is a valid UUID format.
func UUIDPtr(field string, value *string) error {
	if value == nil {
		return nil
	}
	return UUID(field, *value)
}

// URL checks that a string is a valid absolute URL.
// Empty values pass — use Required separately to enforce presence.
func URL(field, value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be a valid URL", field)
	}
	return nil
}

// URLPtr checks that a pointer value is a valid absolute URL.
func URLPtr(field string, value *string) error {
	if value == nil {
		return nil
	}
	return URL(field, *value)
}

// Matches checks that a string matches the given regex pattern.
// Empty values pass — use Required separately to enforce presence.
// The msg parameter describes the expected format (e.g. "a valid phone number").
func Matches(field, value string, pattern *regexp.Regexp, msg string) error {
	if value == "" {
		return nil
	}
	if !pattern.MatchString(value) {
		return fmt.Errorf("%s must be %s", field, msg)
	}
	return nil
}

// MatchesPtr checks that a pointer value matches the given regex pattern.
func MatchesPtr(field string, value *string, pattern *regexp.Regexp, msg string) error {
	if value == nil {
		return nil
	}
	return Matches(field, *value, pattern, msg)
}

// =============================================================================
// Numeric Validators
// =============================================================================

// Min checks that an int is at least min.
func Min(field string, value, min int) error {
	if value < min {
		return fmt.Errorf("%s must be at least %d", field, min)
	}
	return nil
}

// MinPtr checks that an int pointer is at least min.
func MinPtr(field string, value *int, min int) error {
	if value != nil && *value < min {
		return fmt.Errorf("%s must be at least %d", field, min)
	}
	return nil
}

// Max checks that an int is at most max.
func Max(field string, value, max int) error {
	if value > max {
		return fmt.Errorf("%s must be at most %d", field, max)
	}
	return nil
}

// MaxPtr checks that an int pointer is at most max.
func MaxPtr(field string, value *int, max int) error {
	if value != nil && *value > max {
		return fmt.Errorf("%s must be at most %d", field, max)
	}
	return nil
}

// Range checks that an int is within a range (inclusive).
func Range(field string, value, min, max int) error {
	if value < min || value > max {
		return fmt.Errorf("%s must be between %d and %d", field, min, max)
	}
	return nil
}

// Positive checks that an int is greater than zero.
func Positive(field string, value int) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

// PositivePtr checks that an int pointer is greater than zero.
func PositivePtr(field string, value *int) error {
	if value != nil && *value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

// =============================================================================
// Collection Validators
// =============================================================================

// NotEmpty checks that a slice has at least one element.
func NotEmpty[T any](field string, slice []T) error {
	if len(slice) == 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	return nil
}

// MinItems checks that a slice has at least min elements.
func MinItems[T any](field string, slice []T, min int) error {
	if len(slice) < min {
		return fmt.Errorf("%s must have at least %d items", field, min)
	}
	return nil
}

// MaxItems checks that a slice has at most max elements.
func MaxItems[T any](field string, slice []T, max int) error {
	if len(slice) > max {
		return fmt.Errorf("%s must have at most %d items", field, max)
	}
	return nil
}

// =============================================================================
// Common Validators
// =============================================================================

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug checks that a string is a valid URL slug (lowercase letters, numbers, hyphens).
// Empty values pass — use Required separately to enforce presence.
func Slug(field, value string) error {
	if value == "" {
		return nil
	}
	if !slugPattern.MatchString(value) {
		return fmt.Errorf("%s must be a valid slug (lowercase letters, numbers, and hyphens)", field)
	}
	return nil
}

// SlugPtr validates a slug pointer. Nil values pass.
func SlugPtr(field string, value *string) error {
	if value == nil {
		return nil
	}
	return Slug(field, *value)
}

// PasswordStrength checks that a password meets minimum complexity requirements:
// at least 8 characters, one uppercase, one lowercase, one digit, and one special character.
// Empty values pass — use Required separately to enforce presence.
func PasswordStrength(field, value string) error {
	if value == "" {
		return nil
	}

	var (
		hasMinLen  = len(value) >= 8
		hasUpper   bool
		hasLower   bool
		hasDigit   bool
		hasSpecial bool
	)

	for _, r := range value {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	var missing []string
	if !hasMinLen {
		missing = append(missing, "at least 8 characters")
	}
	if !hasUpper {
		missing = append(missing, "one uppercase letter")
	}
	if !hasLower {
		missing = append(missing, "one lowercase letter")
	}
	if !hasDigit {
		missing = append(missing, "one digit")
	}
	if !hasSpecial {
		missing = append(missing, "one special character")
	}

	if len(missing) > 0 {
		return fmt.Errorf("%s must contain %s", field, strings.Join(missing, ", "))
	}
	return nil
}

// PasswordsMatch checks that two password strings are identical.
func PasswordsMatch(password, confirm string) error {
	if password != confirm {
		return fmt.Errorf("passwords do not match")
	}
	return nil
}
