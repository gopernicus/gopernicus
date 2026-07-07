// Package id generates dependency-free random identifiers using crypto/rand.
// IDs are 128-bit random values encoded as lowercase base32 (no padding),
// which is URL-safe and case-insensitive. v0.1 keys pagination on
// (created_at, id), so sortable IDs are not required.
package id

import (
	"crypto/rand"
	"encoding/base32"
)

// lowerBase32 is RFC 4648 base32 without padding, lowercased.
var lowerBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// New returns a new 128-bit random ID as a 26-character lowercase base32 string.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail; panic rather than return a weak ID.
		panic("id: crypto/rand failed: " + err.Error())
	}
	return toLower(lowerBase32.EncodeToString(b[:]))
}

func toLower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}
