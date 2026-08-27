package environment

import (
	"errors"
	"fmt"
)

// Mode is an application's deployment posture: the app-wide switch a component
// consults to decide whether an unsafe-but-convenient configuration is a startup
// WARN or a fail-closed construction error.
//
// It is a REQUIRED enum with no default. The empty value is ErrModeRequired and
// an unknown value is ErrModeInvalid, so a component can never silently inherit
// the permissive development posture from an unset field.
//
// Mode is deliberately NOT any of the following:
//
//   - a build tag. Go build tags select which code compiles; Mode selects how the
//     compiled code behaves at construction. One binary runs in both modes.
//   - a full environment name. Only "development" and "production" exist. A host
//     with staging, preview, CI, or test environments maps each to the security
//     posture it wants — normally ModeProduction, because a staging deployment
//     that accepts a body-leaking console transport is a real disclosure risk.
//     Adding a third value would create a posture nobody has defined the rules
//     for.
//   - a delivery or execution model. pockets/authentication's DeliveryMode
//     ("off"/"in_process"/"jobs") selects an execution strategy and is orthogonal:
//     a host picks a Mode and a DeliveryMode independently.
//
// The mode is a value, not an ambient lookup: nothing in this package reads an
// environment variable to produce one. The host owns the key name — AUTH_RUNTIME_MODE,
// APP_MODE, ENV, or anything else — and passes the parsed value down:
//
//	mode, err := environment.ParseMode(environment.GetEnvOrDefault("APP_MODE", ""))
//	if err != nil {
//		return fmt.Errorf("APP_MODE: %w", err)
//	}
//
// Blessing one variable name here would make every consumer's configuration
// implicit and untestable, so ParseMode takes the already-read string.
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

// ParseMode converts an already-read configuration string into a Mode. It reads
// no environment variable of its own — the caller decides which key holds the
// posture. Parsing is exact: the value must be "development" or "production"
// with no case folding, trimming, or aliasing, so a typo fails loudly instead of
// resolving to a posture nobody chose. On failure it returns the zero Mode and
// an ErrModeRequired/ErrModeInvalid error.
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
