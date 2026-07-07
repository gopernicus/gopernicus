package cryptids

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// SHA256Hasher is a fast, deterministic hasher producing a hex-encoded SHA-256
// digest. It suits values where speed matters and salting isn't needed —
// hashing API keys for a constant-time lookup, for example.
//
// It is deliberately NOT for passwords: password hashing needs a slow, salted
// algorithm. That belongs to the auth feature's PasswordHasher port and its
// bcrypt integration (integrations/cryptids/bcrypt), not here.
type SHA256Hasher struct{}

// NewSHA256Hasher creates a SHA256Hasher.
func NewSHA256Hasher() *SHA256Hasher {
	return &SHA256Hasher{}
}

// Hash returns the SHA-256 digest of key as a 64-character lowercase hex
// string. An empty key is rejected.
func (h *SHA256Hasher) Hash(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("cryptids: key cannot be empty: %w", errs.ErrInvalidInput)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]), nil
}
