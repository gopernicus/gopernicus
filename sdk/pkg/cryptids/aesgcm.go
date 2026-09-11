package cryptids

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

var _ Encrypter = (*AESGCM)(nil)

// AESGCM is the in-package default Encrypter, using AES-256-GCM. It provides
// authenticated encryption — ciphertext cannot be tampered with without
// detection — and generates a random nonce per call, so encrypting the same
// plaintext twice produces different ciphertext. Every dependency is from the
// standard library (crypto/aes, crypto/cipher).
//
// The ciphertext is raw, unpadded base64url of nonce||ciphertext||tag.
// A key must not encrypt more than 2^32 messages because nonces are random.
type AESGCM struct {
	gcm cipher.AEAD
}

// NewAESGCM creates an AES-256-GCM Encrypter. The key must be exactly 32 bytes
// and should come from a secure source (environment variable, secret manager,
// KMS) — never hardcoded.
func NewAESGCM(key []byte) (*AESGCM, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("aesgcm: key must be exactly 32 bytes, got %d: %w", len(key), sdk.ErrInvalidInput)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aesgcm: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("aesgcm: create GCM: %w", err)
	}

	return &AESGCM{gcm: gcm}, nil
}

// Encrypt encrypts plaintext and returns a base64url-encoded ciphertext. The
// output carries a random nonce prefix, so encrypting the same input twice
// produces different output.
func (e *AESGCM) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("aesgcm: plaintext cannot be empty: %w", sdk.ErrInvalidInput)
	}

	ciphertext := e.gcm.Seal(nil, nil, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt, returning the original plaintext. A tampered or
// truncated ciphertext fails authentication and returns an error.
func (e *AESGCM) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", fmt.Errorf("aesgcm: ciphertext cannot be empty: %w", sdk.ErrInvalidInput)
	}

	data, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("aesgcm: decode base64: %w", err)
	}

	// Preserve the invalid-input classification for a missing 12-byte nonce.
	if len(data) < 12 {
		return "", fmt.Errorf("aesgcm: ciphertext too short: %w", sdk.ErrInvalidInput)
	}

	plaintext, err := e.gcm.Open(nil, nil, data, nil)
	if err != nil {
		return "", fmt.Errorf("aesgcm: decrypt: %w", err)
	}

	return string(plaintext), nil
}
