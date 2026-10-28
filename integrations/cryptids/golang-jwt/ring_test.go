package golangjwt_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

var (
	ringKey1 = []byte(strings.Repeat("1", 32))
	ringKey2 = []byte(strings.Repeat("2", 48))
)

// keyRingReporter is the authentication core's KeyRingReporter shape, kept
// here so this module proves it without importing the pocket.
type keyRingReporter interface {
	ActiveKeyID() string
	KeyIDs() []string
	LegacyKeyID() string
}

var _ keyRingReporter = (*golangjwt.RingSigner)(nil)

func newRing(t *testing.T, active string, keys map[string][]byte, opts ...golangjwt.RingOption) *golangjwt.RingSigner {
	t.Helper()
	r, err := golangjwt.NewRing(cryptids.KeyRing{Active: active, Keys: keys}, opts...)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	return r
}

// header decodes a token's JOSE header without verifying it.
func header(t *testing.T, token string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(token, ".", 2)[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return h
}

// signWithHeader signs claims with golang-jwt directly under method and key,
// setting the JOSE headers in extra (a nil value deletes the header).
func signWithHeader(t *testing.T, method jwt.SigningMethod, key any, extra map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(method, jwt.MapClaims{
		"user_id": "u1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	for k, v := range extra {
		if v == nil {
			delete(token.Header, k)
			continue
		}
		token.Header[k] = v
	}
	s, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return s
}

func mustSign(t *testing.T, s cryptids.JWTSigner) string {
	t.Helper()
	token, err := s.Sign(map[string]any{"user_id": "u1"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return token
}

func TestRingRoundTrip(t *testing.T) {
	r := newRing(t, "k1", map[string][]byte{"k1": ringKey1, "k2": ringKey2})
	expiresAt := time.Now().Add(time.Hour)
	token, err := r.Sign(map[string]any{"user_id": "u1"}, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if h := header(t, token); h["kid"] != "k1" || h["alg"] != "HS256" {
		t.Fatalf("header = %v, want kid k1 alg HS256", h)
	}
	claims, err := r.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["user_id"] != "u1" || claims["exp"] != float64(expiresAt.Unix()) {
		t.Errorf("claims = %v", claims)
	}
	if _, err := r.Verify(""); !errors.Is(err, golangjwt.ErrEmptyToken) {
		t.Errorf("empty token err = %v", err)
	}
}

// TestRingRotationPhases walks the three-phase rotation: (a) the legacy key
// is active and the ring writes what the single-key Signer writes; (b) the new
// key is active and old tokens still verify; (c) the legacy key is dropped and
// old tokens are refused.
func TestRingRotationPhases(t *testing.T) {
	single := newSigner(t)
	legacyKey := []byte(testSecret)
	t0 := mustSign(t, single)

	// Phase (a).
	phaseA := newRing(t, "legacy", map[string][]byte{"legacy": legacyKey, "k2": ringKey2}, golangjwt.WithLegacyKeyID("legacy"))
	ta := mustSign(t, phaseA)
	if _, has := header(t, ta)["kid"]; has {
		t.Fatalf("phase (a) token carries a kid: %v", header(t, ta))
	}
	if _, err := single.Verify(ta); err != nil {
		t.Fatalf("single-key Signer rejects a phase (a) token: %v", err)
	}
	if _, err := phaseA.Verify(t0); err != nil {
		t.Fatalf("phase (a) ring rejects a single-key token: %v", err)
	}

	// Phase (b).
	phaseB := newRing(t, "k2", map[string][]byte{"legacy": legacyKey, "k2": ringKey2}, golangjwt.WithLegacyKeyID("legacy"))
	t1 := mustSign(t, phaseB)
	if kid := header(t, t1)["kid"]; kid != "k2" {
		t.Fatalf("phase (b) kid = %v, want k2", kid)
	}
	for name, token := range map[string]string{"single-key": t0, "phase (a)": ta, "phase (b)": t1} {
		if _, err := phaseB.Verify(token); err != nil {
			t.Errorf("phase (b) ring rejects a %s token: %v", name, err)
		}
	}
	if _, err := phaseA.Verify(t1); err != nil {
		t.Errorf("a phase (a) instance rejects a phase (b) token mid-deploy: %v", err)
	}

	// Phase (c).
	phaseC := newRing(t, "k2", map[string][]byte{"k2": ringKey2})
	if _, err := phaseC.Verify(t1); err != nil {
		t.Fatalf("phase (c) ring rejects a phase (b) token: %v", err)
	}
	for name, token := range map[string]string{"single-key": t0, "phase (a)": ta} {
		if _, err := phaseC.Verify(token); !errors.Is(err, golangjwt.ErrMissingKeyID) {
			t.Errorf("phase (c) %s token err = %v, want ErrMissingKeyID", name, err)
		}
	}
}

func TestRingUnknownKeyID(t *testing.T) {
	r := newRing(t, "k1", map[string][]byte{"k1": ringKey1}, golangjwt.WithLegacyKeyID("k1"))
	for _, tt := range []struct {
		name, echo string
		kid        any
		key        []byte
	}{
		{"absent key", `"k2"`, "k2", ringKey2},
		// Signed under a ring key: refused because no other key is tried.
		{"absent id, ring key", `"k9"`, "k9", ringKey1},
		{"malformed", "invalid", "../K1", ringKey1},
		{"empty", "invalid", "", ringKey1},
		{"not a string", "invalid", 1, ringKey1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Verify(signWithHeader(t, jwt.SigningMethodHS256, tt.key, map[string]any{"kid": tt.kid}))
			if !errors.Is(err, golangjwt.ErrUnknownKeyID) {
				t.Fatalf("err = %v, want ErrUnknownKeyID", err)
			}
			if !strings.Contains(err.Error(), tt.echo) {
				t.Errorf("err %q lacks %s", err, tt.echo)
			}
			if strings.Contains(err.Error(), "../K1") || strings.Contains(err.Error(), string(tt.key)) {
				t.Errorf("err %q echoes the kid or a key", err)
			}
		})
	}
}

func TestRingTokenWithoutKeyID(t *testing.T) {
	keys := map[string][]byte{"legacy": []byte(testSecret), "k1": ringKey1}
	legacyToken := mustSign(t, newSigner(t))

	without := newRing(t, "k1", keys)
	if _, err := without.Verify(legacyToken); !errors.Is(err, golangjwt.ErrMissingKeyID) {
		t.Fatalf("without legacy option err = %v, want ErrMissingKeyID", err)
	}

	with := newRing(t, "k1", keys, golangjwt.WithLegacyKeyID("legacy"))
	if _, err := with.Verify(legacyToken); err != nil {
		t.Fatalf("with legacy option: %v", err)
	}
	// A kid-less token verifies under the legacy key only, never the active one.
	activeNoKid := signWithHeader(t, jwt.SigningMethodHS256, ringKey1, nil)
	if _, err := with.Verify(activeNoKid); !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Fatalf("kid-less token under the active key err = %v, want ErrTokenSignatureInvalid", err)
	}
}

func TestRingAlgorithmConfusionRejected(t *testing.T) {
	r := newRing(t, "k1", map[string][]byte{"k1": ringKey1}, golangjwt.WithLegacyKeyID("k1"))
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		method jwt.SigningMethod
		key    any
	}{
		{"HS384 same key", jwt.SigningMethodHS384, ringKey1},
		{"HS512 same key", jwt.SigningMethodHS512, ringKey1},
		{"RS256", jwt.SigningMethodRS256, rsaKey},
		{"ES256", jwt.SigningMethodES256, ecKey},
		{"none", jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType},
	} {
		for _, kid := range []any{"k1", "k9", nil} {
			token := signWithHeader(t, tt.method, tt.key, map[string]any{"kid": kid})
			_, err := r.Verify(token)
			if err == nil {
				t.Fatalf("%s kid=%v verified", tt.name, kid)
			}
			// The method pin fires before the kid is read.
			if errors.Is(err, golangjwt.ErrUnknownKeyID) || errors.Is(err, golangjwt.ErrMissingKeyID) {
				t.Errorf("%s kid=%v reached the key lookup: %v", tt.name, kid, err)
			}
			if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
				t.Errorf("%s kid=%v err = %v, want ErrTokenSignatureInvalid", tt.name, kid, err)
			}
		}
	}
}

func TestRingSharesSignerClaimChecks(t *testing.T) {
	r := newRing(t, "k1", map[string][]byte{"k1": ringKey1})
	for name, claims := range map[string]jwt.MapClaims{
		"expired":     {"exp": time.Now().Add(-2 * time.Minute).Unix()},
		"missing exp": {"iat": time.Now().Unix()},
		"exp too big": {"exp": float64(1e300)},
	} {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		token.Header["kid"] = "k1"
		s, err := token.SignedString(ringKey1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Verify(s); err == nil {
			t.Errorf("%s token verified", name)
		}
	}
}

func TestNewRingValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		ring cryptids.KeyRing
		opts []golangjwt.RingOption
		want error
	}{
		{"empty", cryptids.KeyRing{Active: "k1"}, nil, cryptids.ErrKeyRingEmpty},
		{"short key", cryptids.KeyRing{Active: "k1", Keys: map[string][]byte{"k1": ringKey1[:31]}}, nil, cryptids.ErrKeyMalformed},
		{"active missing", cryptids.KeyRing{Active: "k2", Keys: map[string][]byte{"k1": ringKey1}}, nil, cryptids.ErrKeyRingActiveMissing},
		{"bad id", cryptids.KeyRing{Active: "K1", Keys: map[string][]byte{"K1": ringKey1}}, nil, cryptids.ErrKeyMalformed},
		{"legacy missing", cryptids.KeyRing{Active: "k1", Keys: map[string][]byte{"k1": ringKey1}}, []golangjwt.RingOption{golangjwt.WithLegacyKeyID("legacy")}, cryptids.ErrLegacyKeyMissing},
		{"nil option", cryptids.KeyRing{Active: "k1", Keys: map[string][]byte{"k1": ringKey1}}, []golangjwt.RingOption{nil}, sdk.ErrInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := golangjwt.NewRing(tt.ring, tt.opts...)
			if r != nil || !errors.Is(err, tt.want) {
				t.Fatalf("NewRing = %v, %v; want %v", r, err, tt.want)
			}
		})
	}
	long := []byte(strings.Repeat("l", 64))
	r := newRing(t, "k1", map[string][]byte{"k1": long})
	if _, err := r.Verify(signWithHeader(t, jwt.SigningMethodHS256, long, map[string]any{"kid": "k1"})); err != nil {
		t.Fatalf("64-byte key: %v", err)
	}
}

