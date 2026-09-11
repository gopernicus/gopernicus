package golangjwt_test

import (
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
)

const testSecret = "test-secret-key-at-least-64-bytes-long-for-HS256-HS384-and-HS512-tests"

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

func TestNew_MethodAndKeyBoundaries(t *testing.T) {
	for _, tt := range []struct {
		method *jwt.SigningMethodHMAC
		bytes  int
	}{
		{jwt.SigningMethodHS256, 32},
		{jwt.SigningMethodHS384, 48},
		{jwt.SigningMethodHS512, 64},
	} {
		t.Run(tt.method.Alg(), func(t *testing.T) {
			if _, err := golangjwt.New(strings.Repeat("a", tt.bytes-1), golangjwt.WithMethod(tt.method)); !errors.Is(err, golangjwt.ErrSecretTooShort) {
				t.Errorf("short key error = %v, want ErrSecretTooShort", err)
			}
			signer, err := golangjwt.New(strings.Repeat("a", tt.bytes), golangjwt.WithMethod(tt.method))
			if err != nil {
				t.Fatalf("minimum length key: %v", err)
			}
			token, err := signer.Sign(nil, time.Now().Add(time.Hour))
			if err != nil {
				t.Fatalf("Sign with minimum length key: %v", err)
			}
			if _, err := signer.Verify(token); err != nil {
				t.Fatalf("Verify with minimum length key: %v", err)
			}
		})
	}
	for _, method := range []*jwt.SigningMethodHMAC{
		{Name: "custom", Hash: crypto.SHA256},
		{Name: "HS256", Hash: crypto.SHA512},
		{Name: "HS512", Hash: crypto.SHA256},
	} {
		if _, err := golangjwt.New(testSecret, golangjwt.WithMethod(method)); err == nil {
			t.Errorf("accepted unsupported method %+v", method)
		}
	}
	if _, err := golangjwt.New(strings.Repeat("é", 16), golangjwt.WithMethod(nil)); err != nil {
		t.Errorf("32-byte key with nil method option: %v", err)
	}
}

func TestNew_CopiesSelectedMethod(t *testing.T) {
	method := *jwt.SigningMethodHS256
	signer := newSigner(t, golangjwt.WithMethod(&method))
	method = *jwt.SigningMethodHS512
	token, err := signer.Sign(nil, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := newSigner(t).Verify(token); err != nil {
		t.Fatalf("configured HS256 changed after caller mutated its method: %v", err)
	}
}

func TestUninitializedSigner(t *testing.T) {
	for _, signer := range []*golangjwt.Signer{nil, {}} {
		if token, err := signer.Sign(nil, time.Now().Add(time.Hour)); err == nil || token != "" {
			t.Errorf("uninitialized Sign returned token %q and error %v", token, err)
		}
		if claims, err := signer.Verify("dummy-token"); err == nil || claims != nil {
			t.Errorf("uninitialized Verify returned claims %v and error %v", claims, err)
		}
	}
}

func TestSignOwnsTimeClaims(t *testing.T) {
	signer := newSigner(t)
	input := map[string]any{"user_id": "u123", "exp": "ignored", "iat": "ignored"}
	expiresAt := time.Now().Add(time.Hour)
	before := time.Now().Unix()
	token, err := signer.Sign(input, expiresAt)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["exp"] != float64(expiresAt.Unix()) {
		t.Errorf("exp = %v, want expiresAt %d", claims["exp"], expiresAt.Unix())
	}
	iat, ok := claims["iat"].(float64)
	if !ok || iat < float64(before) || iat > float64(time.Now().Unix()) {
		t.Errorf("iat = %v, want signing time", claims["iat"])
	}
	if input["exp"] != "ignored" || input["iat"] != "ignored" {
		t.Errorf("Sign mutated its input: %v", input)
	}
	token, err = signer.Sign(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Sign expired token: %v", err)
	}
	if _, err := signer.Verify(token); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("caller exp overrode expiresAt: %v", err)
	}
}

type rawClaims struct {
	jwt.MapClaims
	payload json.RawMessage
}

func (r rawClaims) MarshalJSON() ([]byte, error) { return r.payload, nil }

func TestVerifyRejectsMalformedClaims(t *testing.T) {
	signer := newSigner(t)
	exp := time.Now().Add(time.Hour).Unix()
	payloads := []string{"null", "[]", "123", `"claims"`, "{}", `{"exp":0}`}
	for _, invalid := range []string{"null", `"123"`, "true", "[]", "{}"} {
		payloads = append(payloads, `{"exp":`+invalid+`}`)
		for _, name := range []string{"nbf", "iat"} {
			payloads = append(payloads, fmt.Sprintf(`{"exp":%d,%q:%s}`, exp, name, invalid))
		}
	}
	for _, payload := range payloads {
		t.Run(payload, func(t *testing.T) {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, rawClaims{payload: json.RawMessage(payload)}).SignedString([]byte(testSecret))
			if err != nil {
				t.Fatalf("sign malformed claims: %v", err)
			}
			if claims, err := signer.Verify(token); err == nil || claims != nil {
				t.Errorf("accepted malformed claims: %v, error %v", claims, err)
			}
		})
	}
}

func TestVerifyTimeTolerance(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name  string
		claim string
		value int64
		valid bool
	}{
		{"recently expired", "exp", now.Add(-30 * time.Second).Unix(), true},
		{"expired beyond tolerance", "exp", now.Add(-90 * time.Second).Unix(), false},
		{"near future not before", "nbf", now.Add(30 * time.Second).Unix(), true},
		{"not before beyond tolerance", "nbf", now.Add(90 * time.Second).Unix(), false},
		{"near future issued at", "iat", now.Add(30 * time.Second).Unix(), true},
		{"issued at beyond tolerance", "iat", now.Add(90 * time.Second).Unix(), false},
		{"zero not before", "nbf", 0, true},
		{"zero issued at", "iat", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix(), tt.claim: tt.value}
			token := signRaw(t, jwt.SigningMethodHS256, []byte(testSecret), claims)
			_, err := newSigner(t).Verify(token)
			if (err == nil) != tt.valid {
				t.Errorf("Verify error = %v, want valid %t", err, tt.valid)
			}
		})
	}
}

func TestVerifyNumericDateRange(t *testing.T) {
	signer := newSigner(t)
	for _, name := range []string{"exp", "nbf", "iat"} {
		for _, value := range []float64{1e30, -1e30, 0x1p63, -0x1p63, 9223372000000000000, -62135596801, 253402300800} {
			t.Run(fmt.Sprintf("%s=%v", name, value), func(t *testing.T) {
				claims := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix(), name: value}
				token := signRaw(t, jwt.SigningMethodHS256, []byte(testSecret), claims)
				if _, err := signer.Verify(token); err == nil {
					t.Fatal("accepted NumericDate outside supported calendar range")
				}
			})
		}
	}
	token := signRaw(t, jwt.SigningMethodHS256, []byte(testSecret), jwt.MapClaims{
		"exp": int64(253402300799), "nbf": int64(-62135596800), "iat": int64(-62135596800),
	})
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("dates at supported boundaries: %v", err)
	}
}

func TestVerifyRejectsNoncanonicalSignature(t *testing.T) {
	signer := newSigner(t)
	token, err := signer.Sign(nil, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, token[len(token)-1])
	// An HS256 signature has unused low padding bits in its last character.
	altered := token[:len(token)-1] + string(alphabet[last|1])
	for _, invalid := range []string{altered, token + "="} {
		if _, err := signer.Verify(invalid); err == nil {
			t.Fatal("accepted a noncanonical signature")
		}
	}
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
