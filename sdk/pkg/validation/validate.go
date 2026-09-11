// Package validation provides explicit, reflection-free field checks.
//
// Validators return nil for valid input or a *sdk.Violation describing a field
// problem. Collect results with sdk.ValidationError in request or domain code:
//
//	var problems sdk.ValidationError
//	problems.AddViolation(validation.Required("email", in.Email))
//	problems.AddViolation(validation.Email("email", in.Email))
//	return problems.Err()
//
// Optional string checks accept an empty string; compose with Required to
// enforce presence. Pointer variants skip nil and apply the same scalar rule.
// RequiredPtr rejects nil and blank strings. Numeric zero and empty collections
// are checked against the rule's actual bound; they are not implicitly absent.
// String length is measured in Unicode code points, without normalization.
//
// Custom checks belong in the consuming application. Return caller-facing
// sdk.Violation data, or use problems.Add for a domain rule. Unexpected errors
// stay ordinary errors and must not be converted to public field messages.
package validation

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// IfSet runs the validator only if the pointer is non-nil.
// Use this for custom validators on optional fields to avoid writing Ptr variants.
func IfSet[T any](p *T, fn func(T) *sdk.Violation) *sdk.Violation {
	if p == nil {
		return nil
	}
	return fn(*p)
}

// =============================================================================
// String Validators
// =============================================================================

// Required checks that a string is not empty (after trimming whitespace).
func Required(field, value string) *sdk.Violation {
	if strings.TrimSpace(value) == "" {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeRequired,
			Message: fmt.Sprintf("%s is required", field),
		}
	}
	return nil
}

// RequiredPtr checks that a pointer is non-nil and the value is not empty.
func RequiredPtr(field string, value *string) *sdk.Violation {
	if value == nil {
		return Required(field, "")
	}
	return Required(field, *value)
}

// MinLength checks that a string has at least min Unicode code points.
// Empty values pass — use Required separately to enforce presence.
func MinLength(field, value string, min int) *sdk.Violation {
	if value == "" {
		return nil
	}
	if utf8.RuneCountInString(value) < min {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be at least %d characters", field, min),
		}
	}
	return nil
}

// MinLengthPtr applies MinLength when value is non-nil.
func MinLengthPtr(field string, value *string, min int) *sdk.Violation {
	if value == nil {
		return nil
	}
	return MinLength(field, *value, min)
}

// MaxLength checks that a string does not exceed max Unicode code points.
// Empty values pass — use Required separately to enforce presence.
func MaxLength(field, value string, max int) *sdk.Violation {
	if value == "" {
		return nil
	}
	if utf8.RuneCountInString(value) > max {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be at most %d characters", field, max),
		}
	}
	return nil
}

// MaxLengthPtr applies MaxLength when value is non-nil.
func MaxLengthPtr(field string, value *string, max int) *sdk.Violation {
	if value == nil {
		return nil
	}
	return MaxLength(field, *value, max)
}

// OneOf checks that a string is one of the allowed values.
// Empty values pass — use Required separately to enforce presence.
func OneOf(field, value string, allowed ...string) *sdk.Violation {
	if value == "" {
		return nil
	}
	if !slices.Contains(allowed, value) {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be one of: %s", field, strings.Join(allowed, ", ")),
		}
	}
	return nil
}

// OneOfPtr checks that a pointer value is one of the allowed values.
func OneOfPtr(field string, value *string, allowed ...string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return OneOf(field, *value, allowed...)
}

// Email checks the address syntax accepted by net/mail.ParseAddress, including
// display-name forms such as "Alice <alice@example.com>". It does not normalize
// the input or enforce an authentication identifier policy.
// Empty values pass — use Required separately to enforce presence.
func Email(field, value string) *sdk.Violation {
	if value == "" {
		return nil
	}
	if _, err := mail.ParseAddress(value); err != nil {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be a valid email address", field),
		}
	}
	return nil
}

// EmailPtr checks that a pointer value is a valid email address.
func EmailPtr(field string, value *string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Email(field, *value)
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// UUID checks that a string is a valid UUID format.
// Empty values pass — use Required separately to enforce presence.
func UUID(field, value string) *sdk.Violation {
	if value == "" {
		return nil
	}
	if !uuidRegex.MatchString(value) {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be a valid UUID", field),
		}
	}
	return nil
}

