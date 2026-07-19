package authentication

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// var _ pins the seam: the fake signer satisfies the sdk-owned JWTSigner port.
var _ cryptids.JWTSigner = (*fakeSigner)(nil)

// fakeSigner is an honest in-package cryptids.JWTSigner (golang-jwt stays out of
// the feature core — cut refinement 10). It HMAC-SHA256s a base64url JSON claims
// payload with the expiry encoded, verifies the MAC in constant time, and
// rejects expired tokens. Two-dot shaped so isJWTToken classes it as a JWT.
type fakeSigner struct {
	secret []byte
}

func newFakeSigner() *fakeSigner {
	return &fakeSigner{secret: []byte("test-secret-not-for-production-use")}
}

func (f *fakeSigner) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	payload := make(map[string]any, len(claims)+1)
	for k, v := range claims {
		payload[k] = v
	}
	payload["exp"] = expiresAt.Unix()
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	b := base64.RawURLEncoding.EncodeToString(body)
	return "fake." + b + "." + f.mac(b), nil
}

func (f *fakeSigner) Verify(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "fake" {
		return nil, errors.New("fakeSigner: malformed token")
	}
	if !hmac.Equal([]byte(parts[2]), []byte(f.mac(parts[1]))) {
		return nil, errors.New("fakeSigner: bad signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	expF, ok := payload["exp"].(float64)
	if !ok {
		return nil, errors.New("fakeSigner: missing exp")
	}
	if time.Now().After(time.Unix(int64(expF), 0)) {
		return nil, errors.New("fakeSigner: token expired")
	}
	return payload, nil
}

func (f *fakeSigner) mac(b string) string {
	m := hmac.New(sha256.New, f.secret)
	m.Write([]byte(b))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// newTokenHandler builds a real authsvc.Service with the JWT bearer mode wired
// and mounts the full route table.
func newTokenHandler(t *testing.T, signer cryptids.JWTSigner, requireVerified bool, limiter ratelimiter.Limiter) http.Handler {
	t.Helper()
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	users := newMemUsers()
	svc := authsvc.NewService(authsvc.Deps{
		Users:                users,
		Identifiers:          newMemIdentifiers(users),
		Passwords:            &memPasswords{m: map[string]string{}},
		Sessions:             &memSessions{m: map[string]session.Session{}},
		Hasher:               fakeHasher{},
		Limiter:              limiter,
		Cookie:               authsvc.CookieConfig{},
		RequireVerifiedEmail: requireVerified,
		TokenSigner:          signer,
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{}, nil, nil)
	return h
}

// bearerReq builds a request carrying an Authorization: Bearer header.
func bearerReq(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// --- issuance round-trip + bearer-backed access ---

func TestTokenRouteIssuesSessionBackedPair(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), false, nil)
	do(t, h, "POST", "/auth/register", `{"email":"tok@example.com","password":"password123456789","display_name":"T"}`)

	rec := do(t, h, "POST", "/auth/token", `{"email":"tok@example.com","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		AccessToken  string `json:"access_token"`
		ExpiresAt    string `json:"expires_at"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatalf("token response missing the session-backed pair: %+v", resp)
	}
	if _, err := time.Parse(time.RFC3339, resp.ExpiresAt); err != nil {
		t.Errorf("expires_at %q not RFC3339: %v", resp.ExpiresAt, err)
	}

	// The refresh token rotates at /auth/refresh (a new pair is issued).
	rr := do(t, h, "POST", "/auth/refresh", `{"refresh_token":"`+resp.RefreshToken+`"}`)
	if rr.Code != http.StatusOK {
		t.Errorf("refresh status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
}

// --- bearer rejection paths on a gated route ---

func TestTokenRouteBearerExpired(t *testing.T) {
	signer := newFakeSigner()
	h := newTokenHandler(t, signer, false, nil)
	// /auth/password/change is RequireLiveSession-gated; an expired bearer denies.
	expired, _ := signer.Sign(map[string]any{"user_id": "u1", "session_id": "s1"}, time.Now().Add(-time.Minute))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerReq("POST", "/auth/password/change", expired))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired bearer on gated route status = %d, want 401", rec.Code)
	}
}

func TestTokenRouteBearerGarbage(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), false, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerReq("POST", "/auth/password/change", "aaa.bbb.ccc"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage bearer on gated route status = %d, want 401", rec.Code)
	}
}

// --- gating and discipline mirror /auth/login ---

func TestTokenRouteStrictDecode(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), false, nil)
	rec := do(t, h, "POST", "/auth/token", `{"email":"a@example.com","password":"password123456789","extra":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

func TestTokenRouteWrongPassword(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), false, nil)
	do(t, h, "POST", "/auth/register", `{"email":"wp@example.com","password":"password123456789","display_name":"W"}`)
	rec := do(t, h, "POST", "/auth/token", `{"email":"wp@example.com","password":"wrongpass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}
}

func TestTokenRouteRateLimited(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), false, denyLimiter{})
	rec := do(t, h, "POST", "/auth/token", `{"email":"e@example.com","password":"password123456789"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited status = %d, want 429", rec.Code)
	}
}

func TestTokenRouteVerifiedEmailGate(t *testing.T) {
	h := newTokenHandler(t, newFakeSigner(), true, nil) // RequireVerifiedEmail on
	do(t, h, "POST", "/auth/register", `{"email":"unv@example.com","password":"password123456789","display_name":"U"}`)
	rec := do(t, h, "POST", "/auth/token", `{"email":"unv@example.com","password":"password123456789"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("unverified /auth/token status = %d, want 403", rec.Code)
	}
}
