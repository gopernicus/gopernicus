// Package cryptids provides ID generation, hashing interfaces, and their adapters.
//
// The name is a portmanteau of "crypto tidbits." The core package is std lib
// only (crypto/rand). Implementations that require external dependencies live
// in subpackages (cryptids/bcrypt, cryptids/golangjwt).
//
// # ID Generation
//
// GenerateID creates URL-safe, cryptographically secure random identifiers
// suitable for database primary keys, API tokens, and session IDs.
//
//	id, err := cryptids.GenerateID() // "V1StGXR8Z5jdHi6B4mT3p"
//
// # Interfaces
//
// PasswordHasher and JWTSigner define the ports that adapters implement.
//
//	func NewUserService(hasher cryptids.PasswordHasher, signer cryptids.JWTSigner) *UserService
package cryptids

import (
	"crypto/rand"
	"fmt"
)

// Default ID configuration
var (
	idAlphabet = "bcdfghjklmnpqrstvwxyZBCDFGHJKLMNPQRSTVWXYZ0123456789"
	idLength   = 21
)

// bufferMultiplier determines the size of the random byte buffer relative to
// the desired ID length. A larger buffer reduces the number of crypto/rand
// calls needed, improving performance at the cost of slightly more memory.
const bufferMultiplier = 1.6

// GenerateID creates a cryptographically secure random string using the
// default alphabet and length (21 characters, URL-safe).
//
// The default alphabet excludes visually confusing characters (O, I, o, i)
// and vowels (to avoid accidental words).
func GenerateID() (string, error) {
	return generateID(idAlphabet, idLength)
}

// GenerateCustomID creates a cryptographically secure random string using
// a custom alphabet and length.
func GenerateCustomID(alphabet string, size int) (string, error) {
	return generateID(alphabet, size)
}

func generateID(alphabet string, size int) (string, error) {
	if len(alphabet) < 2 {
		return "", fmt.Errorf("alphabet must contain at least 2 characters")
	}
	if size < 1 {
		return "", fmt.Errorf("size must be at least 1")
	}

	// Calculate the mask based on the closest power of 2 >= alphabet length
	mask := 1
	for mask < len(alphabet) {
		mask = (mask << 1) | 1
	}

	// Create a buffer larger than needed to reduce crypto/rand calls
	step := int(float64(size) * bufferMultiplier)
	if step < size {
		step = size
	}

	id := make([]byte, size)
	bytes := make([]byte, step)

	idIndex := 0
	for idIndex < size {
		_, err := rand.Read(bytes)
		if err != nil {
			return "", err
		}

		for i := 0; i < len(bytes) && idIndex < size; i++ {
			// Applying mask ensures uniform distribution
			alphabetIndex := int(bytes[i]) & mask
			if alphabetIndex >= len(alphabet) {
				continue
			}
			id[idIndex] = alphabet[alphabetIndex]
			idIndex++
		}
	}

	return string(id), nil
}
