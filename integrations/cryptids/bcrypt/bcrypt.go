// Package bcrypt is a password-hashing connector wrapping exactly one
// third-party library, golang.org/x/crypto/bcrypt. Its Hasher structurally
// satisfies the feature-owned PasswordHasher port (mirrors auth.PasswordHasher
// in the auth feature module) with zero import in either direction: the port
// lives with its consumer, this integration knows only bcrypt.
//
// It owns "how to hash with bcrypt," never any feature's policy. Cost is
// configurable via New's options; a different algorithm (argon2, scrypt) would
// be a sibling connector, swapped at the composition root.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt) depending only
// on golang.org/x/crypto. The port promises a self-describing hash and a
// non-nil error on mismatch with a constant-time compare — all satisfied here
// with plain, stable errors, so no sdk dependency is needed.
package bcrypt

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// maxPasswordBytes is bcrypt's hard input limit. Anything longer is silently
// truncated by the algorithm, which would let distinct passwords collide — a
// security foot-gun — so Hasher rejects over-long input instead.
const maxPasswordBytes = 72

// ErrPasswordTooLong is returned by HashPassword when the password exceeds
// bcrypt's 72-byte limit. Rejecting is deliberate: bcrypt truncates silently,
// and truncation would make distinct passwords verify against the same hash.
var ErrPasswordTooLong = fmt.Errorf("bcrypt: password exceeds %d bytes", maxPasswordBytes)

// Hasher hashes and verifies passwords with bcrypt at a fixed cost. Construct it
// with New; the zero value is not usable.
type Hasher struct {
	cost int
}

// Option configures a Hasher passed to New.
type Option func(*Hasher)

// WithCost sets the bcrypt cost factor. Out-of-range values (below
// bcrypt.MinCost or above bcrypt.MaxCost) fall back to bcrypt.DefaultCost.
// Higher cost is slower and more resistant to brute force; 10-12 is a common
// production range.
func WithCost(cost int) Option {
	return func(h *Hasher) {
		if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
			cost = bcrypt.DefaultCost
		}
		h.cost = cost
	}
}

// New builds a Hasher, defaulting to bcrypt.DefaultCost when no WithCost option
// is given.
func New(opts ...Option) *Hasher {
	h := &Hasher{cost: bcrypt.DefaultCost}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HashPassword returns a self-describing bcrypt hash of password. It returns
// ErrPasswordTooLong for input over bcrypt's 72-byte limit rather than letting
// the algorithm truncate.
func (h *Hasher) HashPassword(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: hash: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches hash, returning nil on a match
// and a non-nil error otherwise. The comparison is constant time
// (bcrypt.CompareHashAndPassword).
func (h *Hasher) VerifyPassword(hash, password string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return fmt.Errorf("bcrypt: password mismatch: %w", err)
		}
		return fmt.Errorf("bcrypt: verify: %w", err)
	}
	return nil
}