// UUIDPtr checks that a pointer value is a valid UUID format.
func UUIDPtr(field string, value *string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return UUID(field, *value)
}

// URL checks that a string is a valid absolute URL.
// Empty values pass — use Required separately to enforce presence.
func URL(field, value string) *sdk.Violation {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be a valid URL", field),
		}
	}
	return nil
}

// URLPtr checks that a pointer value is a valid absolute URL.
func URLPtr(field string, value *string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return URL(field, *value)
}

// Matches checks that a string matches the given regex pattern.
// Empty values pass — use Required separately to enforce presence.
// The msg parameter describes the expected format (e.g. "a valid phone number").
func Matches(field, value string, pattern *regexp.Regexp, msg string) *sdk.Violation {
	if value == "" {
		return nil
	}
	if !pattern.MatchString(value) {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be %s", field, msg),
		}
	}
	return nil
}

// MatchesPtr checks that a pointer value matches the given regex pattern.
func MatchesPtr(field string, value *string, pattern *regexp.Regexp, msg string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Matches(field, *value, pattern, msg)
}

// =============================================================================
// Numeric Validators
// =============================================================================

// Min checks that an int is at least min.
func Min(field string, value, min int) *sdk.Violation {
	if value < min {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be at least %d", field, min),
		}
	}
	return nil
}

// MinPtr checks that an int pointer is at least min.
func MinPtr(field string, value *int, min int) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Min(field, *value, min)
}

// Max checks that an int is at most max.
func Max(field string, value, max int) *sdk.Violation {
	if value > max {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be at most %d", field, max),
		}
	}
	return nil
}

// MaxPtr checks that an int pointer is at most max.
func MaxPtr(field string, value *int, max int) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Max(field, *value, max)
}

// Range checks that an int is within a range (inclusive).
func Range(field string, value, min, max int) *sdk.Violation {
	if value < min || value > max {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be between %d and %d", field, min, max),
		}
	}
	return nil
}

// Positive checks that an int is greater than zero.
func Positive(field string, value int) *sdk.Violation {
	if value <= 0 {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be positive", field),
		}
	}
	return nil
}

// PositivePtr checks that an int pointer is greater than zero.
func PositivePtr(field string, value *int) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Positive(field, *value)
}

// =============================================================================
// Collection Validators
// =============================================================================

// NotEmpty checks that a slice has at least one element.
func NotEmpty[T any](field string, slice []T) *sdk.Violation {
	if len(slice) == 0 {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeRequired,
			Message: fmt.Sprintf("%s must not be empty", field),
		}
	}
	return nil
}

// MinItems checks that a slice has at least min elements.
func MinItems[T any](field string, slice []T, min int) *sdk.Violation {
	if len(slice) < min {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must have at least %d items", field, min),
		}
	}
	return nil
}

// MaxItems checks that a slice has at most max elements.
func MaxItems[T any](field string, slice []T, max int) *sdk.Violation {
	if len(slice) > max {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must have at most %d items", field, max),
		}
	}
	return nil
}

// =============================================================================
// Common Validators
// =============================================================================

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug checks that a string is a valid URL slug (lowercase letters, numbers, hyphens).
// Empty values pass — use Required separately to enforce presence.
func Slug(field, value string) *sdk.Violation {
	if value == "" {
		return nil
	}
	if !slugPattern.MatchString(value) {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: fmt.Sprintf("%s must be a valid slug (lowercase letters, numbers, and hyphens)", field),
		}
	}
	return nil
}

// SlugPtr validates a slug pointer. Nil values pass.
func SlugPtr(field string, value *string) *sdk.Violation {
	if value == nil {
		return nil
	}
	return Slug(field, *value)
}

// PasswordsMatch checks that two password strings are identical and reports
// a mismatch on the caller-selected confirmation field.
func PasswordsMatch(field, password, confirm string) *sdk.Violation {
	if password != confirm {
		return &sdk.Violation{
			Field:   field,
			Code:    sdk.CodeInvalidFormat,
			Message: "passwords do not match",
		}
	}
	return nil
}
