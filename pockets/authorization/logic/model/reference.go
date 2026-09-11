package model

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// MaxRefFieldLen bounds an opaque reference component (a type, id, relation, or
// permission name). It is a byte bound, applied after the UTF-8 validity check.
const MaxRefFieldLen = 256

// ErrInvalidRef indicates a reference component is empty, over-long, not valid
// UTF-8, or carries a control character. It wraps [sdk.ErrInvalidInput].
var ErrInvalidRef = fmt.Errorf("authorization reference: %w", sdk.ErrInvalidInput)

// PrincipalRef is a concrete decision caller or actor — always a (Type, ID)
// pair, NEVER a userset. It is the only subject a decision request carries: a
// userset relation cannot be expressed here, so no public decision path can
// smuggle one in. It mirrors sdk.Principal field-for-field and is directly
// convertible from it (see authorization.PrincipalFrom).
type PrincipalRef struct {
	Type string // "user" or "service_account" (the runtime principal types)
	ID   string
}

// Validate reports whether the principal is structurally usable: both Type and
// ID must be present and well formed (see ValidateRefField).
func (p PrincipalRef) Validate() error {
	if err := ValidateRefField("principal type", p.Type); err != nil {
		return err
	}
	return ValidateRefField("principal id", p.ID)
}

// ValidateRefField reports whether an opaque reference component is well formed:
// non-empty, at most [MaxRefFieldLen] bytes, valid UTF-8, and free of control
// characters. The value is treated as an opaque exact string — no case folding
// or trimming is applied. field names the component for the error message.
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
