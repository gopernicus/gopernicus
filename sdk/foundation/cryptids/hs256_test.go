package cryptids

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const hs256TestSecret = "test-secret-key-at-least-32-chars-long"

func newHS256(t *testing.T) *HS256 {
	t.Helper()
	s, err := NewHS256([]byte(hs256TestSecret))
	if err != nil {
		t.Fatalf("NewHS256: %v", err)
	}
	return s
}

// forge builds a token with an arbitrary header and claims, signing over the
// segments with key so tests can exercise the alg-confusion and signature
// guards without going through Sign.
func forge(t *testing.T, header map[string]any, claims map[string]any, key []byte) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(hb)
	claimsSeg := base64.RawURLEncoding.EncodeToString(cb)
	signingInput := headerSeg + "." + claimsSeg
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

func TestHS256New(t *testing.T) {
	t.Run("valid secret", func(t *testing.T) {
		if _, err := NewHS256([]byte(hs256TestSecret)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("empty secret rejected", func(t *testing.T) {
		if _, err := NewHS256(nil); !errors.Is(err, ErrSecretTooShort) {
			t.Fatalf("err = %v, want ErrSecretTooShort", err)
		}
	})
	t.Run("short secret rejected", func(t *testing.T) {
		if _, err := NewHS256([]byte("too-short")); !errors.Is(err, ErrSecretTooShort) {
			t.Fatalf("err = %v, want ErrSecretTooShort", err)
		}
	})
	t.Run("secret is copied", func(t *testing.T) {
		secret := []byte("test-secret-key-at-least-32-chars-long")
		s, err := NewHS256(secret)
		if err != nil {
			t.Fatalf("NewHS256: %v", err)
		}
		token, err := s.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		// Mutate the caller's slice; verification must still succeed.
		for i := range secret {
			secret[i] = 0
		}
		if _, err := s.Verify(token); err != nil {
			t.Fatalf("Verify after caller mutated secret: %v", err)
		}
	})
}

func TestHS256RoundTrip(t *testing.T) {
	s := newHS256(t)
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

func TestHS256HeaderIsHS256(t *testing.T) {
	s := newHS256(t)
	token, err := s.Sign(map[string]any{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	headerSeg, _, _ := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if h["alg"] != "HS256" {
		t.Errorf("alg = %v, want HS256", h["alg"])
	}
	if h["typ"] != "JWT" {
		t.Errorf("typ = %v, want JWT", h["typ"])
	}
}

func TestHS256EmptyTokenRejected(t *testing.T) {
	s := newHS256(t)
	if _, err := s.Verify(""); !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("err = %v, want ErrEmptyToken", err)
	}
}

func TestHS256MalformedTokenRejected(t *testing.T) {
	s := newHS256(t)
	for _, tok := range []string{"onlyonepart", "two.parts", "a.b.c.d", "!!!.???.###"} {
		if _, err := s.Verify(tok); !errors.Is(err, ErrMalformedToken) {
			t.Errorf("Verify(%q) err = %v, want ErrMalformedToken", tok, err)
		}
	}
}

func TestHS256TamperedSignatureRejected(t *testing.T) {
	s := newHS256(t)
	token, err := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	// Flip a bit in the decoded signature bytes rather than a base64 character:
	// the final base64url char carries two unused padding bits, so mutating it
	// can decode to the same MAC bytes and leave a valid token. Mutating a byte
	// guarantees the MAC differs.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sig[0] ^= 0x01
	tamperedSeg := base64.RawURLEncoding.EncodeToString(sig)
	if tamperedSeg == parts[2] {
		t.Fatal("tamper did not change the signature segment")
	}
	tampered := parts[0] + "." + parts[1] + "." + tamperedSeg
	if _, err := s.Verify(tampered); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestHS256TamperedClaimsRejected(t *testing.T) {
	s := newHS256(t)
	token, err := s.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	forgedClaims := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":"attacker","exp":9999999999}`))
	tampered := parts[0] + "." + forgedClaims + "." + parts[2]
	if _, err := s.Verify(tampered); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestHS256AlgConfusionRejected(t *testing.T) {
	s := newHS256(t)
	future := time.Now().Add(time.Hour).Unix()

	t.Run("alg=none rejected", func(t *testing.T) {
		// alg=none tokens carry an empty signature; forge with an empty key and
		// clear the signature segment.
		hb, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
		cb, _ := json.Marshal(map[string]any{"user_id": "attacker", "exp": future})
		tok := base64.RawURLEncoding.EncodeToString(hb) + "." +
			base64.RawURLEncoding.EncodeToString(cb) + "."
		if _, err := s.Verify(tok); !errors.Is(err, ErrUnexpectedSigningMethod) {
			t.Fatalf("err = %v, want ErrUnexpectedSigningMethod", err)
		}
	})

	t.Run("alg=HS384 rejected even with the real secret", func(t *testing.T) {
		forged := forge(t,
			map[string]any{"alg": "HS384", "typ": "JWT"},
			map[string]any{"user_id": "attacker", "exp": future},
			[]byte(hs256TestSecret),
		)
		if _, err := s.Verify(forged); !errors.Is(err, ErrUnexpectedSigningMethod) {
			t.Fatalf("err = %v, want ErrUnexpectedSigningMethod", err)
		}
	})

	t.Run("absent alg rejected", func(t *testing.T) {
		forged := forge(t,
			map[string]any{"typ": "JWT"},
			map[string]any{"user_id": "attacker", "exp": future},
			[]byte(hs256TestSecret),
		)
		if _, err := s.Verify(forged); !errors.Is(err, ErrUnexpectedSigningMethod) {
			t.Fatalf("err = %v, want ErrUnexpectedSigningMethod", err)
		}
	})
}

func TestHS256ExpiredRejected(t *testing.T) {
	s := newHS256(t)
	// Well beyond the leeway window.
	token, err := s.Sign(map[string]any{}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s.Verify(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

func TestHS256LeewayHonored(t *testing.T) {
	s := newHS256(t)

	t.Run("just-expired within leeway accepted", func(t *testing.T) {
		// exp 30s in the past — inside the 60s leeway.
		token, err := s.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(-30*time.Second))
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		if _, err := s.Verify(token); err != nil {
			t.Fatalf("Verify within leeway: %v", err)
		}
	})

	t.Run("iat slightly in the future within leeway accepted", func(t *testing.T) {
		forged := forge(t,
			map[string]any{"alg": "HS256", "typ": "JWT"},
			map[string]any{
				"user_id": "u1",
				"iat":     time.Now().Add(30 * time.Second).Unix(),
				"exp":     time.Now().Add(time.Hour).Unix(),
			},
			[]byte(hs256TestSecret),
		)
		if _, err := s.Verify(forged); err != nil {
			t.Fatalf("Verify iat within leeway: %v", err)
		}
	})

	t.Run("iat far in the future rejected", func(t *testing.T) {
		forged := forge(t,
			map[string]any{"alg": "HS256", "typ": "JWT"},
			map[string]any{
				"user_id": "u1",
				"iat":     time.Now().Add(time.Hour).Unix(),
				"exp":     time.Now().Add(2 * time.Hour).Unix(),
			},
			[]byte(hs256TestSecret),
		)
		if _, err := s.Verify(forged); !errors.Is(err, ErrTokenUsedBeforeIssued) {
			t.Fatalf("err = %v, want ErrTokenUsedBeforeIssued", err)
		}
	})
}

func TestHS256WrongSecretRejected(t *testing.T) {
	signerA, err := NewHS256([]byte("secret-alpha-at-least-32-chars-long!!"))
	if err != nil {
		t.Fatalf("NewHS256 A: %v", err)
	}
	signerB, err := NewHS256([]byte("secret-bravo-at-least-32-chars-long!!"))
	if err != nil {
		t.Fatalf("NewHS256 B: %v", err)
	}
	token, err := signerA.Sign(map[string]any{"user_id": "u123"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signerB.Verify(token); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
}
