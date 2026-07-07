package cryptids

import (
	"crypto/rand"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

func newTestAESGCM(t *testing.T) *AESGCM {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := NewAESGCM(key)
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}
	return enc
}

func TestAESGCMEncryptAndDecrypt(t *testing.T) {
	enc := newTestAESGCM(t)

	plaintext := "secret-oauth-token-abc123"
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == "" {
		t.Fatal("Encrypt: returned empty string")
	}
	if ciphertext == plaintext {
		t.Fatal("Encrypt: returned plaintext")
	}

	got, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("Decrypt: got %q, want %q", got, plaintext)
	}
}

func TestAESGCMDifferentCiphertextsForSamePlaintext(t *testing.T) {
	enc := newTestAESGCM(t)

	ct1, err := enc.Encrypt("same-token")
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	ct2, err := enc.Encrypt("same-token")
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}
	if ct1 == ct2 {
		t.Fatal("Encrypt: same plaintext should produce different ciphertexts (random nonce)")
	}

	p1, _ := enc.Decrypt(ct1)
	p2, _ := enc.Decrypt(ct2)
	if p1 != p2 {
		t.Fatalf("Decrypt: got different plaintexts %q and %q", p1, p2)
	}
}

func TestAESGCMDecryptTamperedFails(t *testing.T) {
	enc := newTestAESGCM(t)

	ct, err := enc.Encrypt("tamper-test")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := enc.Decrypt(ct + "x"); err == nil {
		t.Fatal("Decrypt: tampered ciphertext should fail")
	}
}

func TestAESGCMEmptyInputsFail(t *testing.T) {
	enc := newTestAESGCM(t)

	if _, err := enc.Encrypt(""); err == nil {
		t.Fatal("Encrypt: empty plaintext should fail")
	} else if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("Encrypt empty: got %v, want ErrInvalidInput", err)
	}

	if _, err := enc.Decrypt(""); err == nil {
		t.Fatal("Decrypt: empty ciphertext should fail")
	} else if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("Decrypt empty: got %v, want ErrInvalidInput", err)
	}
}

func TestNewAESGCMKeyValidation(t *testing.T) {
	tests := []struct {
		name    string
		keyLen  int
		wantErr bool
	}{
		{"16 bytes", 16, true},
		{"24 bytes", 24, true},
		{"32 bytes", 32, false},
		{"64 bytes", 64, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := NewAESGCM(key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewAESGCM(%d bytes): err=%v, wantErr=%v", tt.keyLen, err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, errs.ErrInvalidInput) {
				t.Fatalf("NewAESGCM(%d bytes): got %v, want ErrInvalidInput", tt.keyLen, err)
			}
		})
	}
}
