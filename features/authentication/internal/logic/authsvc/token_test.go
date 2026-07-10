package authsvc

import (
	"context"
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

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// --- compile-time seam assertion ---

var _ cryptids.JWTSigner = (*fakeSigner)(nil)

// fakeSigner is an honest in-package cryptids.JWTSigner (cut refinement 10 keeps
// golang-jwt out of the feature core; the real integration is exercised
// host-side in A9). It genuinely verifies: it HMAC-SHA256s a base64url JSON
// claims payload (with the expiry encoded) under a test secret, and Verify
// rejects expired tokens (encoded exp checked against the clock) AND
// tampered/badly-signed tokens (recomputed MAC compared in constant time). The
// tokens are two-dot shaped so isJWTToken classes them as JWTs.
type fakeSigner struct {
	secret []byte
	now    func() time.Time
}

func newFakeSigner() *fakeSigner {
	return &fakeSigner{secret: []byte("test-secret-not-for-production-use"), now: time.Now}
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
	if f.now().After(time.Unix(int64(expF), 0)) {
		return nil, errors.New("fakeSigner: token expired")
	}
	return payload, nil
}

func (f *fakeSigner) mac(b string) string {
	m := hmac.New(sha256.New, f.secret)
	m.Write([]byte(b))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// newTokenHarness builds a harness with the JWT bearer mode wired.
func newTokenHarness(t *testing.T, signer cryptids.JWTSigner, requireVerified bool, limiter ratelimiter.Limiter) *harness {
	t.Helper()
	h := &harness{
		users:  newFakeUsers(),
		pw:     newFakePasswords(),
		sess:   newFakeSessions(),
		codes:  newFakeCodes(),
		tokens: newFakeTokens(),
		hasher: &fakeHasher{},
		mailer: &recordingMailer{},
		events: newSpySecurityEvents(),
	}
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	h.svc = NewService(Deps{
		Users:                h.users,
		Passwords:            h.pw,
		Sessions:             h.sess,
		Codes:                h.codes,
		Tokens:               h.tokens,
		Hasher:               h.hasher,
		Mailer:               h.mailer,
		MailFrom:             "noreply@example.com",
		Limiter:              limiter,
		Cookie:               CookieConfig{},
		RequireVerifiedEmail: requireVerified,
		TokenSigner:          signer,
		SecurityEvents:       h.events,
	})
	return h
}

// mustVerify marks a just-registered user's email verified via its code.
func (h *harness) mustVerify(t *testing.T, email string) {
	t.Helper()
	var code string
	for c, rec := range h.codes.m {
		u, _ := h.users.GetByEmail(context.Background(), email)
		if rec.UserID == u.ID {
			code = c
		}
	}
	if code == "" {
		t.Fatalf("no verification code for %s", email)
	}
	if err := h.svc.Verify(context.Background(), code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// --- TokenEnabled / deny-by-absence ---

func TestTokenEnabled(t *testing.T) {
	on := newTokenHarness(t, newFakeSigner(), false, nil)
	if !on.svc.TokenEnabled() {
		t.Error("TokenEnabled false with a signer wired")
	}
	off := newHarness(t, nil)
	if off.svc.TokenEnabled() {
		t.Error("TokenEnabled true with no signer")
	}
}

// --- IssueToken ---

func TestIssueTokenRoundTrip(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "iss@example.com", "password123")

	tok, expiresAt, err := h.svc.IssueToken(context.Background(), "Iss@example.com", "password123")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if tok == "" {
		t.Fatal("IssueToken returned an empty token")
	}
	if !isJWTToken(tok) {
		t.Errorf("issued token is not JWT-shaped: %q", tok)
	}
	// Expiry is now + the default 1h TTL (within a small slack).
	want := time.Now().Add(defaultTokenTTL)
	if d := expiresAt.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("expiresAt = %v, want ~%v", expiresAt, want)
	}
	// The token resolves back to the same user identity.
	gotID, ok := h.svc.verifyBearer(tok)
	if !ok || gotID != u.ID {
		t.Errorf("verifyBearer = (%q, %v), want (%q, true)", gotID, ok, u.ID)
	}
}

func TestIssueTokenCustomTTL(t *testing.T) {
	h := &harness{
		users: newFakeUsers(), pw: newFakePasswords(), sess: newFakeSessions(),
		codes: newFakeCodes(), tokens: newFakeTokens(), hasher: &fakeHasher{}, mailer: &recordingMailer{},
	}
	h.svc = NewService(Deps{
		Users: h.users, Passwords: h.pw, Sessions: h.sess, Codes: h.codes, Tokens: h.tokens,
		Hasher: h.hasher, Mailer: h.mailer, MailFrom: "noreply@example.com",
		Limiter: ratelimiter.NewMemory(), Cookie: CookieConfig{},
		TokenSigner: newFakeSigner(), TokenTTL: 5 * time.Minute,
	})
	h.mustRegister(t, "ttl@example.com", "password123")
	_, expiresAt, err := h.svc.IssueToken(context.Background(), "ttl@example.com", "password123")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	want := time.Now().Add(5 * time.Minute)
	if d := expiresAt.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("expiresAt = %v, want ~%v (custom 5m TTL)", expiresAt, want)
	}
}

func TestIssueTokenWrongPassword(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	h.mustRegister(t, "wp@example.com", "password123")
	if _, _, err := h.svc.IssueToken(context.Background(), "wp@example.com", "nope"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("wrong password: err=%v, want ErrUnauthorized", err)
	}
}

func TestIssueTokenUnknownEmail(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	if _, _, err := h.svc.IssueToken(context.Background(), "ghost@example.com", "password123"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("unknown email: err=%v, want ErrUnauthorized", err)
	}
}

func TestIssueTokenRateLimitedFirst(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, denyLimiter{})
	h.mustRegister(t, "rl@example.com", "password123")
	before := h.users.calls
	_, _, err := h.svc.IssueToken(context.Background(), "rl@example.com", "password123")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: err=%v, want ErrRateLimited", err)
	}
	if h.users.calls != before {
		t.Error("rate limit did not short-circuit before touching the user store")
	}
}

