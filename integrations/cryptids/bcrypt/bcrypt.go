// Package bcrypt is a password-hashing connector wrapping exactly one
// third-party library, golang.org/x/crypto/bcrypt. Its Hasher structurally
// satisfies the Hasher port in pockets/authentication/logic/authentication
// without importing that pocket. The port lives with its consumer; this
// integration uses SDK error classifications.
//
// It owns "how to hash with bcrypt," never any pocket's policy. Cost is
// configurable via New's options; a different algorithm (argon2, scrypt) would
// be a sibling connector, swapped at the composition root.
//
// Inputs beyond bcrypt's byte limit are classified as sdk.ErrInvalidInput.
// Minimum length and password complexity remain host policy.
package bcrypt

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/gopernicus/gopernicus/sdk"
)

// maxPasswordBytes is bcrypt's input limit, enforced for hashing and verification.
const maxPasswordBytes = 72

// ErrPasswordTooLong is returned by HashPassword and VerifyPassword when the
// password exceeds bcrypt's 72-byte limit. It wraps sdk.ErrInvalidInput.
var ErrPasswordTooLong = fmt.Errorf("%w: bcrypt: password exceeds %d bytes", sdk.ErrInvalidInput, maxPasswordBytes)

// Hasher hashes passwords at a configured cost and verifies existing hashes
// using their stored cost. The zero value uses bcrypt.DefaultCost for hashing.
type Hasher struct {
	cost int
}

// Option configures construction of a Hasher. Options apply in order.
type Option func(*config)

type config struct {
	cost int
}

// WithCost sets the bcrypt cost factor. Out-of-range values (below
// bcrypt.MinCost or above bcrypt.MaxCost) fall back to bcrypt.DefaultCost.
// Higher cost is slower and more resistant to brute force; 10-12 is a common
// production range.
func WithCost(cost int) Option {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = bcrypt.DefaultCost
	}
	return func(cfg *config) { cfg.cost = cost }
}

// New builds a Hasher, defaulting to bcrypt.DefaultCost when no WithCost option
// is given. A nil option panics.
func New(opts ...Option) *Hasher {
	cfg := config{cost: bcrypt.DefaultCost}
	for _, opt := range opts {
		if opt == nil {
			panic("bcrypt: nil Option")
		}
		opt(&cfg)
	}
	return &Hasher{cost: cfg.cost}
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
// (bcrypt.CompareHashAndPassword). Candidates over 72 bytes return
// ErrPasswordTooLong before comparison.
func (h *Hasher) VerifyPassword(hash, password string) error {
	if len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return fmt.Errorf("bcrypt: password mismatch: %w", err)
		}
		return fmt.Errorf("bcrypt: verify: %w", err)
	}
	return nil
}
