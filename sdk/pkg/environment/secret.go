package environment

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Secret decoding errors can be matched with errors.Is.
var (
	// ErrSecretEncoding is returned when a value is not valid hex.
	ErrSecretEncoding = errors.New("environment: secret is not valid hex")
	// ErrSecretUnderMinimum is returned when a value decodes to fewer than the
	// requested number of bytes.
	ErrSecretUnderMinimum = errors.New("environment: secret is under the minimum length")
)

// DecodeSecret decodes a hex-encoded secret. Empty input returns nil, nil;
// the host decides whether a missing secret is permitted.
//
// When minBytes > 0, shorter decoded values return ErrSecretUnderMinimum.
// Invalid hex returns ErrSecretEncoding. Errors omit the raw value; encoding
// errors name at most the offending character.
//
// The minimum is only a floor. Consumers enforce their own key constraints:
// for example, a key longer than 32 bytes passes a floor of 32 but is rejected
// by cryptids.NewAESGCM, which requires exactly 32 bytes.
func DecodeSecret(value string, minBytes int) ([]byte, error) {
	if value == "" {
		return nil, nil
	}

	raw, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSecretEncoding, err)
	}

	if minBytes > 0 && len(raw) < minBytes {
		return nil, fmt.Errorf("%w: need at least %d bytes, got %d", ErrSecretUnderMinimum, minBytes, len(raw))
	}

	return raw, nil
}

// Secret reads a hex-encoded environment variable and calls DecodeSecret.
// Unset and empty values return nil, nil. Errors include the key name without
// including the raw value. For a namespaced key, use GetNamespaceEnvKey.
//
// The host owns missing-key policy, including whether to generate an ephemeral
// development key or reject production startup. The consuming constructor
// still validates the decoded key's exact requirements.
func Secret(key string, minBytes int) ([]byte, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil, nil
	}

	raw, err := DecodeSecret(value, minBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}

	return raw, nil
}
