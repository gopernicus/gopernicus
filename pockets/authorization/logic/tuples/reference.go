package tuples

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// MaxRefFieldLen bounds an opaque reference component in bytes.
const MaxRefFieldLen = 256

// ErrInvalidRef reports a malformed authorization reference or scope.
// It wraps [sdk.ErrInvalidInput].
var ErrInvalidRef = fmt.Errorf("authorization reference: %w", sdk.ErrInvalidInput)

// ValidateRefField checks an opaque reference component without normalizing it.
// A value must be non-empty, at most [MaxRefFieldLen] bytes, valid UTF-8 and free
// of control characters. field names the component in the error message.
func ValidateRefField(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty: %w", field, ErrInvalidRef)
	}
	if len(value) > MaxRefFieldLen {
		return fmt.Errorf("%s exceeds %d bytes: %w", field, MaxRefFieldLen, ErrInvalidRef)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8: %w", field, ErrInvalidRef)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character: %w", field, ErrInvalidRef)
		}
	}
	return nil
}
