package authentication

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// --- compile-time seam assertion ---

var _ cryptids.JWTSigner = (*fakeSigner)(nil)

// verifyBearerClaims projects first-party identity for existing login fixtures.
func (s *Service) verifyBearerClaims(raw string) (userID, sessionID string, ok bool) {
	userID, cred, ok := s.verifyAccessClaims(raw)
	if !ok || cred.Profile != session.ProfileFirstParty {
		return "", "", false
	}
	return userID, cred.SessionID, true
}

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

// TestAccessTokenAssuranceClaim pins the aal claim: signed from the session's
// level, omitted for a row that recorded none, only aal1/aal2 admitted on a
// first-party token, and refused on a delegated one.
func TestAccessTokenAssuranceClaim(t *testing.T) {
	h := newHarness(t, nil)
	for level, want := range map[session.AssuranceLevel]bool{session.AssuranceAAL1: true, session.AssuranceAAL2: true, session.AssuranceUnknown: false} {
		raw, _, err := h.svc.signAccessToken("u1", "s1", session.AuthenticationMetadata{Assurance: level})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := h.signer.Verify(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := claims["aal"]; present != want {
			t.Errorf("level %q: aal present = %v, want %v", level, present, want)
		}
		if _, cred, ok := parseAccessClaims(claims); !ok || cred.Assurance != level {
			t.Errorf("level %q parsed as %q, %v", level, cred.Assurance, ok)
		}
	}
	firstParty := func(aal any) map[string]any {
		return map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "first_party", "aal": aal}
	}
	for _, bad := range []any{"aal3", "AAL2", "", 2, nil} {
		if _, _, ok := parseAccessClaims(firstParty(bad)); ok {
			t.Errorf("first-party aal %#v admitted", bad)
		}
	}
	delegated := map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "delegated",
		"exp": float64(time.Now().Add(time.Minute).Unix()), "iss": "https://as.example.test", "client_id": "client", "aud": "https://rs.example.test"}
	if _, _, ok := parseAccessClaims(delegated); !ok {
		t.Fatal("the delegated fixture does not parse")
	}
	delegated["aal"] = "aal2"
	if _, _, ok := parseAccessClaims(delegated); ok {
		t.Error("a delegated token carrying aal was admitted")
	}

	h.mustRegister(t, "aal@example.com", "password123456789")
	h.mustVerify(t, "aal@example.com")
	pair, _, err := h.svc.Login(context.Background(), "aal@example.com", "password123456789")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"login": pair.AccessToken, "refresh": rotated.AccessToken} {
		if _, cred, ok := h.svc.verifyAccessClaims(raw); !ok || cred.Assurance != session.AssuranceAAL1 {
			t.Errorf("%s token assurance = %q, want aal1", name, cred.Assurance)
		}
	}
}

// TestAccessTokenMethodsClaim pins the methods claim: signed from the session
// row's method kinds (each once, in order), omitted for a row that recorded
// none, parsed into Credential.Methods, refused when it names an unknown,
// duplicate or external kind or is not a non-empty string array, and refused on
// a delegated token.
func TestAccessTokenMethodsClaim(t *testing.T) {
	h := newHarness(t, nil)
	pw, _ := session.DescribeMethod(session.MethodPassword)
	code, _ := session.DescribeMethod(session.MethodTOTP)
	raw, _, err := h.svc.signAccessToken("u1", "s1", session.AuthenticationMetadata{
		Assurance: session.AssuranceAAL2, Methods: []session.AuthenticationMethod{pw, code, pw},
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := h.signer.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := claims["methods"].([]any); !ok || len(got) != 2 || got[0] != "password" || got[1] != "totp" {
		t.Fatalf("methods claim = %#v", claims["methods"])
	}
	if _, cred, ok := parseAccessClaims(claims); !ok || !slices.Equal(cred.Methods, []session.MethodKind{session.MethodPassword, session.MethodTOTP}) {
		t.Fatalf("parsed methods = %v, %v", cred.Methods, ok)
	}
	bare, _, err := h.svc.signAccessToken("u1", "s1", session.AuthenticationMetadata{Assurance: session.AssuranceAAL1})
	if err != nil {
		t.Fatal(err)
	}
	if claims, _ := h.signer.Verify(bare); claims["methods"] != nil {
		t.Fatalf("a row without methods signed %#v", claims["methods"])
	}
	firstParty := func(methods any) map[string]any {
		return map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "first_party", "aal": "aal2", "methods": methods}
	}
	for _, bad := range []any{[]any{}, []any{"sms_push"}, []any{"external"}, []any{"totp", "totp"}, []any{2}, "totp", nil} {
		if _, _, ok := parseAccessClaims(firstParty(bad)); ok {
			t.Errorf("first-party methods %#v admitted", bad)
		}
	}
	delegated := map[string]any{"user_id": "u1", "session_id": "s1", "session_profile": "delegated",
		"exp": float64(time.Now().Add(time.Minute).Unix()), "iss": "https://as.example.test", "client_id": "client", "aud": "https://rs.example.test",
		"methods": []any{"password"}}
	if _, _, ok := parseAccessClaims(delegated); ok {
		t.Error("a delegated token carrying methods was admitted")
	}

	h.mustRegister(t, "methods@example.com", "password123456789")
	h.mustVerify(t, "methods@example.com")
	pair, _, err := h.svc.Login(context.Background(), "methods@example.com", "password123456789")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"login": pair.AccessToken, "refresh": rotated.AccessToken} {
		if _, cred, ok := h.svc.verifyAccessClaims(raw); !ok || !slices.Equal(cred.Methods, []session.MethodKind{session.MethodPassword}) {
			t.Errorf("%s token methods = %v, want [password]", name, cred.Methods)
		}
	}
}
