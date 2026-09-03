package environment

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Secret decoding errors. A consumer matches these with errors.Is; the wrapped
// error carries the cause. They are deliberately not named ErrSecretTooShort:
// cryptids exports a sentinel by that name, and a host that wires Secret and
// then NewHS256 has both in scope.
var (
	// ErrSecretEncoding is returned when a value is not valid hex.
	ErrSecretEncoding = errors.New("environment: secret is not valid hex")
	// ErrSecretUnderMinimum is returned when a value decodes to fewer than the
	// requested number of bytes.
	ErrSecretUnderMinimum = errors.New("environment: secret is under the minimum length")
)

// DecodeSecret decodes a hex-encoded secret and enforces a minimum decoded
// length. Hex is the only encoding: a 32-byte key is 64 hex characters, and a
// value read as raw ASCII elsewhere fails here with ErrSecretEncoding rather
// than silently becoming key material of the wrong shape.
//
// An empty value returns nil, nil — a template that leaves a key blank produces
// no key and no error, and the host owns the posture branch.
//
// minBytes is a FLOOR, not the authority. It is checked only when positive;
// minBytes <= 0 disables the check entirely. The consuming constructor still
// enforces its own rule: cryptids.NewHS256 and NewHMACChallengeProtector accept
// 32 bytes or more, while NewAESGCM requires EXACTLY 32 and continues to reject
// a longer key that passes a floor of 32 here.
//
// The returned error is safe to log — a hex failure names at most the single
// offending character, and neither error carries the value. The raw value never
// is.
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

// Secret reads a hex-encoded secret from the environment and decodes it with
// DecodeSecret, wrapping every error with the key name so the message says which
// variable is wrong.
//
// An unset key and a key set to the empty string both return nil, nil. That
// collapse is deliberate — a copied template leaves a key blank, and the two
// cases call for the same host behavior — but it means a caller cannot tell
// them apart through this function.
//
// There is no namespace parameter: a namespaced host composes the key itself
// with Secret(GetNamespaceEnvKey(ns, key), minBytes).
//
// The posture policy is the host's, not the sdk's. The full intended shape:
//
//	raw, err := environment.Secret("AUTH_TOKEN_ENCRYPTER_KEY", 32)
//	if err != nil {
//		return err
//	}
//	if raw == nil {
//		if mode.IsProduction() {
//			return errors.New("AUTH_TOKEN_ENCRYPTER_KEY is required in production")
//		}
//		raw = ephemeral32()
//		log.Warn("AUTH_TOKEN_ENCRYPTER_KEY unset: EPHEMERAL key …")
//	}
//	enc, err := cryptids.NewAESGCM(raw) // still exactly 32 bytes; 32 here is a floor
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
