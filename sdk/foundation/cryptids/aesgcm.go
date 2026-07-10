package cryptids

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/gopernicus/gopernicus/sdk"
)

var _ Encrypter = (*AESGCM)(nil)

// AESGCM is the in-package default Encrypter, using AES-256-GCM. It provides
// authenticated encryption — ciphertext cannot be tampered with without
// detection — and generates a random nonce per call, so encrypting the same
// plaintext twice produces different ciphertext. Every dependency is from the
// standard library (crypto/aes, crypto/cipher, crypto/rand).
//
// The ciphertext is base64url (raw, unpadded) of nonce||ciphertext.
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

	gcm, err := cipher.NewGCM(block)
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

	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("aesgcm: generate nonce: %w", err)
	}

	// Seal appends the ciphertext to the nonce, so the result is nonce||ciphertext.
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
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

	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("aesgcm: ciphertext too short: %w", sdk.ErrInvalidInput)
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("aesgcm: decrypt: %w", err)
	}

	return string(plaintext), nil
}
