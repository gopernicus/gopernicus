package golangjwt

import (
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
)

// Compile-time check that Signer implements cryptids.JWTSigner.
var _ cryptids.JWTSigner = (*Signer)(nil)

const testSecret = "test-secret-key-at-least-32-chars-long"

func TestNewSigner(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		s, err := NewSigner(testSecret)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Fatal("nil signer")
		}
	})

	t.Run("empty secret", func(t *testing.T) {
		_, err := NewSigner("")
		if err == nil {
			t.Fatal("expected error for empty secret")
		}
	})

	t.Run("short secret", func(t *testing.T) {
		_, err := NewSigner("too-short")
		if err == nil {
			t.Fatal("expected error for secret shorter than 32 bytes")
		}
	})
}

func TestSign(t *testing.T) {
	s, _ := NewSigner(testSecret)

	t.Run("produces valid JWT structure", func(t *testing.T) {
		token, err := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			t.Errorf("JWT should have 3 parts, got %d", len(parts))
		}
	})

	t.Run("embeds custom claims", func(t *testing.T) {
		claims := map[string]any{"user_id": "u123", "count": float64(42)}
		token, _ := s.Sign(claims, time.Now().Add(time.Hour))

		result, err := s.Verify(token)
		if err != nil {
			t.Fatalf("verify failed: %v", err)
		}
		if result["user_id"] != "u123" {
			t.Errorf("user_id = %v, want u123", result["user_id"])
		}
		if result["count"] != float64(42) {
			t.Errorf("count = %v, want 42", result["count"])
		}
	})

	t.Run("sets expiry", func(t *testing.T) {
		expiresAt := time.Now().Add(time.Hour)
		token, _ := s.Sign(map[string]any{}, expiresAt)

		result, _ := s.Verify(token)
		exp, ok := result["exp"].(float64)
		if !ok {
			t.Fatal("exp claim missing or wrong type")
		}
		if int64(exp) != expiresAt.Unix() {
			t.Errorf("exp = %v, want %v", int64(exp), expiresAt.Unix())
		}
	})
}

func TestVerify(t *testing.T) {
	s, _ := NewSigner(testSecret)

	t.Run("valid token", func(t *testing.T) {
		token, _ := s.Sign(map[string]any{"role": "admin"}, time.Now().Add(time.Hour))
		result, err := s.Verify(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["role"] != "admin" {
			t.Errorf("role = %v, want admin", result["role"])
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token, _ := s.Sign(map[string]any{}, time.Now().Add(-time.Hour))
		_, err := s.Verify(token)
		if err == nil {
			t.Fatal("expected error for expired token")
		}
	})

	t.Run("empty token", func(t *testing.T) {
		_, err := s.Verify("")
		if err == nil {
			t.Fatal("expected error for empty token")
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		_, err := s.Verify("not.a.valid.jwt")
		if err == nil {
			t.Fatal("expected error for malformed token")
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		s1, _ := NewSigner("secret-one-at-least-32-chars-long!!")
		s2, _ := NewSigner("secret-two-at-least-32-chars-long!!")

		token, _ := s1.Sign(map[string]any{}, time.Now().Add(time.Hour))
		_, err := s2.Verify(token)
		if err == nil {
			t.Fatal("expected error for wrong secret")
		}
	})

	t.Run("tampered token", func(t *testing.T) {
		token, _ := s.Sign(map[string]any{}, time.Now().Add(time.Hour))
		tampered := token[:len(token)-1] + "X"
		_, err := s.Verify(tampered)
		if err == nil {
			t.Fatal("expected error for tampered token")
		}
	})
}

func BenchmarkSign(b *testing.B) {
	s, _ := NewSigner(testSecret)
	claims := map[string]any{"user_id": "u123"}
	exp := time.Now().Add(time.Hour)
	for i := 0; i < b.N; i++ {
		_, _ = s.Sign(claims, exp)
	}
}

func BenchmarkVerify(b *testing.B) {
	s, _ := NewSigner(testSecret)
	token, _ := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	for i := 0; i < b.N; i++ {
		_, _ = s.Verify(token)
	}
}
