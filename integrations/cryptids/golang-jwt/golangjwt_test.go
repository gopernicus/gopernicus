package golangjwt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
)

const testSecret = "test-secret-key-at-least-32-chars-long"

func newSigner(t *testing.T, opts ...golangjwt.Option) *golangjwt.Signer {
	t.Helper()
	s, err := golangjwt.New(testSecret, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// signRaw signs claims with golang-jwt directly, bypassing Signer, so tests can
// forge tokens with an arbitrary signing method to exercise the verification
// guards.
func signRaw(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return s
}

func TestNew(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		if _, err := golangjwt.New(testSecret); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty secret rejected", func(t *testing.T) {
		if _, err := golangjwt.New(""); !errors.Is(err, golangjwt.ErrSecretTooShort) {
			t.Fatalf("err = %v, want ErrSecretTooShort", err)
		}
	})

	t.Run("short secret rejected", func(t *testing.T) {
		if _, err := golangjwt.New("too-short"); !errors.Is(err, golangjwt.ErrSecretTooShort) {
			t.Fatalf("err = %v, want ErrSecretTooShort", err)
		}
	})
}

func TestRoundTrip(t *testing.T) {
	s := newSigner(t)
	expiresAt := time.Now().Add(time.Hour)

	token, err := s.Sign(map[string]any{"user_id": "u123", "role": "admin"}, expiresAt)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["user_id"] != "u123" {
		t.Errorf("user_id = %v, want u123", claims["user_id"])
	}
	if claims["role"] != "admin" {
		t.Errorf("role = %v, want admin", claims["role"])
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatal("exp claim missing or not a number")
	}
	if int64(exp) != expiresAt.Unix() {
		t.Errorf("exp = %d, want %d", int64(exp), expiresAt.Unix())
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Error("iat claim missing or not a number")
	}
}

func TestRoundTripHS512(t *testing.T) {
	s := newSigner(t, golangjwt.WithMethod(jwt.SigningMethodHS512))
	token, err := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := s.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["user_id"] != "u123" {
		t.Errorf("user_id = %v, want u123", claims["user_id"])
	}
}

func TestEmptyTokenRejected(t *testing.T) {
	s := newSigner(t)
	if _, err := s.Verify(""); !errors.Is(err, golangjwt.ErrEmptyToken) {
		t.Fatalf("err = %v, want ErrEmptyToken", err)
	}
}

func TestMalformedTokenRejected(t *testing.T) {
	s := newSigner(t)
	if _, err := s.Verify("not.a.valid.jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	s := newSigner(t)
	token, err := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tampered := token[:len(token)-1] + "X"
	if _, err := s.Verify(tampered); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	s := newSigner(t)
	token, err := s.Sign(map[string]any{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.Verify(token); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestNotYetValidTokenRejected(t *testing.T) {
	s := newSigner(t)
	// nbf in the future makes the token not-yet-valid; golang-jwt validates it.
	token, err := s.Sign(
		map[string]any{"nbf": time.Now().Add(time.Hour).Unix()},
		time.Now().Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.Verify(token); err == nil {
		t.Fatal("expected error for not-yet-valid token")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	signerA, err := golangjwt.New("secret-alpha-at-least-32-chars-long!!")
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	signerB, err := golangjwt.New("secret-bravo-at-least-32-chars-long!!")
	if err != nil {
		t.Fatalf("New B: %v", err)
	}
	token, err := signerA.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signerB.Verify(token); err == nil {
		t.Fatal("expected error verifying with the wrong key")
	}
}

// TestAlgorithmConfusionRejected is the security-critical case: a token whose
// signing method differs from the Signer's configured method must be rejected,
// even when the same secret is used. This defeats the JWT algorithm-confusion
// attack in each of its forms.
func TestAlgorithmConfusionRejected(t *testing.T) {
	claims := jwt.MapClaims{
		"user_id": "attacker",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}

	t.Run("different HMAC variant, same secret", func(t *testing.T) {
		// Forge an HS512 token with the very secret an HS256 Signer holds; a
		// family-only check ("is it HMAC?") would wrongly accept it.
		forged := signRaw(t, jwt.SigningMethodHS512, []byte(testSecret), claims)
		s := newSigner(t) // HS256
		if _, err := s.Verify(forged); err == nil {
			t.Fatal("HS256 Signer accepted an HS512 token signed with its secret")
		}
	})

	t.Run("HS256 token rejected when HS512 configured", func(t *testing.T) {
		hs256 := newSigner(t) // HS256
		token, err := hs256.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		hs512 := newSigner(t, golangjwt.WithMethod(jwt.SigningMethodHS512))
		if _, err := hs512.Verify(token); err == nil {
			t.Fatal("HS512 Signer accepted an HS256 token")
		}
	})

	t.Run("alg=none rejected", func(t *testing.T) {
		forged := signRaw(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims)
		s := newSigner(t)
		if _, err := s.Verify(forged); err == nil {
			t.Fatal("Signer accepted an alg=none token")
		}
	})
}
