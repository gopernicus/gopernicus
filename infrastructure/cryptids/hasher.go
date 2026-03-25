package cryptids

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// PasswordHasher defines the interface for password hashing and comparison.
// Implementations (bcrypt, argon2, etc.) live in cryptids/bcrypt/, etc.
//
//	type UserService struct {
//	    hasher cryptids.PasswordHasher
//	}
type PasswordHasher interface {
	// Hash takes a plaintext password and returns a hashed representation.
	Hash(password string) (string, error)

	// Compare checks whether a plaintext password matches a hash.
	// Returns nil on match, error on mismatch or failure.
	Compare(hash, password string) error
}

// SHA256Hasher implements fast, deterministic hashing using SHA256.
// Suitable for API keys where speed is important and salting isn't needed.
//
// NOT suitable for passwords — use a PasswordHasher adapter
// (bcrypt, argon2) for user passwords.
type SHA256Hasher struct{}

// NewSHA256Hasher creates a new SHA256 hasher.
func NewSHA256Hasher() *SHA256Hasher {
	return &SHA256Hasher{}
}

// Hash returns the SHA256 hash of the input as a hex string.
func (h *SHA256Hasher) Hash(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key cannot be empty")
	}
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:]), nil
}
