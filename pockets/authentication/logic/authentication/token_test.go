package authentication

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// --- compile-time seam assertion ---

var _ cryptids.JWTSigner = (*fakeSigner)(nil)

// fakeSigner is an honest in-package cryptids.JWTSigner (cut refinement 10 keeps
// golang-jwt out of the pocket core; the real integration is exercised
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
	users := newFakeUsers()
	h := &harness{
		users:  users,
		idents: newFakeIdentifiers(users),
		pw:     newFakePasswords(),
		sess:   newFakeSessions(),
		ch:     newFakeChallenges(),
		prot:   newFakeProtector("k1", "k1"),
		hasher: &fakeHasher{},
		mailer: &recordingMailer{},
		events: newSpySecurityEvents(),
	}
	if limiter == nil {
		limiter = ratelimiter.NewMemory()
	}
	h.svc = newServiceWithFakes(constructorConfig{
		Users:                h.users,
		Identifiers:          h.idents,
		Passwords:            h.pw,
		Sessions:             h.sess,
		Challenges:           h.ch,
		Protector:            h.prot,
		Hasher:               h.hasher,
		Limiter:              limiter,
		RequireVerifiedEmail: requireVerified,
		TokenSigner:          signer,
		SecurityEvents:       h.events,
	})
	wireSyncDelivery(t, h.svc, h.mailer, nil)
	return h
}

// mustVerify marks a just-registered user's email verified via the code mailed to
// that address (matched by recipient so interleaved registrations do not collide).
func (h *harness) mustVerify(t *testing.T, email string) {
	t.Helper()
	normalized, err := (identifier.DefaultNormalizer{}).Normalize(string(identifier.KindEmail), email)
	if err != nil {
		t.Fatalf("normalize %q: %v", email, err)
	}
	if err := h.svc.Verify(context.Background(), email, h.mailer.codeFor(t, normalized)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// --- TokenEnabled / deny-by-absence ---

func TestTokenEnabled(t *testing.T) {
	// The signer is now required (D3), so TokenEnabled is always true on a built
	// Service; the transport still gates POST /auth/token on it for symmetry.
	on := newTokenHarness(t, newFakeSigner(), false, nil)
	if !on.svc.TokenEnabled() {
		t.Error("TokenEnabled false with a signer wired")
	}
}

// --- IssueToken ---

func TestIssueTokenRoundTrip(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "iss@example.com", "password123456789")

	pair, err := h.svc.IssueToken(context.Background(), "Iss@example.com", "password123456789")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("IssueToken returned an incomplete pair: %+v", pair)
	}
	if !(strings.Count(pair.AccessToken, ".") == 2) {
		t.Errorf("issued access token is not JWT-shaped: %q", pair.AccessToken)
	}
	// Expiry is now + the default 15m access TTL (within a small slack).
	want := time.Now().Add(defaultAccessTokenTTL)
	if d := pair.AccessExpiresAt.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("AccessExpiresAt = %v, want ~%v", pair.AccessExpiresAt, want)
	}
	// The access token resolves back to the same user identity.
	gotID, _, ok := h.svc.verifyBearerClaims(pair.AccessToken)
	if !ok || gotID != u.ID {
		t.Errorf("verifyBearerClaims = (%q, %v), want (%q, true)", gotID, ok, u.ID)
	}
}

func TestIssueTokenCustomTTL(t *testing.T) {
	users := newFakeUsers()
	h := &harness{
		users: users, idents: newFakeIdentifiers(users), pw: newFakePasswords(), sess: newFakeSessions(),
		ch: newFakeChallenges(), prot: newFakeProtector("k1", "k1"),
		hasher: &fakeHasher{}, mailer: &recordingMailer{},
	}
	h.svc = newServiceWithFakes(constructorConfig{
		Users: h.users, Identifiers: h.idents, Passwords: h.pw, Sessions: h.sess,
		Challenges: h.ch, Protector: h.prot,
		Hasher:      h.hasher,
		Limiter:     ratelimiter.NewMemory(),
		TokenSigner: newFakeSigner(), AccessTokenTTL: 5 * time.Minute,
	})
	wireSyncDelivery(t, h.svc, h.mailer, nil)
	h.mustRegister(t, "ttl@example.com", "password123456789")
	pair, err := h.svc.IssueToken(context.Background(), "ttl@example.com", "password123456789")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	want := time.Now().Add(5 * time.Minute)
	if d := pair.AccessExpiresAt.Sub(want); d < -2*time.Second || d > 2*time.Second {
		t.Errorf("AccessExpiresAt = %v, want ~%v (custom 5m TTL)", pair.AccessExpiresAt, want)
	}
}

func TestIssueTokenWrongPassword(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	h.mustRegister(t, "wp@example.com", "password123456789")
	if _, err := h.svc.IssueToken(context.Background(), "wp@example.com", "nope"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("wrong password: err=%v, want ErrUnauthorized", err)
	}
}

func TestIssueTokenUnknownEmail(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	if _, err := h.svc.IssueToken(context.Background(), "ghost@example.com", "password123456789"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("unknown email: err=%v, want ErrUnauthorized", err)
	}
}

func TestIssueTokenRateLimitedFirst(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, denyLimiter{})
	h.mustRegister(t, "rl@example.com", "password123456789")
	before := h.idents.loginCalls
	_, err := h.svc.IssueToken(context.Background(), "rl@example.com", "password123456789")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: err=%v, want ErrRateLimited", err)
	}
	if h.idents.loginCalls != before {
		t.Error("rate limit did not short-circuit before resolving the login identifier")
	}
}

func TestIssueTokenRequireVerifiedEmailBlocksUnverified(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), true, nil)
	h.mustRegister(t, "unv@example.com", "password123456789") // unverified
	_, err := h.svc.IssueToken(context.Background(), "unv@example.com", "password123456789")
	if !errors.Is(err, ErrEmailNotVerified) {
		t.Errorf("unverified issue: err=%v, want ErrEmailNotVerified", err)
	}
}

func TestIssueTokenRequireVerifiedEmailAllowsVerified(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), true, nil)
	h.mustRegister(t, "ver@example.com", "password123456789")
	h.mustVerify(t, "ver@example.com")
	if _, err := h.svc.IssueToken(context.Background(), "ver@example.com", "password123456789"); err != nil {
		t.Errorf("verified issue: %v", err)
	}
}

// --- bearer verification through the middleware trio ---