func TestIssueTokenRequireVerifiedEmailBlocksUnverified(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), true, nil)
	h.mustRegister(t, "unv@example.com", "password123") // unverified
	_, _, err := h.svc.IssueToken(context.Background(), "unv@example.com", "password123")
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Errorf("unverified issue: err=%v, want ErrEmailNotVerified", err)
	}
}

func TestIssueTokenRequireVerifiedEmailAllowsVerified(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), true, nil)
	h.mustRegister(t, "ver@example.com", "password123")
	h.mustVerify(t, "ver@example.com")
	if _, _, err := h.svc.IssueToken(context.Background(), "ver@example.com", "password123"); err != nil {
		t.Errorf("verified issue: %v", err)
	}
}

// --- bearer verification through the middleware trio ---

func TestRequireUserBearerJWT(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "bru@example.com", "password123")
	tok, _, err := h.svc.IssueToken(context.Background(), "bru@example.com", "password123")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	var gotID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.svc.CurrentUser(r.Context())
		if !ok {
			t.Error("CurrentUser not set inside RequireUser via bearer JWT")
		}
		gotID = id
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequireUser(next).ServeHTTP(rec, bearerRequest(tok))
	if rec.Code != http.StatusNoContent || gotID != u.ID {
		t.Errorf("bearer RequireUser: status=%d id=%q, want 204 %q", rec.Code, gotID, u.ID)
	}
}

func TestRequirePrincipalBearerJWT(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "brp@example.com", "password123")
	tok, _, _ := h.svc.IssueToken(context.Background(), "brp@example.com", "password123")

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal(next).ServeHTTP(rec, bearerRequest(tok))
	if rec.Code != http.StatusNoContent || got.Type != PrincipalUser || got.ID != u.ID {
		t.Errorf("bearer RequirePrincipal: status=%d principal=%+v, want 204 {user, %s}", rec.Code, got, u.ID)
	}
}

func TestBearerExpiredDenied(t *testing.T) {
	signer := newFakeSigner()
	h := newTokenHarness(t, signer, false, nil)
	// A genuinely-expired token (exp in the past) — the honest fake rejects it.
	expired, err := signer.Sign(map[string]any{tokenClaimUserID: "u1"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	assertBearerDenied(t, h, expired, "expired")
}

func TestBearerTamperedSignatureDenied(t *testing.T) {
	signer := newFakeSigner()
	h := newTokenHarness(t, signer, false, nil)
	valid, _ := signer.Sign(map[string]any{tokenClaimUserID: "u1"}, time.Now().Add(time.Hour))
	tampered := flipLastRune(valid)
	if tampered == valid {
		t.Fatal("failed to tamper the signature")
	}
	assertBearerDenied(t, h, tampered, "tampered")
}

func TestBearerGarbageDenied(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	// Two-dot garbage classes as a JWT, then Verify fails — no panic, 401.
	assertBearerDenied(t, h, "aaa.bbb.ccc", "garbage")
}

func TestBearerWrongSecretDenied(t *testing.T) {
	// A token signed by a DIFFERENT secret must not verify against this service.
	other := &fakeSigner{secret: []byte("a-totally-different-secret-value!"), now: time.Now}
	forged, _ := other.Sign(map[string]any{tokenClaimUserID: "u1"}, time.Now().Add(time.Hour))
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	assertBearerDenied(t, h, forged, "wrong-secret")
}

func TestBearerSignerNilInert(t *testing.T) {
	// No signer wired: a JWT-shaped bearer is never parsed; RequireUser and
	// RequirePrincipal both deny (deny-by-absence, A3 behavior unchanged).
	h := newHarness(t, nil)
	if h.svc.TokenEnabled() {
		t.Fatal("TokenEnabled true with no signer")
	}
	valid, _ := newFakeSigner().Sign(map[string]any{tokenClaimUserID: "u1"}, time.Now().Add(time.Hour))
	assertBearerDenied(t, h, valid, "signer-nil")
}

// assertBearerDenied asserts that both RequireUser and RequirePrincipal reject a
// bearer token with 401 and never call next.
func assertBearerDenied(t *testing.T, h *harness, token, label string) {
	t.Helper()
	for name, mw := range map[string]func(http.Handler) http.Handler{
		"RequireUser":      h.svc.RequireUser,
		"RequirePrincipal": h.svc.RequirePrincipal,
	} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, bearerRequest(token))
		if rec.Code != http.StatusUnauthorized || called {
			t.Errorf("%s(%s): status=%d called=%v, want 401 not-called", name, label, rec.Code, called)
		}
	}
}

// flipLastRune returns s with its final byte flipped to a different base64url
// character, corrupting the signature segment.
func flipLastRune(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}
