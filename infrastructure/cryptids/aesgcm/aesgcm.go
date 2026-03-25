// Package aesgcm provides an Encrypter implementation using AES-256-GCM.
//
// AES-GCM provides authenticated encryption — ciphertext cannot be tampered
// with without detection. Each encryption generates a random nonce, so
// encrypting the same plaintext twice produces different ciphertext.
//
// This is an adapter — it implements cryptids.Encrypter. All dependencies
// are from the standard library (crypto/aes, crypto/cipher, crypto/rand).
//
// Example:
//
//	enc, err := aesgcm.New(key) // key must be exactly 32 bytes
//	ciphertext, err := enc.Encrypt("secret-token")
//	plaintext, err := enc.Decrypt(ciphertext)
package aesgcm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
)

var _ cryptids.Encrypter = (*Encrypter)(nil)

// Encrypter implements cryptids.Encrypter using AES-256-GCM.
type Encrypter struct {
	gcm cipher.AEAD
}

// New creates an AES-256-GCM encrypter. The key must be exactly 32 bytes.
//
// The key should be loaded from a secure source (environment variable,
// secret manager, KMS) — never hardcoded.
func New(key []byte) (*Encrypter, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("aesgcm: key must be exactly 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aesgcm: create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aesgcm: create GCM: %w", err)
	}

	return &Encrypter{gcm: gcm}, nil
}

// Encrypt encrypts a plaintext string and returns a base64-encoded ciphertext.
// The output includes a random nonce prefix, so encrypting the same input
// twice produces different output.
func (e *Encrypter) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("aesgcm: plaintext cannot be empty")
	}

	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("aesgcm: generate nonce: %w", err)
	}

	// Seal appends the ciphertext to the nonce, so the result is nonce+ciphertext.
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext string produced by [Encrypt].
func (e *Encrypter) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", fmt.Errorf("aesgcm: ciphertext cannot be empty")
	}

	data, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("aesgcm: decode base64: %w", err)
	}

	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("aesgcm: ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("aesgcm: decrypt: %w", err)
	}

	return string(plaintext), nil
}
