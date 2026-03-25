package authentication

import "fmt"

// Password policy defaults per NIST SP 800-63B.
const (
	// MinPasswordLength is the minimum password length (NIST minimum: 8).
	MinPasswordLength = 8

	// MaxPasswordLength is the maximum password length. NIST recommends
	// supporting at least 64 characters. We cap at 72 because bcrypt
	// silently truncates beyond that.
	MaxPasswordLength = 72
)

// ValidatePassword checks a password against NIST SP 800-63B guidelines.
//
// This covers synchronous, pure checks only:
//   - Not empty
//   - At least [MinPasswordLength] characters
//   - At most [MaxPasswordLength] characters (bcrypt truncation boundary)
//
// It intentionally does NOT enforce complexity rules (uppercase, special
// chars, digits) — NIST SP 800-63B §5.1.1.2 recommends against them as
// they produce predictable patterns.
//
// Breached-password checking (e.g. HaveIBeenPwned) is an I/O-bound
// concern that callers should handle separately if desired.
func ValidatePassword(password string) error {
	if password == "" {
		return fmt.Errorf("password is required")
	}
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLength)
	}
	return nil
}
