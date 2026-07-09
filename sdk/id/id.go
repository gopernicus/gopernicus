// Package id generates dependency-free random identifiers using crypto/rand.
//
// IDs are nanoid-shaped: New returns DefaultLength (21) characters drawn
// uniformly from Alphabet, a 52-byte confusion-free set (no vowels, no
// O/I/o/i). At the defaults that is 21 * log2(52) ~= 119.8 bits of entropy,
// the same strength class as a 128-bit key. Generation uses mask-based
// rejection sampling for a uniform distribution and reads crypto/rand in a
// 1.6x buffer to cut the number of syscalls.
//
// Error posture is split: New and UUID panic on crypto/rand failure, whose
// only cause is the OS entropy source — treated as fatal rather than handed
// back as a weak ID, keeping call sites clean. NewCustom returns errors
// because it validates caller input (alphabet and size).
//
// Alphabet fixes a bug in the original gopernicus cryptids alphabet, which
// had uppercase Z twice and no lowercase z (biasing output toward Z).
//
// Provenance: segovia-lessons phase 03, decision D5 (2026-07-09); ported from
// the original cryptids id core with the alphabet bug fixed.
package id

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// Generation defaults.
const (
	// DefaultLength is the number of Alphabet characters New emits.
	DefaultLength = 21
	// Alphabet is the confusion-free set New draws from: no vowels and no
	// O/I/o/i, 52 unique bytes.
	Alphabet = "bcdfghjklmnpqrstvwxyzBCDFGHJKLMNPQRSTVWXYZ0123456789"
)

// bufferMultiplier sizes the random byte buffer relative to the desired ID
// length. A larger buffer reduces crypto/rand reads at the cost of memory.
const bufferMultiplier = 1.6

// New returns a DefaultLength-character ID over Alphabet. It panics if
// crypto/rand fails, which the OS entropy source should never do.
func New() string {
	s, err := generate(Alphabet, DefaultLength)
	if err != nil {
		// crypto/rand should never fail; panic rather than return a weak ID.
		panic("id: crypto/rand failed: " + err.Error())
	}
	return s
}

// NewCustom generates an ID of size characters over alphabet. It returns an
// error if alphabet has fewer than 2 characters, contains a duplicate byte,
// or size is less than 1.
func NewCustom(alphabet string, size int) (string, error) {
	if len(alphabet) < 2 {
		return "", errors.New("id: alphabet must contain at least 2 characters")
	}
	if size < 1 {
		return "", errors.New("id: size must be at least 1")
	}
	var seen [256]bool
	for i := 0; i < len(alphabet); i++ {
		if seen[alphabet[i]] {
			return "", fmt.Errorf("id: alphabet has duplicate byte %q", alphabet[i])
		}
		seen[alphabet[i]] = true
	}
	return generate(alphabet, size)
}

// UUID returns a canonical lowercase UUIDv4 string. It panics if crypto/rand
// fails, matching New's posture.
func UUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("id: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generate(alphabet string, size int) (string, error) {
	// Mask is the smallest (2^k - 1) >= len(alphabet)-1, so index & mask
	// yields a value in [0, mask]; values past the alphabet are rejected,
	// which keeps the accepted distribution uniform.
	mask := 1
	for mask < len(alphabet) {
		mask = (mask << 1) | 1
	}

	step := int(float64(size) * bufferMultiplier)
	if step < size {
		step = size
	}

	out := make([]byte, size)
	buf := make([]byte, step)

	idx := 0
	for idx < size {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for i := 0; i < len(buf) && idx < size; i++ {
			alphabetIndex := int(buf[i]) & mask
			if alphabetIndex >= len(alphabet) {
				continue
			}
			out[idx] = alphabet[alphabetIndex]
			idx++
		}
	}

	return string(out), nil
}
