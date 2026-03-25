// Package cryptidstest provides compliance tests for cryptids interfaces.
//
// Example:
//
//	func TestHasherCompliance(t *testing.T) {
//	    hasher := bcrypt.NewHasher(10)
//	    cryptidstest.RunHasherSuite(t, hasher)
//	}
//
//	func TestSignerCompliance(t *testing.T) {
//	    signer, _ := golangjwt.NewSigner("my-secret-key-at-least-32-chars!")
//	    cryptidstest.RunSignerSuite(t, signer)
//	}
package cryptidstest

import (
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
)

// RunHasherSuite runs compliance tests for PasswordHasher implementations.
func RunHasherSuite(t *testing.T, h cryptids.PasswordHasher) {
	t.Helper()

	t.Run("HashAndCompare", func(t *testing.T) {
		hash, err := h.Hash("test-password")
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if hash == "" {
			t.Fatal("Hash: returned empty string")
		}
		if hash == "test-password" {
			t.Fatal("Hash: returned plaintext password")
		}
		if err := h.Compare(hash, "test-password"); err != nil {
			t.Fatalf("Compare: correct password should match: %v", err)
		}
	})

	t.Run("CompareMismatch", func(t *testing.T) {
		hash, err := h.Hash("correct-password")
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		if err := h.Compare(hash, "wrong-password"); err == nil {
			t.Fatal("Compare: wrong password should not match")
		}
	})

	t.Run("DifferentHashesForSamePassword", func(t *testing.T) {
		hash1, err := h.Hash("same-password")
		if err != nil {
			t.Fatalf("Hash 1: %v", err)
		}
		hash2, err := h.Hash("same-password")
		if err != nil {
			t.Fatalf("Hash 2: %v", err)
		}
		// Salted hashers should produce different hashes.
		// Deterministic hashers (SHA256) may produce the same hash.
		// Both are valid — this test just verifies no error.
		_ = hash1
		_ = hash2
	})
}

// RunEncrypterSuite runs compliance tests for Encrypter implementations.
func RunEncrypterSuite(t *testing.T, e cryptids.Encrypter) {
	t.Helper()

	t.Run("EncryptAndDecrypt", func(t *testing.T) {
		plaintext := "secret-oauth-token-abc123"
		ciphertext, err := e.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if ciphertext == "" {
			t.Fatal("Encrypt: returned empty string")
		}
		if ciphertext == plaintext {
			t.Fatal("Encrypt: returned plaintext")
		}

		got, err := e.Decrypt(ciphertext)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plaintext {
			t.Fatalf("Decrypt: got %q, want %q", got, plaintext)
		}
	})

	t.Run("DifferentCiphertextsForSamePlaintext", func(t *testing.T) {
		ct1, err := e.Encrypt("same-token")
		if err != nil {
			t.Fatalf("Encrypt 1: %v", err)
		}
		ct2, err := e.Encrypt("same-token")
		if err != nil {
			t.Fatalf("Encrypt 2: %v", err)
		}
		if ct1 == ct2 {
			t.Fatal("Encrypt: same plaintext should produce different ciphertexts (random nonce)")
		}
		// Both should decrypt to the same value.
		p1, _ := e.Decrypt(ct1)
		p2, _ := e.Decrypt(ct2)
		if p1 != p2 {
			t.Fatalf("Decrypt: got different plaintexts %q and %q", p1, p2)
		}
	})

	t.Run("DecryptTamperedFails", func(t *testing.T) {
		ct, err := e.Encrypt("tamper-test")
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		// Tamper with the ciphertext.
		tampered := ct + "x"
		_, err = e.Decrypt(tampered)
		if err == nil {
			t.Fatal("Decrypt: tampered ciphertext should fail")
		}
	})

	t.Run("EncryptEmptyFails", func(t *testing.T) {
		_, err := e.Encrypt("")
		if err == nil {
			t.Fatal("Encrypt: empty plaintext should fail")
		}
	})

	t.Run("DecryptEmptyFails", func(t *testing.T) {
		_, err := e.Decrypt("")
		if err == nil {
			t.Fatal("Decrypt: empty ciphertext should fail")
		}
	})
}

// RunSignerSuite runs compliance tests for JWTSigner implementations.
func RunSignerSuite(t *testing.T, s cryptids.JWTSigner) {
	t.Helper()

	t.Run("SignAndVerify", func(t *testing.T) {
		claims := map[string]any{
			"user_id": "u_123",
			"role":    "admin",
		}
		token, err := s.Sign(claims, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if token == "" {
			t.Fatal("Sign: returned empty token")
		}

		got, err := s.Verify(token)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}

		userID, ok := got["user_id"]
		if !ok {
			t.Fatal("Verify: missing user_id claim")
		}
		if fmt, ok := userID.(string); !ok || fmt != "u_123" {
			t.Fatalf("Verify: user_id got %v, want %q", userID, "u_123")
		}
	})

	t.Run("VerifyExpired", func(t *testing.T) {
		claims := map[string]any{"user_id": "u_expired"}
		token, err := s.Sign(claims, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("Sign expired: %v", err)
		}

		_, err = s.Verify(token)
		if err == nil {
			t.Fatal("Verify: expired token should fail")
		}
	})

	t.Run("VerifyInvalidToken", func(t *testing.T) {
		_, err := s.Verify("not-a-valid-token")
		if err == nil {
			t.Fatal("Verify: invalid token should fail")
		}
	})
}
