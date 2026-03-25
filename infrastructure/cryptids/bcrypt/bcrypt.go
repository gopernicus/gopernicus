// Package bcrypt provides a PasswordHasher implementation using
// golang.org/x/crypto/bcrypt.
//
// This is an adapter — it implements cryptids.PasswordHasher. If a different
// hashing algorithm is needed (argon2, scrypt), write a new adapter and swap
// at the composition root.
//
// Example:
//
//	hasher := bcrypt.NewHasher(10) // cost 10-12 recommended for production
//	hash, err := hasher.Hash("password")
//	err = hasher.Compare(hash, "password") // nil if match
package bcrypt

import (
	"fmt"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"golang.org/x/crypto/bcrypt"
)

var _ cryptids.PasswordHasher = (*Hasher)(nil)

// Hasher implements cryptids.PasswordHasher using bcrypt.
type Hasher struct {
	cost int
}

// Options holds configuration for the bcrypt hasher.
type Options struct {
	Cost int `env:"BCRYPT_COST" default:"10"`
}

// New creates a bcrypt hasher from Options (typically parsed from environment).
// Cost is clamped to the valid range [4, 31]. Recommended: 10-12 for production.
func New(opts Options) *Hasher {
	cost := max(opts.Cost, bcrypt.MinCost)
	cost = min(cost, 31)
	return &Hasher{cost: cost}
}

// NewHasher creates a bcrypt hasher with the specified cost directly.
// Cost is clamped to the valid range [4, 31]. Recommended: 10-12 for production.
func NewHasher(cost int) *Hasher {
	return New(Options{Cost: cost})
}

// Hash takes a plaintext password and returns a bcrypt hash.
// Returns an error if the password is empty or exceeds 72 bytes
// (bcrypt's maximum — silently truncating would be a security risk).
func (h *Hasher) Hash(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}
	if len([]byte(password)) > 72 {
		return "", fmt.Errorf("password exceeds 72 bytes (bcrypt maximum)")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: failed to hash: %w", err)
	}

	return string(hash), nil
}

// Compare checks whether a plaintext password matches a bcrypt hash.
// Returns nil on match, error on mismatch or failure.
func (h *Hasher) Compare(hash, password string) error {
	if hash == "" || password == "" {
		return fmt.Errorf("hash and password must not be empty")
	}

	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		if err == bcrypt.ErrMismatchedHashAndPassword {
			return fmt.Errorf("password mismatch")
		}
		return fmt.Errorf("bcrypt: failed to compare: %w", err)
	}

	return nil
}
