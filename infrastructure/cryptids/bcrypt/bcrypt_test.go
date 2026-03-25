package bcrypt

import (
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
)

// Compile-time check that Hasher implements cryptids.PasswordHasher.
var _ cryptids.PasswordHasher = (*Hasher)(nil)

func TestNewHasher(t *testing.T) {
	t.Run("clamps low cost", func(t *testing.T) {
		h := NewHasher(1)
		hash, err := h.Hash("password")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hash == "" {
			t.Fatal("empty hash")
		}
	})

	t.Run("clamps high cost", func(t *testing.T) {
		h := NewHasher(50)
		if h == nil {
			t.Fatal("nil hasher")
		}
	})
}

func TestHash(t *testing.T) {
	h := NewHasher(4) // low cost for fast tests

	t.Run("valid bcrypt hash", func(t *testing.T) {
		hash, err := h.Hash("password123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(hash, "$2") {
			t.Errorf("hash should start with $2, got: %s", hash[:10])
		}
	})

	t.Run("different hashes for same password", func(t *testing.T) {
		h1, _ := h.Hash("same-password")
		h2, _ := h.Hash("same-password")
		if h1 == h2 {
			t.Error("bcrypt should produce different hashes (random salt)")
		}
	})

	t.Run("rejects empty password", func(t *testing.T) {
		_, err := h.Hash("")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("rejects password over 72 bytes", func(t *testing.T) {
		_, err := h.Hash(strings.Repeat("a", 100))
		if err == nil {
			t.Fatal("expected error for >72 byte password")
		}
		if !strings.Contains(err.Error(), "72 bytes") {
			t.Errorf("error should mention 72 bytes, got: %v", err)
		}
	})

	t.Run("accepts 72 byte password", func(t *testing.T) {
		hash, err := h.Hash(strings.Repeat("a", 72))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hash == "" {
			t.Fatal("empty hash")
		}
	})

	t.Run("handles special characters", func(t *testing.T) {
		hash, err := h.Hash("p@$$w0rd!#$%^&*()")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hash == "" {
			t.Fatal("empty hash")
		}
	})
}

func TestCompare(t *testing.T) {
	h := NewHasher(4)

	t.Run("matches correct password", func(t *testing.T) {
		hash, _ := h.Hash("correct-password")
		if err := h.Compare(hash, "correct-password"); err != nil {
			t.Errorf("expected match, got: %v", err)
		}
	})

	t.Run("rejects wrong password", func(t *testing.T) {
		hash, _ := h.Hash("correct-password")
		err := h.Compare(hash, "wrong-password")
		if err == nil {
			t.Fatal("expected mismatch error")
		}
		if !strings.Contains(err.Error(), "mismatch") {
			t.Errorf("error should mention mismatch, got: %v", err)
		}
	})

	t.Run("rejects empty hash", func(t *testing.T) {
		if err := h.Compare("", "password"); err == nil {
			t.Fatal("expected error for empty hash")
		}
	})

	t.Run("rejects empty password", func(t *testing.T) {
		hash, _ := h.Hash("password")
		if err := h.Compare(hash, ""); err == nil {
			t.Fatal("expected error for empty password")
		}
	})

	t.Run("case sensitive", func(t *testing.T) {
		hash, _ := h.Hash("Password123")
		if err := h.Compare(hash, "password123"); err == nil {
			t.Error("should be case sensitive")
		}
	})
}
