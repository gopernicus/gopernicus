package cryptids

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// SHA256 returns a nonempty value's digest as 64 lowercase hexadecimal characters.
// It is suitable for stable digests and high-entropy token lookup. Human passwords
// require a slow, salted password hasher instead.
func SHA256(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("cryptids: value cannot be empty: %w", sdk.ErrInvalidInput)
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]), nil
}