func TestNewRingCopiesKeys(t *testing.T) {
	key := slices.Clone(ringKey1)
	keys := map[string][]byte{"k1": key}
	r := newRing(t, "k1", keys)
	token := mustSign(t, r)
	key[0] ^= 0xff
	keys["k9"] = ringKey2
	if _, err := r.Verify(token); err != nil {
		t.Fatalf("caller mutation reached the signer: %v", err)
	}
	if ids := r.KeyIDs(); !slices.Equal(ids, []string{"k1"}) {
		t.Fatalf("KeyIDs = %v after caller added a key", ids)
	}
}

func TestRingReportsKeyIDs(t *testing.T) {
	keys := map[string][]byte{}
	for _, id := range []string{"k5", "a1", "legacy", "k2", "z9", "b3"} {
		keys[id] = ringKey1
	}
	r := newRing(t, "k2", keys, golangjwt.WithLegacyKeyID("legacy"))
	want := []string{"a1", "b3", "k2", "k5", "legacy", "z9"}
	for range 10 {
		ids := r.KeyIDs()
		if !slices.Equal(ids, want) {
			t.Fatalf("KeyIDs = %v, want %v", ids, want)
		}
		ids[0] = "mutated"
	}
	if r.ActiveKeyID() != "k2" || r.LegacyKeyID() != "legacy" {
		t.Errorf("ActiveKeyID = %q, LegacyKeyID = %q", r.ActiveKeyID(), r.LegacyKeyID())
	}
	if got := newRing(t, "k2", keys).LegacyKeyID(); got != "" {
		t.Errorf("LegacyKeyID without option = %q", got)
	}
}

func TestUninitializedRingSigner(t *testing.T) {
	var zero golangjwt.RingSigner
	var nilRing *golangjwt.RingSigner
	for _, s := range []*golangjwt.RingSigner{&zero, nilRing} {
		if _, err := s.Sign(nil, time.Now().Add(time.Hour)); err == nil {
			t.Error("uninitialized Sign succeeded")
		}
		if _, err := s.Verify("a.b.c"); err == nil {
			t.Error("uninitialized Verify succeeded")
		}
	}
}
