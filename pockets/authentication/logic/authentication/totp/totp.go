// Package totp is the native second-factor domain (SOC 2 S4c): RFC 6238
// time-based one-time passwords over the RFC 4226 HMAC-SHA1 truncation, the
// single-use recovery codes that stand beside a confirmed credential, and the
// aggregate repository that stores both per user. The algorithm is stdlib only;
// the flows that enroll, step up and remove run in the authentication service.
// TOTP is a second factor only — never a sign-in method.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// SecretBytes is the width of a minted shared secret (160 bits, the RFC 4226
	// recommendation for HMAC-SHA1).
	SecretBytes = 20
	// Digits is the length of every code.
	Digits = 6
	// Period is the time step.
	Period = 30 * time.Second
	// Algorithm names the HMAC the key URI advertises.
	Algorithm = "SHA1"
)

// secretEncoding is the authenticator-app key alphabet: RFC 4648 base32,
// unpadded.
var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns SecretBytes random bytes.
func NewSecret() ([]byte, error) {
	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("totp: mint secret: %w", err)
	}
	return secret, nil
}

// EncodeSecret renders secret as unpadded base32, the form an authenticator app
// is typed or scanned.
func EncodeSecret(secret []byte) string {
	return secretEncoding.EncodeToString(secret)
}

// DecodeSecret parses an unpadded base32 secret; spaces are ignored and case is
// folded. A malformed value is sdk.ErrInvalidInput.
func DecodeSecret(encoded string) ([]byte, error) {
	raw, err := secretEncoding.DecodeString(strings.ToUpper(strings.ReplaceAll(encoded, " ", "")))
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("totp: secret is not base32: %w", sdk.ErrInvalidInput)
	}
	return raw, nil
}

// FormatSecret renders secret in space-separated groups of four, the form a
// person types into an app.
func FormatSecret(secret []byte) string {
	encoded := EncodeSecret(secret)
	var b strings.Builder
	for i := 0; i < len(encoded); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(encoded[i:min(i+4, len(encoded))])
	}
	return b.String()
}

// Step is the RFC 6238 time step at t.
func Step(t time.Time) int64 {
	return t.Unix() / int64(Period/time.Second)
}

// Code is the RFC 4226 HOTP value of secret at counter step: HMAC-SHA1, dynamic
// truncation, Digits decimal digits.
func Code(secret []byte, step int64) string {
	mac := hmac.New(sha1.New, secret)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	code := strconv.FormatUint(uint64(value%1_000_000), 10)
	return strings.Repeat("0", Digits-len(code)) + code
}

// NormalizeCode strips the spaces and hyphens a person types into a code and
// reports whether what remains is Digits decimal digits.
func NormalizeCode(code string) (string, bool) {
	code = strings.NewReplacer(" ", "", "-", "").Replace(code)
	if len(code) != Digits {
		return "", false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return code, true
}

// Validate reports the step whose code equals code within window steps either
// side of at's step. Every candidate step is compared in constant time and the
// earliest match wins; the caller refuses a step it has already accepted.
func Validate(secret []byte, code string, at time.Time, window int) (int64, bool) {
	normalized, ok := NormalizeCode(code)
	if !ok || len(secret) == 0 || window < 0 {
		return 0, false
	}
	now := Step(at)
	var matched int64
	found := 0
	for step := now - int64(window); step <= now+int64(window); step++ {
		eq := subtle.ConstantTimeCompare([]byte(Code(secret, step)), []byte(normalized))
		if eq == 1 && found == 0 {
			matched = step
		}
		found |= eq
	}
	return matched, found == 1
}

// KeyURI is the otpauth:// key URI an authenticator app scans:
// otpauth://totp/<issuer>:<account>?secret=…&issuer=…&algorithm=SHA1&digits=6&period=30.
// The label is percent-encoded. An empty issuer or account, or a colon in the
// issuer, is sdk.ErrInvalidInput.
func KeyURI(issuer, account string, secret []byte) (string, error) {
	if issuer == "" || account == "" || strings.Contains(issuer, ":") || len(secret) == 0 {
		return "", fmt.Errorf("totp: key URI needs an issuer without a colon, an account and a secret: %w", sdk.ErrInvalidInput)
	}
	q := url.Values{}
	q.Set("secret", EncodeSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", Algorithm)
	q.Set("digits", strconv.Itoa(Digits))
	q.Set("period", strconv.Itoa(int(Period/time.Second)))
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return "otpauth://totp/" + label + "?" + q.Encode(), nil
}
