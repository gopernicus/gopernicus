package environment

import (
	"errors"
	"fmt"
)

// Mode is an application's deployment posture. Components use it to choose
// whether an unsafe configuration is a development warning or a production
// construction error. Hosts map staging, preview, and CI onto one of these two
// postures; execution strategies such as synchronous or queued delivery are
// separate choices.
//
// ValidateMode and ParseMode require an explicit development or production
// value. The zero value is invalid. The host owns the environment key and passes
// the validated mode to components; this type performs no environment lookup.
type Mode string

const (
	// ModeDevelopment is the local/dev posture: a component permits an unsafe
	// configuration and is expected to warn about it rather than refuse to start.
	ModeDevelopment Mode = "development"
	// ModeProduction is the fail-closed posture: a component rejects an unsafe or
	// unprovable configuration at construction.
	ModeProduction Mode = "production"
)

// Mode validation errors. A consumer matches these with errors.Is; the wrapped
// error carries the offending value.
var (
	// ErrModeRequired is returned for the empty Mode. The enum has no default so
	// a host cannot accidentally ship the development posture.
	ErrModeRequired = errors.New(`environment: mode is required ("development" or "production")`)
	// ErrModeInvalid is returned for any value other than "development" or
	// "production".
	ErrModeInvalid = errors.New(`environment: mode must be "development" or "production"`)
)

// ValidateMode enforces the required-enum rule: empty is ErrModeRequired, an
// unknown value is ErrModeInvalid wrapped with the offending value, and the two
// known values are nil.
func ValidateMode(m Mode) error {
	switch m {
	case "":
		return ErrModeRequired
	case ModeDevelopment, ModeProduction:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrModeInvalid, m)
	}
}

// ParseMode accepts exactly "development" or "production", without trimming
// or case folding. On failure it returns the zero Mode and an error matching
// ErrModeRequired or ErrModeInvalid.
func ParseMode(s string) (Mode, error) {
	m := Mode(s)
	if err := ValidateMode(m); err != nil {
		return "", err
	}
	return m, nil
}

// IsProduction reports whether m is the fail-closed posture. It is a readability
// helper for consumers that branch on posture; it is not a validation call, and
// an invalid mode reports false. Validate first.
func (m Mode) IsProduction() bool { return m == ModeProduction }

// String returns the mode's wire value.
func (m Mode) String() string { return string(m) }
