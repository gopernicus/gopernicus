package totp

import (
	"crypto/rand"
	"fmt"
	"strings"
)

const (
	// RecoveryCodeCount is how many recovery codes one generation mints.
	RecoveryCodeCount = 10
	// recoveryCodeChars is the length of a normalized recovery code: ten Crockford
	// base32 characters, 50 bits.
	recoveryCodeChars = 10
	// recoveryAlphabet is Crockford base32, lowercase: no i, l, o or u.
	recoveryAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"
)

// NewRecoveryCodes mints n recovery codes in their display form, xxxxx-xxxxx.
func NewRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	raw := make([]byte, recoveryCodeChars)
	for range n {
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("totp: mint recovery code: %w", err)
		}
		var b strings.Builder
		for i, v := range raw {
			if i == recoveryCodeChars/2 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[v&0x1f])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// NormalizeRecoveryCode folds a typed recovery code to the form its digest is
// taken over: lowercase, hyphens and spaces stripped. It reports false for
// anything that is not ten characters of the recovery alphabet.
func NormalizeRecoveryCode(code string) (string, bool) {
	code = strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(code))
	if len(code) != recoveryCodeChars {
		return "", false
	}
	for _, r := range code {
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return "", false
		}
	}
	return code, true
}
